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

	selectormatch "github.com/leansignal/leansignal-agent/components/selectormatch"
	"github.com/leansignal/leansignal-agent/components/tracedemand"
)

// TraceDemandProvider is the interface the filter expects the
// leansignal_edge_controller extension to satisfy.  It is defined here so that
// the filter owns its own abstraction and has no module-level dependency on
// the edge-controller package.
type TraceDemandProvider interface {
	GetTraceDemands() []string // normalized trace resource selectors
}

// TraceRouteProvider is the OPTIONAL per-rule extension of TraceDemandProvider.
// When the edge controller offers it (i.e. the server sent trace_demands), the
// filter stops asking "is this resource demanded?" and asks "WHICH rules demand
// it?", stamping each matching rule's id so the exporter can push those spans to
// that rule's own Tempo org.
//
// Why per rule: Tempo cannot delete a subset of an org, so deleting one trace
// ingestion rule can only purge its spans if they live in their own org. A
// resource matched by several rules is therefore emitted once per rule —
// duplicated on purpose, which is the price of per-rule deletion.
type TraceRouteProvider interface {
	GetTraceRoutes() []tracedemand.Route
}

// FilterIDAttr marks each emitted ResourceSpans with the rule that demanded it.
// The exporter reads it to choose the push path (and thus the org) and strips it
// before sending — it is agent-internal routing, never tenant data.
const FilterIDAttr = "leansignal.trace.filter_id"

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
	routes   TraceRouteProvider  // same extension, when it supports per-rule routing

	// Parsed-selector cache: selectors are parsed once per demand-list change
	// (keyed by the joined raw list) and reused for every subsequent batch.
	mu           sync.Mutex
	cachedKey    string
	cachedParsed []*selectormatch.Selector

	cachedRouteKey string
	cachedRoutes   []parsedRoute
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
			if rp, ok := ext.(TraceRouteProvider); ok {
				p.routes = rp
			}
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

	// Per-rule routing when the server supplies it; otherwise fall through to the
	// legacy keep/drop path below (older server → tenant-wide org, as before).
	if p.routes != nil {
		if routes := p.routes.GetTraceRoutes(); len(routes) > 0 {
			return p.consumeRouted(ctx, td, routes, totalSpans)
		}
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
			if sel.Matches(labels) {
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
func (p *traceDemandFilterProcessor) parsedSelectors(raw []string) []*selectormatch.Selector {
	key := strings.Join(raw, "\x00")

	p.mu.Lock()
	defer p.mu.Unlock()
	if key == p.cachedKey {
		return p.cachedParsed
	}

	parsed := make([]*selectormatch.Selector, 0, len(raw))
	for _, s := range raw {
		// ParseDotted: trace selector label names are dotted TraceQL
		// resource-scoped attribute keys (e.g. resource.service.name).
		sel, err := selectormatch.ParseDotted(s)
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

// consumeRouted emits one copy of each demanded ResourceSpans PER matching rule,
// stamped with that rule's filter id. A resource matched by three rules ships
// three times — deliberate duplication: each rule's org must hold its own copy
// or deleting one rule would take the others' spans with it.
//
// Fail-closed is preserved: a resource matching no route is dropped, and if
// every route fails to parse nothing ships.
func (p *traceDemandFilterProcessor) consumeRouted(
	ctx context.Context,
	td ptrace.Traces,
	routes []tracedemand.Route,
	totalSpans int,
) error {
	parsed := p.parsedRoutes(routes)
	if len(parsed) == 0 {
		p.logger.Warn("trace demand filter: no parseable route selectors, blocking all traces",
			zap.Int("received", totalSpans),
			zap.Int("routes", len(routes)),
		)

		return nil
	}

	out := ptrace.NewTraces()
	src := td.ResourceSpans()

	for i := 0; i < src.Len(); i++ {
		rs := src.At(i)
		labels := resourceLabels(rs.Resource().Attributes())

		for _, pr := range parsed {
			if !pr.selector.Matches(labels) {
				continue
			}

			dst := out.ResourceSpans().AppendEmpty()
			rs.CopyTo(dst)
			dst.Resource().Attributes().PutStr(FilterIDAttr, pr.filterID)
		}
	}

	emitted := out.SpanCount()
	p.logger.Info("trace demand filter: batch routed",
		zap.Int("received", totalSpans),
		zap.Int("emitted", emitted),
		zap.Int("routes", len(parsed)),
	)

	if emitted == 0 {
		return nil
	}

	return p.next.ConsumeTraces(ctx, out)
}

// parsedRoute is a compiled route: the selector plus the rule it belongs to.
type parsedRoute struct {
	selector *selectormatch.Selector
	filterID string
}

// parsedRoutes compiles the routing table, re-parsing only when it changed.
func (p *traceDemandFilterProcessor) parsedRoutes(routes []tracedemand.Route) []parsedRoute {
	key := make([]string, 0, len(routes))
	for _, r := range routes {
		key = append(key, r.FilterID+"="+r.Selector)
	}

	joined := strings.Join(key, "\x00")

	p.mu.Lock()
	defer p.mu.Unlock()

	if joined == p.cachedRouteKey {
		return p.cachedRoutes
	}

	out := make([]parsedRoute, 0, len(routes))

	for _, r := range routes {
		// ParseDotted, like the legacy path: trace selector label names are
		// dotted TraceQL resource-scoped keys (resource.service.name).
		sel, err := selectormatch.ParseDotted(r.Selector)
		if err != nil {
			p.logger.Warn("trace demand filter: skipping unparseable route selector",
				zap.String("selector", r.Selector),
				zap.String("filter_id", r.FilterID),
				zap.Error(err),
			)

			continue
		}

		out = append(out, parsedRoute{selector: sel, filterID: r.FilterID})
	}

	p.cachedRouteKey = joined
	p.cachedRoutes = out

	return out
}
