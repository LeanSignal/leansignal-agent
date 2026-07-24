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

package leansignaltracedemandfilter

import (
	"context"
	"sort"
	"testing"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/pdata/ptrace"
	"go.uber.org/zap"

	"github.com/leansignal/leansignal-agent/components/tracedemand"
)

// ---------------------------------------------------------------------------
// Mock helpers
// ---------------------------------------------------------------------------

type mockTraceDemandProvider struct{ selectors []string }

func (m *mockTraceDemandProvider) GetTraceDemands() []string { return m.selectors }

// mockExtension satisfies both component.Component and TraceDemandProvider so
// it can live in host.GetExtensions() and be type-asserted to TraceDemandProvider.
type mockExtension struct{ *mockTraceDemandProvider }

func (e *mockExtension) Start(_ context.Context, _ component.Host) error { return nil }
func (e *mockExtension) Shutdown(_ context.Context) error                { return nil }

// mockConsumer captures every ConsumeTraces call.
type mockConsumer struct{ batches []ptrace.Traces }

func (m *mockConsumer) ConsumeTraces(_ context.Context, td ptrace.Traces) error {
	m.batches = append(m.batches, td)
	return nil
}
func (m *mockConsumer) Capabilities() consumer.Capabilities {
	return consumer.Capabilities{MutatesData: false}
}

func (m *mockConsumer) totalSpans() int {
	n := 0
	for _, td := range m.batches {
		n += td.SpanCount()
	}
	return n
}

// receivedServices returns the sorted service.name attrs of forwarded ResourceSpans.
func (m *mockConsumer) receivedServices() []string {
	var out []string
	for _, td := range m.batches {
		rss := td.ResourceSpans()
		for i := 0; i < rss.Len(); i++ {
			if v, ok := rss.At(i).Resource().Attributes().Get("service.name"); ok {
				out = append(out, v.Str())
			} else {
				out = append(out, "<none>")
			}
		}
	}
	sort.Strings(out)
	return out
}

// makeTraces builds one Traces payload with one ResourceSpans per entry; each
// entry is a resource-attribute map and gets `spans` spans.
func makeTraces(spansPer int, resources ...map[string]string) ptrace.Traces {
	td := ptrace.NewTraces()
	for _, attrs := range resources {
		rs := td.ResourceSpans().AppendEmpty()
		for k, v := range attrs {
			rs.Resource().Attributes().PutStr(k, v)
		}
		ss := rs.ScopeSpans().AppendEmpty()
		for i := 0; i < spansPer; i++ {
			ss.Spans().AppendEmpty().SetName("op")
		}
	}
	return td
}

func newProcessor(next consumer.Traces, provider TraceDemandProvider) *traceDemandFilterProcessor {
	p := newTraceDemandFilterProcessor(zap.NewNop(), next, &Config{})
	p.provider = provider
	return p
}

// ---------------------------------------------------------------------------
// Fail-closed behavior
// ---------------------------------------------------------------------------

func TestFailClosedNoProvider(t *testing.T) {
	next := &mockConsumer{}
	p := newTraceDemandFilterProcessor(zap.NewNop(), next, &Config{})

	td := makeTraces(3, map[string]string{"service.name": "checkout"})
	if err := p.ConsumeTraces(context.Background(), td); err != nil {
		t.Fatalf("ConsumeTraces: %v", err)
	}
	if len(next.batches) != 0 {
		t.Fatalf("expected no forwarded batches, got %d", len(next.batches))
	}
}

func TestFailClosedEmptyDemand(t *testing.T) {
	next := &mockConsumer{}
	p := newProcessor(next, &mockTraceDemandProvider{selectors: nil})

	td := makeTraces(3, map[string]string{"service.name": "checkout"})
	if err := p.ConsumeTraces(context.Background(), td); err != nil {
		t.Fatalf("ConsumeTraces: %v", err)
	}
	if len(next.batches) != 0 {
		t.Fatalf("expected no forwarded batches, got %d", len(next.batches))
	}
}

func TestFailClosedAllSelectorsUnparseable(t *testing.T) {
	next := &mockConsumer{}
	p := newProcessor(next, &mockTraceDemandProvider{selectors: []string{
		"not-a-selector",
		"{}",
		`{resource.service.name=`,
	}})

	td := makeTraces(3, map[string]string{"service.name": "checkout"})
	if err := p.ConsumeTraces(context.Background(), td); err != nil {
		t.Fatalf("ConsumeTraces: %v", err)
	}
	if len(next.batches) != 0 {
		t.Fatalf("expected no forwarded batches, got %d", len(next.batches))
	}
}

// ---------------------------------------------------------------------------
// Filtering semantics
// ---------------------------------------------------------------------------

func TestFilterKeepsMatchingResourceSpans(t *testing.T) {
	next := &mockConsumer{}
	p := newProcessor(next, &mockTraceDemandProvider{selectors: []string{
		`{resource.service.name="checkout"}`,
	}})

	td := makeTraces(2,
		map[string]string{"service.name": "checkout"},
		map[string]string{"service.name": "payments"},
	)
	if err := p.ConsumeTraces(context.Background(), td); err != nil {
		t.Fatalf("ConsumeTraces: %v", err)
	}
	if got := next.receivedServices(); len(got) != 1 || got[0] != "checkout" {
		t.Fatalf("expected only checkout, got %v", got)
	}
	if next.totalSpans() != 2 {
		t.Fatalf("expected 2 spans, got %d", next.totalSpans())
	}
}

func TestFilterMatchesAnySelector(t *testing.T) {
	next := &mockConsumer{}
	p := newProcessor(next, &mockTraceDemandProvider{selectors: []string{
		`{resource.service.name="checkout"}`,
		`{resource.service.name="payments"}`,
	}})

	td := makeTraces(1,
		map[string]string{"service.name": "checkout"},
		map[string]string{"service.name": "payments"},
		map[string]string{"service.name": "search"},
	)
	if err := p.ConsumeTraces(context.Background(), td); err != nil {
		t.Fatalf("ConsumeTraces: %v", err)
	}
	want := []string{"checkout", "payments"}
	got := next.receivedServices()
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("expected %v, got %v", want, got)
	}
}

func TestFilterConjunctionAllMatchersMustMatch(t *testing.T) {
	next := &mockConsumer{}
	p := newProcessor(next, &mockTraceDemandProvider{selectors: []string{
		`{resource.deployment.environment="prod",resource.service.name="checkout"}`,
	}})

	td := makeTraces(1,
		map[string]string{"service.name": "checkout", "deployment.environment": "prod"},
		map[string]string{"service.name": "checkout", "deployment.environment": "dev"},
	)
	if err := p.ConsumeTraces(context.Background(), td); err != nil {
		t.Fatalf("ConsumeTraces: %v", err)
	}
	if next.totalSpans() != 1 {
		t.Fatalf("expected 1 span (prod only), got %d", next.totalSpans())
	}
}

func TestFilterRegexSelector(t *testing.T) {
	next := &mockConsumer{}
	p := newProcessor(next, &mockTraceDemandProvider{selectors: []string{
		`{resource.service.name=~"check.*"}`,
	}})

	td := makeTraces(1,
		map[string]string{"service.name": "checkout"},
		map[string]string{"service.name": "recheck"}, // anchored: must NOT match
	)
	if err := p.ConsumeTraces(context.Background(), td); err != nil {
		t.Fatalf("ConsumeTraces: %v", err)
	}
	if got := next.receivedServices(); len(got) != 1 || got[0] != "checkout" {
		t.Fatalf("expected only checkout (anchored regex), got %v", got)
	}
}

func TestFilterNonStringResourceAttribute(t *testing.T) {
	next := &mockConsumer{}
	p := newProcessor(next, &mockTraceDemandProvider{selectors: []string{
		`{resource.host.id="42"}`,
	}})

	td := ptrace.NewTraces()
	rs := td.ResourceSpans().AppendEmpty()
	rs.Resource().Attributes().PutInt("host.id", 42)
	rs.ScopeSpans().AppendEmpty().Spans().AppendEmpty().SetName("op")

	if err := p.ConsumeTraces(context.Background(), td); err != nil {
		t.Fatalf("ConsumeTraces: %v", err)
	}
	if next.totalSpans() != 1 {
		t.Fatalf("expected int attribute to match via AsString, got %d spans", next.totalSpans())
	}
}

func TestFilterBadSelectorDoesNotPoisonRest(t *testing.T) {
	next := &mockConsumer{}
	p := newProcessor(next, &mockTraceDemandProvider{selectors: []string{
		"garbage{{{",
		`{resource.service.name="checkout"}`,
	}})

	td := makeTraces(1, map[string]string{"service.name": "checkout"})
	if err := p.ConsumeTraces(context.Background(), td); err != nil {
		t.Fatalf("ConsumeTraces: %v", err)
	}
	if next.totalSpans() != 1 {
		t.Fatalf("expected the good selector to still apply, got %d spans", next.totalSpans())
	}
}

func TestFilterDemandChangeAppliesNextBatch(t *testing.T) {
	next := &mockConsumer{}
	provider := &mockTraceDemandProvider{selectors: []string{
		`{resource.service.name="checkout"}`,
	}}
	p := newProcessor(next, provider)

	td := makeTraces(1, map[string]string{"service.name": "payments"})
	if err := p.ConsumeTraces(context.Background(), td); err != nil {
		t.Fatalf("ConsumeTraces: %v", err)
	}
	if next.totalSpans() != 0 {
		t.Fatalf("payments not demanded yet, got %d spans", next.totalSpans())
	}

	provider.selectors = []string{`{resource.service.name="payments"}`}
	td = makeTraces(1, map[string]string{"service.name": "payments"})
	if err := p.ConsumeTraces(context.Background(), td); err != nil {
		t.Fatalf("ConsumeTraces: %v", err)
	}
	if next.totalSpans() != 1 {
		t.Fatalf("expected new demand to apply on next batch, got %d spans", next.totalSpans())
	}
}

func TestFilterAllDroppedStillForwardsEmptyBatch(t *testing.T) {
	next := &mockConsumer{}
	p := newProcessor(next, &mockTraceDemandProvider{selectors: []string{
		`{resource.service.name="checkout"}`,
	}})

	td := makeTraces(2, map[string]string{"service.name": "payments"})
	if err := p.ConsumeTraces(context.Background(), td); err != nil {
		t.Fatalf("ConsumeTraces: %v", err)
	}
	// Once a demand exists the batch is forwarded (possibly empty) — parity
	// with the log filter's behavior.
	if len(next.batches) != 1 {
		t.Fatalf("expected 1 forwarded (empty) batch, got %d", len(next.batches))
	}
	if next.totalSpans() != 0 {
		t.Fatalf("expected 0 spans, got %d", next.totalSpans())
	}
}

func TestSelectorCacheReuseAndInvalidation(t *testing.T) {
	p := newTraceDemandFilterProcessor(zap.NewNop(), &mockConsumer{}, &Config{})

	first := p.parsedSelectors([]string{`{resource.service.name="a"}`})
	second := p.parsedSelectors([]string{`{resource.service.name="a"}`})
	if len(first) != 1 || len(second) != 1 || first[0] != second[0] {
		t.Fatal("expected cached parse to be reused for identical demand list")
	}

	third := p.parsedSelectors([]string{`{resource.service.name="b"}`})
	if len(third) != 1 || third[0] == first[0] {
		t.Fatal("expected changed demand list to re-parse")
	}
}

func TestCapabilitiesMutatesData(t *testing.T) {
	p := newTraceDemandFilterProcessor(zap.NewNop(), &mockConsumer{}, &Config{})
	if !p.Capabilities().MutatesData {
		t.Fatal("filter must declare MutatesData")
	}
}

// ---------------------------------------------------------------------------
// Start / extension discovery
// ---------------------------------------------------------------------------

type mockHost struct {
	exts map[component.ID]component.Component
}

func (h *mockHost) GetExtensions() map[component.ID]component.Component { return h.exts }

type unrelatedExtension struct{}

func (unrelatedExtension) Start(_ context.Context, _ component.Host) error { return nil }
func (unrelatedExtension) Shutdown(_ context.Context) error                { return nil }

func TestStartFindsProviderAmongOtherExtensions(t *testing.T) {
	provider := &mockTraceDemandProvider{selectors: []string{`{resource.service.name="a"}`}}
	host := &mockHost{exts: map[component.ID]component.Component{
		component.MustNewID("other"):      unrelatedExtension{},
		component.MustNewID("controller"): &mockExtension{provider},
	}}

	p := newTraceDemandFilterProcessor(zap.NewNop(), &mockConsumer{}, &Config{})
	if err := p.Start(context.Background(), host); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if p.provider == nil {
		t.Fatal("expected provider to be resolved from extensions")
	}
}

// --- per-rule routing -------------------------------------------------------

type routeProvider struct {
	selectors []string
	routes    []tracedemand.Route
}

func (r *routeProvider) GetTraceDemands() []string           { return r.selectors }
func (r *routeProvider) GetTraceRoutes() []tracedemand.Route { return r.routes }

// A resource demanded by TWO rules must be emitted twice — once stamped for each
// rule's Tempo org. Without the duplicate, deleting one rule (and expiring its
// org) would take the other rule's spans with it.
func TestConsumeTraces_EmitsOneCopyPerMatchingRule(t *testing.T) {
	sink := &mockConsumer{}
	p := newTraceDemandFilterProcessor(zap.NewNop(), sink, &Config{})
	p.provider = &routeProvider{}
	p.routes = &routeProvider{routes: []tracedemand.Route{
		{FilterID: "rule-a", Selector: `{resource.service.name="checkout"}`},
		{FilterID: "rule-b", Selector: `{resource.service.name=~"check.*"}`},
		{FilterID: "rule-c", Selector: `{resource.service.name="cart"}`},
	}}

	td := ptrace.NewTraces()
	rs := td.ResourceSpans().AppendEmpty()
	rs.Resource().Attributes().PutStr("service.name", "checkout")
	rs.ScopeSpans().AppendEmpty().Spans().AppendEmpty().SetName("s1")

	if err := p.ConsumeTraces(context.Background(), td); err != nil {
		t.Fatal(err)
	}

	if len(sink.batches) != 1 {
		t.Fatalf("forwarded %d batches, want 1", len(sink.batches))
	}

	got := sink.batches[0]
	if got.ResourceSpans().Len() != 2 {
		t.Fatalf("emitted %d resource-spans, want one per matching rule (2)", got.ResourceSpans().Len())
	}

	stamped := map[string]bool{}
	for i := 0; i < got.ResourceSpans().Len(); i++ {
		v, ok := got.ResourceSpans().At(i).Resource().Attributes().Get(FilterIDAttr)
		if !ok {
			t.Fatal("emitted resource spans carry no filter id")
		}

		stamped[v.Str()] = true
	}

	if !stamped["rule-a"] || !stamped["rule-b"] {
		t.Errorf("stamped ids = %v, want rule-a and rule-b", stamped)
	}

	if stamped["rule-c"] {
		t.Error("rule-c does not match this resource and must not be stamped")
	}
}

// Fail-closed still holds on the routed path: a resource no rule demands ships
// nowhere.
func TestConsumeTraces_RoutedDropsUndemandedResource(t *testing.T) {
	sink := &mockConsumer{}
	p := newTraceDemandFilterProcessor(zap.NewNop(), sink, &Config{})
	p.provider = &routeProvider{}
	p.routes = &routeProvider{routes: []tracedemand.Route{
		{FilterID: "rule-a", Selector: `{resource.service.name="checkout"}`},
	}}

	td := ptrace.NewTraces()
	rs := td.ResourceSpans().AppendEmpty()
	rs.Resource().Attributes().PutStr("service.name", "unwanted")
	rs.ScopeSpans().AppendEmpty().Spans().AppendEmpty().SetName("s1")

	if err := p.ConsumeTraces(context.Background(), td); err != nil {
		t.Fatal(err)
	}

	if len(sink.batches) != 0 {
		t.Errorf("undemanded resource was forwarded (%d batches)", len(sink.batches))
	}
}
