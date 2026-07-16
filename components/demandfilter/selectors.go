// Copyright 2026 LeanSignal
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//
// SPDX-License-Identifier: Apache-2.0

// leansignaldemandfilter/selectors.go
//
// Selector-mode filtering: when the demand set carries metric SELECTORS
// (normalized `{__name__="...",label=~"...",...}` strings — the same grammar
// the log/trace demand filters consume), the filter evaluates them per
// DATAPOINT, i.e. per Prometheus series, instead of per metric name.
//
// A series' label view is exactly what the prometheusremotewrite exporter
// writes to the dataplane VM: flattened resource attributes
// (resource_to_telemetry_conversion), normalized label names
// (dots→underscores), synthesized job/instance, datapoint attributes
// overriding resource attributes. That view comes from internal/promnaming —
// the same code the metrics tracker indexes series with — so selector
// matching cannot drift from what the tracker reports or the exporter writes.
//
// `le` and `quantile` matchers are IGNORED: those labels only materialize at
// remote-write time (bucket arrays → per-le series), so the whole
// histogram/summary is forwarded when the remaining matchers match.
package leansignaldemandfilter

import (
	"strings"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.uber.org/zap"

	selectormatch "github.com/leansignal/leansignal-agent/components/selectormatch"
	"github.com/leansignal/leansignal-agent/internal/promnaming"
)

// compiledSelector is one demanded selector split for fast evaluation:
// the __name__ matchers select candidate metrics, the label matchers are
// evaluated per datapoint. le/quantile matchers are dropped at compile time.
type compiledSelector struct {
	nameMatchers  []*selectormatch.Matcher // matchers on __name__
	labelMatchers []*selectormatch.Matcher // all other matchers except le/quantile
}

// matchesName reports whether every __name__ matcher accepts the given
// Prometheus series name. A selector without a __name__ matcher constrains
// labels only and accepts every name.
func (cs *compiledSelector) matchesName(name string) bool {
	for _, m := range cs.nameMatchers {
		if !m.MatchValue(name) {
			return false
		}
	}
	return true
}

// compiledSelectorSet groups the demand's selectors for candidate lookup:
// selectors pinned to one exact name (`__name__="..."`) live in an O(1) map;
// the (usually tiny) rest — name-regex / negated / unconstrained names — is
// scanned per metric.
type compiledSelectorSet struct {
	byName map[string][]*compiledSelector
	scan   []*compiledSelector
}

func (s *compiledSelectorSet) empty() bool {
	return len(s.byName) == 0 && len(s.scan) == 0
}

// candidatesFor collects every selector whose name constraint accepts any of
// the series names this metric produces (histogram/summary family expansion
// included — matching any component series keeps the whole family, exactly
// like name-mode filtering).
func (s *compiledSelectorSet) candidatesFor(familyNames []string) []*compiledSelector {
	var out []*compiledSelector
	for _, n := range familyNames {
		out = append(out, s.byName[n]...)
		for _, cs := range s.scan {
			if cs.matchesName(n) {
				out = append(out, cs)
			}
		}
	}
	return out
}

// compileSelectors parses and indexes a raw selector list. Selectors that
// fail to parse are skipped with a warning so one bad selector cannot poison
// the rest of the demand set (mirrors the log/trace filters).
func compileSelectors(raw []string, logger *zap.Logger) *compiledSelectorSet {
	set := &compiledSelectorSet{byName: make(map[string][]*compiledSelector, len(raw))}
	for _, s := range raw {
		sel, err := selectormatch.Parse(s)
		if err != nil {
			logger.Warn("demand filter: skipping unparseable metric selector",
				zap.String("selector", s),
				zap.Error(err),
			)
			continue
		}
		cs := &compiledSelector{}
		for _, m := range sel.Matchers {
			switch m.Name {
			case "__name__":
				cs.nameMatchers = append(cs.nameMatchers, m)
			case "le", "quantile":
				// Ignored by decision: le/quantile only exist after remote-write
				// translation; forward the whole histogram/summary when the
				// remaining matchers match.
				continue
			default:
				cs.labelMatchers = append(cs.labelMatchers, m)
			}
		}
		if len(cs.nameMatchers) == 1 && cs.nameMatchers[0].Op == selectormatch.OpEq {
			name := cs.nameMatchers[0].Value
			set.byName[name] = append(set.byName[name], cs)
		} else {
			set.scan = append(set.scan, cs)
		}
	}
	return set
}

// parsedSelectors returns the compiled set for the given raw demand list,
// re-compiling only when the list changed since the previous batch (cache
// keyed by the joined raw list — same pattern as the log/trace filters).
func (p *demandFilterProcessor) parsedSelectors(raw []string) *compiledSelectorSet {
	key := strings.Join(raw, "\x00")

	p.selMu.Lock()
	defer p.selMu.Unlock()
	if key == p.selCachedKey && p.selCachedSet != nil {
		return p.selCachedSet
	}

	set := compileSelectors(raw, p.logger)
	p.selCachedKey = key
	p.selCachedSet = set
	return set
}

// familyNames returns the Prometheus series names this OTLP metric produces,
// mirroring isMetricDemanded's name-mode expansion per metric type.
func familyNames(m pmetric.Metric, base string) []string {
	switch m.Type() {
	case pmetric.MetricTypeHistogram:
		return []string{base + "_bucket", base + "_sum", base + "_count"}
	case pmetric.MetricTypeSummary:
		return []string{base, base + "_sum", base + "_count"}
	default: // Gauge, Sum, ExponentialHistogram
		return []string{base}
	}
}

// filterBySelectors prunes md in place at DATAPOINT granularity: a datapoint
// is kept iff its series label view matches ANY demanded selector for its
// metric's name family. Metrics/scopes/resources left empty are dropped.
func (p *demandFilterProcessor) filterBySelectors(md pmetric.Metrics, set *compiledSelectorSet) {
	// dp-attr scratch map, reused across datapoints. Local to this call:
	// ConsumeMetrics may run concurrently, so no processor-level state.
	scratch := make(map[string]string, 8)

	rms := md.ResourceMetrics()
	rms.RemoveIf(func(rm pmetric.ResourceMetrics) bool {
		// Resource-level label view (flattened attrs + job/instance),
		// computed ONCE per ResourceMetrics.
		resLabels := promnaming.ResourceLabelMap(rm.Resource().Attributes())
		sms := rm.ScopeMetrics()
		sms.RemoveIf(func(sm pmetric.ScopeMetrics) bool {
			metrics := sm.Metrics()
			metrics.RemoveIf(func(m pmetric.Metric) bool {
				if p.filterMetricDatapoints(m, resLabels, set, scratch) {
					return false // keep: at least one datapoint survived
				}
				if p.cfg.LogFiltered {
					p.logger.Debug("demand filter: dropping metric",
						zap.String("metric", m.Name()),
					)
				}
				return true // drop
			})
			return metrics.Len() == 0
		})
		return sms.Len() == 0
	})
}

// filterMetricDatapoints removes the metric's non-matching datapoints in
// place and reports whether anything survived. le/quantile matchers were
// dropped at compile time, so histograms/summaries are evaluated (and kept)
// whole per datapoint.
func (p *demandFilterProcessor) filterMetricDatapoints(
	m pmetric.Metric,
	resLabels map[string]string,
	set *compiledSelectorSet,
	scratch map[string]string,
) bool {
	base := promnaming.BaseName(m)
	cands := set.candidatesFor(familyNames(m, base))
	if len(cands) == 0 {
		return false // no selector wants this name: drop the whole metric
	}

	// Fast path: a candidate with no label matchers demands every series of
	// the metric (e.g. `{__name__="up"}` or only le/quantile matchers).
	for _, cs := range cands {
		if len(cs.labelMatchers) == 0 {
			return true
		}
	}

	// matchDP evaluates the candidates against one datapoint's label view:
	// normalized datapoint attributes overriding the resource-level view.
	matchDP := func(attrs pcommon.Map) bool {
		clear(scratch)
		attrs.Range(func(k string, v pcommon.Value) bool {
			scratch[promnaming.NormalizeLabelName(k)] = v.AsString()
			return true
		})
		for _, cs := range cands {
			matched := true
			for _, lm := range cs.labelMatchers {
				v, ok := scratch[lm.Name]
				if !ok {
					v = resLabels[lm.Name] // absent → ""
				}
				if !lm.MatchValue(v) {
					matched = false
					break
				}
			}
			if matched {
				return true // short-circuit on first matching selector
			}
		}
		return false
	}

	switch m.Type() {
	case pmetric.MetricTypeGauge:
		dps := m.Gauge().DataPoints()
		dps.RemoveIf(func(dp pmetric.NumberDataPoint) bool { return !matchDP(dp.Attributes()) })
		return dps.Len() > 0
	case pmetric.MetricTypeSum:
		dps := m.Sum().DataPoints()
		dps.RemoveIf(func(dp pmetric.NumberDataPoint) bool { return !matchDP(dp.Attributes()) })
		return dps.Len() > 0
	case pmetric.MetricTypeHistogram:
		dps := m.Histogram().DataPoints()
		dps.RemoveIf(func(dp pmetric.HistogramDataPoint) bool { return !matchDP(dp.Attributes()) })
		return dps.Len() > 0
	case pmetric.MetricTypeExponentialHistogram:
		dps := m.ExponentialHistogram().DataPoints()
		dps.RemoveIf(func(dp pmetric.ExponentialHistogramDataPoint) bool { return !matchDP(dp.Attributes()) })
		return dps.Len() > 0
	case pmetric.MetricTypeSummary:
		dps := m.Summary().DataPoints()
		dps.RemoveIf(func(dp pmetric.SummaryDataPoint) bool { return !matchDP(dp.Attributes()) })
		return dps.Len() > 0
	}

	return false
}
