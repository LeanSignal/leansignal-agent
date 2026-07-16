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

package leansignallogdemandfilter

import (
	"context"
	"sort"
	"testing"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.uber.org/zap"
)

// ---------------------------------------------------------------------------
// Mock helpers
// ---------------------------------------------------------------------------

type mockLogDemandProvider struct{ selectors []string }

func (m *mockLogDemandProvider) GetLogDemands() []string { return m.selectors }

// mockExtension satisfies both component.Component and LogDemandProvider so it
// can live in host.GetExtensions() and be type-asserted to LogDemandProvider.
type mockExtension struct{ *mockLogDemandProvider }

func (e *mockExtension) Start(_ context.Context, _ component.Host) error { return nil }
func (e *mockExtension) Shutdown(_ context.Context) error                { return nil }

// mockConsumer captures every ConsumeLogs call.
type mockConsumer struct{ batches []plog.Logs }

func (m *mockConsumer) ConsumeLogs(_ context.Context, ld plog.Logs) error {
	m.batches = append(m.batches, ld)
	return nil
}
func (m *mockConsumer) Capabilities() consumer.Capabilities {
	return consumer.Capabilities{MutatesData: false}
}

func (m *mockConsumer) totalRecords() int {
	n := 0
	for _, ld := range m.batches {
		n += ld.LogRecordCount()
	}
	return n
}

// receivedServices returns the sorted service.name attrs of forwarded ResourceLogs.
func (m *mockConsumer) receivedServices() []string {
	var out []string
	for _, ld := range m.batches {
		rls := ld.ResourceLogs()
		for i := 0; i < rls.Len(); i++ {
			if v, ok := rls.At(i).Resource().Attributes().Get("service.name"); ok {
				out = append(out, v.Str())
			} else {
				out = append(out, "<none>")
			}
		}
	}
	sort.Strings(out)
	return out
}

// mockHost implements component.Host.
type mockHost struct {
	extensions map[component.ID]component.Component
}

func (h *mockHost) GetExtensions() map[component.ID]component.Component { return h.extensions }

// ---------------------------------------------------------------------------
// Log builders
// ---------------------------------------------------------------------------

// addResourceLogs appends one ResourceLogs with the given resource attributes
// and n log records in a single scope.
func addResourceLogs(ld plog.Logs, attrs map[string]string, n int) {
	rl := ld.ResourceLogs().AppendEmpty()
	for k, v := range attrs {
		rl.Resource().Attributes().PutStr(k, v)
	}
	sl := rl.ScopeLogs().AppendEmpty()
	for i := 0; i < n; i++ {
		sl.LogRecords().AppendEmpty().Body().SetStr("line")
	}
}

// newProcessor builds a started processor wired to a provider (nil for none)
// and returns it with its downstream mock consumer.
func newProcessor(t *testing.T, provider *mockLogDemandProvider, cfg *Config) (*logDemandFilterProcessor, *mockConsumer) {
	t.Helper()
	if cfg == nil {
		cfg = &Config{}
	}
	next := &mockConsumer{}
	p := newLogDemandFilterProcessor(zap.NewNop(), next, cfg)

	exts := map[component.ID]component.Component{}
	if provider != nil {
		exts[component.MustNewID("leansignal_edge_controller")] = &mockExtension{provider}
	}
	if err := p.Start(context.Background(), &mockHost{extensions: exts}); err != nil {
		t.Fatal(err)
	}
	return p, next
}

// ---------------------------------------------------------------------------
// Fail-closed behavior
// ---------------------------------------------------------------------------

func TestFailClosedNoProvider(t *testing.T) {
	p, next := newProcessor(t, nil, nil)

	ld := plog.NewLogs()
	addResourceLogs(ld, map[string]string{"service.name": "api"}, 3)

	if err := p.ConsumeLogs(context.Background(), ld); err != nil {
		t.Fatal(err)
	}
	if len(next.batches) != 0 {
		t.Errorf("no provider: %d batches forwarded, want 0 (fail-closed)", len(next.batches))
	}
}

func TestFailClosedEmptyDemand(t *testing.T) {
	p, next := newProcessor(t, &mockLogDemandProvider{selectors: nil}, nil)

	ld := plog.NewLogs()
	addResourceLogs(ld, map[string]string{"service.name": "api"}, 3)

	if err := p.ConsumeLogs(context.Background(), ld); err != nil {
		t.Fatal(err)
	}
	if len(next.batches) != 0 {
		t.Errorf("empty demand: %d batches forwarded, want 0 (fail-closed)", len(next.batches))
	}
}

func TestFailClosedAllSelectorsUnparseable(t *testing.T) {
	p, next := newProcessor(t, &mockLogDemandProvider{selectors: []string{"not a selector", "{}"}}, nil)

	ld := plog.NewLogs()
	addResourceLogs(ld, map[string]string{"service.name": "api"}, 3)

	if err := p.ConsumeLogs(context.Background(), ld); err != nil {
		t.Fatal(err)
	}
	if len(next.batches) != 0 {
		t.Errorf("unparseable demand: %d batches forwarded, want 0 (fail-closed)", len(next.batches))
	}
}

// ---------------------------------------------------------------------------
// Filtering
// ---------------------------------------------------------------------------

func TestFilterKeepsMatchingResourceLogs(t *testing.T) {
	provider := &mockLogDemandProvider{selectors: []string{`{service_name="api"}`}}
	p, next := newProcessor(t, provider, nil)

	ld := plog.NewLogs()
	addResourceLogs(ld, map[string]string{"service.name": "api"}, 2)
	addResourceLogs(ld, map[string]string{"service.name": "web"}, 5)

	if err := p.ConsumeLogs(context.Background(), ld); err != nil {
		t.Fatal(err)
	}
	if got := next.totalRecords(); got != 2 {
		t.Errorf("records forwarded: got %d want 2", got)
	}
	if got := next.receivedServices(); len(got) != 1 || got[0] != "api" {
		t.Errorf("forwarded services: %v, want [api]", got)
	}
}

func TestFilterMatchesAnySelector(t *testing.T) {
	provider := &mockLogDemandProvider{selectors: []string{
		`{service_name="api"}`,
		`{k8s_namespace_name="prod"}`,
	}}
	p, next := newProcessor(t, provider, nil)

	ld := plog.NewLogs()
	addResourceLogs(ld, map[string]string{"service.name": "api"}, 1)                                    // matches 1st
	addResourceLogs(ld, map[string]string{"service.name": "web", "k8s.namespace.name": "prod"}, 1)      // matches 2nd
	addResourceLogs(ld, map[string]string{"service.name": "batch", "k8s.namespace.name": "staging"}, 1) // neither

	if err := p.ConsumeLogs(context.Background(), ld); err != nil {
		t.Fatal(err)
	}
	if got := next.receivedServices(); len(got) != 2 || got[0] != "api" || got[1] != "web" {
		t.Errorf("forwarded services: %v, want [api web]", got)
	}
}

func TestFilterRemoveIfPrunesAtResourceGranularity(t *testing.T) {
	// A kept ResourceLogs keeps ALL its records; a dropped one loses all.
	provider := &mockLogDemandProvider{selectors: []string{`{service_name=~"api-.+"}`}}
	p, next := newProcessor(t, provider, nil)

	ld := plog.NewLogs()
	addResourceLogs(ld, map[string]string{"service.name": "api-1"}, 4)
	addResourceLogs(ld, map[string]string{"service.name": "api"}, 3) // anchored regex: no match
	addResourceLogs(ld, map[string]string{"service.name": "api-2"}, 1)

	if err := p.ConsumeLogs(context.Background(), ld); err != nil {
		t.Fatal(err)
	}
	if len(next.batches) != 1 {
		t.Fatalf("batches: got %d want 1", len(next.batches))
	}
	out := next.batches[0]
	if out.ResourceLogs().Len() != 2 {
		t.Errorf("resource logs kept: got %d want 2", out.ResourceLogs().Len())
	}
	if got := out.LogRecordCount(); got != 5 {
		t.Errorf("records kept: got %d want 5 (4+1)", got)
	}
}

func TestFilterServiceNameDefaultIsMatchable(t *testing.T) {
	// A resource without service.name gets service_name="unknown_service" —
	// a selector for it must match (label parity with Loki's ingestion).
	provider := &mockLogDemandProvider{selectors: []string{`{service_name="unknown_service"}`}}
	p, next := newProcessor(t, provider, nil)

	ld := plog.NewLogs()
	addResourceLogs(ld, map[string]string{"host.name": "n1"}, 2)     // no service.name
	addResourceLogs(ld, map[string]string{"service.name": "api"}, 3) // has one

	if err := p.ConsumeLogs(context.Background(), ld); err != nil {
		t.Fatal(err)
	}
	if got := next.totalRecords(); got != 2 {
		t.Errorf("records forwarded: got %d want 2 (only the unknown_service stream)", got)
	}
}

func TestFilterBadSelectorDoesNotPoisonRest(t *testing.T) {
	provider := &mockLogDemandProvider{selectors: []string{
		`{invalid`,             // unparseable — must be skipped
		`{service_name="api"}`, // must still apply
	}}
	p, next := newProcessor(t, provider, nil)

	ld := plog.NewLogs()
	addResourceLogs(ld, map[string]string{"service.name": "api"}, 1)
	addResourceLogs(ld, map[string]string{"service.name": "web"}, 1)

	if err := p.ConsumeLogs(context.Background(), ld); err != nil {
		t.Fatal(err)
	}
	if got := next.receivedServices(); len(got) != 1 || got[0] != "api" {
		t.Errorf("forwarded services: %v, want [api]", got)
	}
}

func TestFilterDemandChangeAppliesNextBatch(t *testing.T) {
	provider := &mockLogDemandProvider{selectors: []string{`{service_name="api"}`}}
	p, next := newProcessor(t, provider, nil)

	ld := plog.NewLogs()
	addResourceLogs(ld, map[string]string{"service.name": "web"}, 1)
	if err := p.ConsumeLogs(context.Background(), ld); err != nil {
		t.Fatal(err)
	}
	if got := next.totalRecords(); got != 0 {
		t.Fatalf("before demand change: got %d records, want 0", got)
	}

	// The provider is read fresh each batch: swapping the demand list takes
	// effect immediately (and refreshes the parsed-selector cache).
	provider.selectors = []string{`{service_name="web"}`}
	ld2 := plog.NewLogs()
	addResourceLogs(ld2, map[string]string{"service.name": "web"}, 1)
	if err := p.ConsumeLogs(context.Background(), ld2); err != nil {
		t.Fatal(err)
	}
	if got := next.totalRecords(); got != 1 {
		t.Errorf("after demand change: got %d records, want 1", got)
	}
}

func TestFilterAllDroppedStillForwardsEmptyBatch(t *testing.T) {
	// Mirrors the metrics demand filter: with a non-empty demand the batch is
	// forwarded after pruning, even when nothing survived.
	provider := &mockLogDemandProvider{selectors: []string{`{service_name="api"}`}}
	p, next := newProcessor(t, provider, nil)

	ld := plog.NewLogs()
	addResourceLogs(ld, map[string]string{"service.name": "web"}, 2)

	if err := p.ConsumeLogs(context.Background(), ld); err != nil {
		t.Fatal(err)
	}
	if len(next.batches) != 1 {
		t.Fatalf("batches: got %d want 1", len(next.batches))
	}
	if got := next.batches[0].ResourceLogs().Len(); got != 0 {
		t.Errorf("resource logs: got %d want 0", got)
	}
}

func TestSelectorCacheReuseAndInvalidation(t *testing.T) {
	p, _ := newProcessor(t, &mockLogDemandProvider{}, nil)

	first := p.parsedSelectors([]string{`{a="1"}`, `{b="2"}`})
	if len(first) != 2 {
		t.Fatalf("parsed: got %d want 2", len(first))
	}
	// Same list → identical cached slice (no re-parse).
	second := p.parsedSelectors([]string{`{a="1"}`, `{b="2"}`})
	if &first[0] != &second[0] {
		t.Error("expected cached parsed selectors to be reused for an unchanged list")
	}
	// Changed list → re-parse.
	third := p.parsedSelectors([]string{`{c="3"}`})
	if len(third) != 1 {
		t.Fatalf("parsed after change: got %d want 1", len(third))
	}
}

func TestCapabilitiesMutatesData(t *testing.T) {
	p, _ := newProcessor(t, &mockLogDemandProvider{}, nil)
	if !p.Capabilities().MutatesData {
		t.Error("processor must declare MutatesData (it prunes ResourceLogs)")
	}
	if err := p.Shutdown(context.Background()); err != nil {
		t.Errorf("Shutdown: %v", err)
	}
}

// plainExtension is a component.Component that is NOT a LogDemandProvider.
type plainExtension struct{}

func (plainExtension) Start(_ context.Context, _ component.Host) error { return nil }
func (plainExtension) Shutdown(_ context.Context) error                { return nil }

func TestStartFindsProviderAmongOtherExtensions(t *testing.T) {
	next := &mockConsumer{}
	p := newLogDemandFilterProcessor(zap.NewNop(), next, &Config{})

	exts := map[component.ID]component.Component{
		component.MustNewID("health_check"):               plainExtension{},
		component.MustNewID("leansignal_edge_controller"): &mockExtension{&mockLogDemandProvider{selectors: []string{`{a="1"}`}}},
	}
	if err := p.Start(context.Background(), &mockHost{extensions: exts}); err != nil {
		t.Fatal(err)
	}
	if p.provider == nil {
		t.Fatal("provider not resolved from extensions")
	}
}
