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

// leansignaledgecontroller/demand_matcher.go
package leansignaledgecontroller

import (
	"sort"
	"strings"

	selectormatch "github.com/leansignal/leansignal-agent/components/selectormatch"
)

// expandDemandNames expands a demand list (metric names extracted from PromQL)
// into the set of Prometheus series names the demand filter would forward.
//
// It mirrors leansignaldemandfilter.isMetricDemanded at name level: the filter
// keeps a whole histogram/summary family when any of its component series is
// demanded, so all sibling series of a demanded component count as stored.
// Known-cache names are already final Prometheus series names (the tracker
// emits separate _bucket/_sum/_count entries and applies the _total suffix),
// so plain set membership against this expansion matches the filter's verdict.
//
// Rules per demanded name d:
//   - d itself is always included (gauges, counters already _total-suffixed,
//     non-monotonic sums, exponential histograms, exact component names).
//   - d ending in _bucket/_sum/_count: the whole family is kept, so the base
//     and all three component names are included (base covers the summary
//     quantile series when d is a _sum/_count of a summary).
//   - bare d: a summary demanded by base also stores d_sum and d_count, so
//     both are included. d_bucket is NOT added — classic histograms are never
//     matched by base name in the filter.
func expandDemandNames(demands []string) map[string]struct{} {
	set := make(map[string]struct{}, len(demands)*3)
	for _, d := range demands {
		if d == "" {
			continue
		}
		set[d] = struct{}{}

		base := d
		suffixed := false
		for _, suffix := range []string{"_bucket", "_sum", "_count"} {
			if strings.HasSuffix(d, suffix) && len(d) > len(suffix) {
				base = strings.TrimSuffix(d, suffix)
				suffixed = true
				break
			}
		}

		if suffixed {
			set[base] = struct{}{}
			set[base+"_bucket"] = struct{}{}
		}
		set[base+"_sum"] = struct{}{}
		set[base+"_count"] = struct{}{}
	}
	return set
}

// diagnoseDemand partitions the demand list into names that match at least one
// known series (matched) and names that match none (missing / "not found").
// A demanded name matches when its family expansion (expandDemandNames) shares
// any name with knownNames — the same semantics the demand filter uses. Both
// returned slices are sorted.
func diagnoseDemand(demands []string, knownNames map[string]struct{}) (matched, missing []string) {
	for _, d := range demands {
		if d == "" {
			continue
		}
		found := false
		for name := range expandDemandNames([]string{d}) {
			if _, ok := knownNames[name]; ok {
				found = true
				break
			}
		}
		if found {
			matched = append(matched, d)
		} else {
			missing = append(missing, d)
		}
	}
	sort.Strings(matched)
	sort.Strings(missing)
	return matched, missing
}

// ---------------------------------------------------------------------------
// Selector-granular demand (DemandSet.metric_selectors) — NAME-LEVEL matching.
//
// TODO(metric-selectors): this matching is name-level only. Full
// selector-awareness (evaluating label matchers per known series) needs the
// per-series labels, but KnownTimeseriesCache deliberately does NOT retain
// them — only the metric name plus the sample ring buffer (labels exist
// transiently in DiscoveredTimeseriesCache until synced). Until labels are
// retained in the known cache, Ping.demanded_known_cache_size and the
// diagnosis may OVERCOUNT for selectors whose label matchers reduce the
// series set (never undercount: the __name__ constraint is applied exactly).
// ---------------------------------------------------------------------------

// nameSelector is the __name__-only view of one demanded metric selector.
type nameSelector struct {
	raw          string
	parsed       bool // false → unparseable, demands nothing
	nameMatchers []*selectormatch.Matcher
}

// parseNameSelectors extracts the __name__ matchers of each selector.
// Unparseable selectors are kept (parsed=false) so diagnosis can report them
// as missing rather than silently dropping them.
func parseNameSelectors(selectors []string) []nameSelector {
	out := make([]nameSelector, 0, len(selectors))
	for _, raw := range selectors {
		ns := nameSelector{raw: raw}
		if sel, err := selectormatch.Parse(raw); err == nil {
			ns.parsed = true
			for _, m := range sel.Matchers {
				if m.Name == "__name__" {
					ns.nameMatchers = append(ns.nameMatchers, m)
				}
			}
		}
		out = append(out, ns)
	}
	return out
}

// matchesSeriesName reports whether a stored series named n would be forwarded
// (at name level) under this selector. It mirrors the demand filter's family
// expansion inverted to the stored-name side: a selector matching any
// component of n's histogram/summary family keeps the whole family, so n is
// demanded when the selector's __name__ constraint accepts any family variant
// of n (see familyNameVariants). A selector without a __name__ matcher
// constrains labels only and accepts every name.
func (ns nameSelector) matchesSeriesName(n string) bool {
	if !ns.parsed {
		return false
	}
	for _, v := range familyNameVariants(n) {
		all := true
		for _, m := range ns.nameMatchers {
			if !m.MatchValue(v) {
				all = false
				break
			}
		}
		if all {
			return true
		}
	}
	return false
}

// familyNameVariants returns the demand names whose (name-level) match would
// cause series name n to be stored — the inverse of expandDemandNames: for
// every variant v here, n ∈ expandDemandNames([v]).
func familyNameVariants(n string) []string {
	base := n
	suffix := ""
	for _, s := range []string{"_bucket", "_sum", "_count"} {
		if strings.HasSuffix(n, s) && len(n) > len(s) {
			base = strings.TrimSuffix(n, s)
			suffix = s
			break
		}
	}
	variants := []string{n, base + "_bucket", base + "_sum", base + "_count"}
	if suffix != "_bucket" && base != n {
		// A bare-name demand stores base/_sum/_count (summary) but never
		// _bucket — classic histograms are not matched by base name.
		variants = append(variants, base)
	}
	return variants
}

// selectorDemandedNames returns the subset of knownNames demanded (at name
// level) by any of the selectors — the selector analog of expandDemandNames,
// intersected with the known-name universe so regex name matchers can be
// evaluated.
func selectorDemandedNames(selectors []string, knownNames map[string]struct{}) map[string]struct{} {
	parsed := parseNameSelectors(selectors)
	out := make(map[string]struct{}, len(knownNames))
	for n := range knownNames {
		for _, ns := range parsed {
			if ns.matchesSeriesName(n) {
				out[n] = struct{}{}
				break
			}
		}
	}
	return out
}

// diagnoseDemandSelectors partitions the selector list into selectors that
// match at least one known series name (matched) and selectors that match
// none — or are unparseable (missing). Both returned slices are sorted.
func diagnoseDemandSelectors(selectors []string, knownNames map[string]struct{}) (matched, missing []string) {
	for _, ns := range parseNameSelectors(selectors) {
		found := false
		for n := range knownNames {
			if ns.matchesSeriesName(n) {
				found = true
				break
			}
		}
		if found {
			matched = append(matched, ns.raw)
		} else {
			missing = append(missing, ns.raw)
		}
	}
	sort.Strings(matched)
	sort.Strings(missing)
	return matched, missing
}
