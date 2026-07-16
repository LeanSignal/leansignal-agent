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

// leansignaltracedemandfilter/processor.go
package leansignaltracedemandfilter

import (
	"context"
	"strings"
	"sync"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/pdata/ptrace"
	"go.uber.org/zap"
)

// TraceDemandProvider is the interface the filter expects the
// leansignal_edge_controller extension to satisfy.  It is defined here so that
// the filter owns its own abstraction and has no module-level dependency on
// the edge-controller package.
type TraceDemandProvider interface {
	GetTraceDemands() []string // normalized trace resource selectors
}

// traceDemandFilterProcessor filters OTLP traces by the current trace-demand
// list (resource selectors) held in the leansignal_edge_controller extension.
// A ResourceSpans group is forwarded iff its resource attributes match ANY
// demanded selector; everything else is dropped.  Demand is deliberately
// resource-granular (whole services, never individual spans): all spans of a
// demanded resource ship, so a single-service trace is always complete, and a
// cross-service trace is complete iff every participating service is demanded.
//
// The extension is discovered at Start() time via component.Host.GetExtensions().
// FAIL-CLOSED: if no TraceDemandProvider extension is found, or the selector
// list is empty (or entirely unparseable), ALL traces are blocked — the tenant
// Tempo only ever receives what is explicitly demanded.
type traceDemandFilterProcessor struct {
	logger   *zap.Logger
	next     consumer.Traces
	cfg      *Config
	provider TraceDemandProvider // resolved in Start() from the registered extensions

	// Parsed-selector cache: selectors are parsed once per demand-list change
	// (keyed by the joined raw list) and reused for every subsequent batch.
	mu           sync.Mutex
	cachedKey    string
	cachedParsed []*selector
}

func newTraceDemandFilterProcessor(
	logger *zap.Logger,
	next consumer.Traces,
	cfg *Config,
) *traceDemandFilterProcessor {
	return &traceDemandFilterProcessor{
		logger: logger,
		next:   next,
		cfg:    cfg,
	}
}

// Start resolves the TraceDemandProvider by iterating the registered extensions
// and finding the first one that satisfies the TraceDemandProvider interface
// (i.e. the leansignal_edge_controller extension).
func (p *traceDemandFilterProcessor) Start(_ context.Context, host component.Host) error {
	for _, ext := range host.GetExtensions() {
		if dp, ok := ext.(TraceDemandProvider); ok {
			p.provider = dp
			p.logger.Info("trace demand filter: connected to TraceDemandProvider extension")
			return nil
		}
	}
	p.logger.Warn("trace demand filter: no TraceDemandProvider extension found; all traces will be blocked")
	return nil
}

// Shutdown is part of component.Component.
func (p *traceDemandFilterProcessor) Shutdown(_ context.Context) error {
	return nil
}

// Capabilities declares that this processor mutates data (it removes spans).
func (p *traceDemandFilterProcessor) Capabilities() consumer.Capabilities {
	return consumer.Capabilities{MutatesData: true}
}

// ConsumeTraces filters the incoming batch by the current trace-demand selectors.
//
// The selector list is read fresh on every call directly from the edge
// controller's demand cache — there is no separate propagation mechanism.  The
// moment the edge controller receives a demand_set command and updates the
// cache, the very next batch processed here uses the new list.
func (p *traceDemandFilterProcessor) ConsumeTraces(ctx context.Context, td ptrace.Traces) error {
	totalSpans := td.SpanCount()

	// FAIL-CLOSED: no provider → drop everything.
	if p.provider == nil {
		p.logger.Info("trace demand filter: no TraceDemandProvider extension, blocking all traces",
			zap.Int("received", totalSpans),
			zap.Int("allowed", 0),
		)
		return nil
	}

	// FAIL-CLOSED: no demand received yet (or demand is empty) → drop everything.
	raw := p.provider.GetTraceDemands()
	if len(raw) == 0 {
		p.logger.Info("trace demand filter: no trace demand yet, blocking all traces",
			zap.Int("received", totalSpans),
			zap.Int("allowed", 0),
		)
		return nil
	}

	// FAIL-CLOSED: every selector failed to parse → drop everything.
	selectors := p.parsedSelectors(raw)
	if len(selectors) == 0 {
		p.logger.Warn("trace demand filter: no parseable selectors, blocking all traces",
			zap.Int("received", totalSpans),
			zap.Int("allowed", 0),
			zap.Int("demand_size", len(raw)),
		)
		return nil
	}

	// Keep a ResourceSpans group iff its resource labels match ANY demanded
	// selector.  RemoveIf prunes in place at ResourceSpans granularity — demand
	// is resource-granular by design, never per span.
	td.ResourceSpans().RemoveIf(func(rs ptrace.ResourceSpans) bool {
		labels := resourceLabels(rs.Resource().Attributes())
		for _, sel := range selectors {
			if sel.matches(labels) {
				return false // keep
			}
		}
		if p.cfg.LogFiltered {
			p.logger.Debug("trace demand filter: dropping resource spans",
				zap.Any("resource_labels", labels),
			)
		}
		return true // drop
	})

	allowedSpans := td.SpanCount()
	p.logger.Info("trace demand filter: batch filtered",
		zap.Int("received", totalSpans),
		zap.Int("allowed", allowedSpans),
		zap.Int("dropped", totalSpans-allowedSpans),
		zap.Int("demand_size", len(raw)),
	)

	return p.next.ConsumeTraces(ctx, td)
}

// parsedSelectors returns the parsed selectors for the given raw demand list,
// re-parsing only when the list changed since the previous batch (cache keyed
// by the joined raw list).  Selectors that fail to parse are skipped with a
// warning so one bad selector cannot poison the rest of the demand set.
func (p *traceDemandFilterProcessor) parsedSelectors(raw []string) []*selector {
	key := strings.Join(raw, "\x00")

	p.mu.Lock()
	defer p.mu.Unlock()
	if key == p.cachedKey {
		return p.cachedParsed
	}

	parsed := make([]*selector, 0, len(raw))
	for _, s := range raw {
		sel, err := parseSelector(s)
		if err != nil {
			p.logger.Warn("trace demand filter: skipping unparseable selector",
				zap.String("selector", s),
				zap.Error(err),
			)
			continue
		}
		parsed = append(parsed, sel)
	}
	p.cachedKey = key
	p.cachedParsed = parsed
	return parsed
}
