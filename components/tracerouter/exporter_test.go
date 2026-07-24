// Copyright 2026 LeanSignal
//
// SPDX-License-Identifier: Apache-2.0

package leansignaltracerouter

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sort"
	"testing"
	"time"

	"go.opentelemetry.io/collector/pdata/ptrace"
	"go.opentelemetry.io/collector/pdata/ptrace/ptraceotlp"
	"go.uber.org/zap"

	tracedemandfilter "github.com/leansignal/leansignal-agent/components/tracedemandfilter"
)

func stamped(service, filterID string) ptrace.ResourceSpans {
	td := ptrace.NewTraces()
	rs := td.ResourceSpans().AppendEmpty()
	rs.Resource().Attributes().PutStr("service.name", service)

	if filterID != "" {
		rs.Resource().Attributes().PutStr(tracedemandfilter.FilterIDAttr, filterID)
	}

	rs.ScopeSpans().AppendEmpty().Spans().AppendEmpty().SetName("s")

	return rs
}

// Each rule's spans must go to that rule's OWN path — that path is what the
// ingress turns into a per-rule Tempo org, and the org is the only granularity
// at which Tempo can later delete them.
func TestPushTraces_OneRequestPerRule(t *testing.T) {
	type got struct {
		path  string
		spans int
		attrs bool
	}

	var seen []got

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body := make([]byte, req.ContentLength)
		_, _ = req.Body.Read(body)

		r := ptraceotlp.NewExportRequest()
		_ = r.UnmarshalProto(body)

		hasStamp := false
		rss := r.Traces().ResourceSpans()

		for i := 0; i < rss.Len(); i++ {
			if _, ok := rss.At(i).Resource().Attributes().Get(tracedemandfilter.FilterIDAttr); ok {
				hasStamp = true
			}
		}

		seen = append(seen, got{path: req.URL.Path, spans: r.Traces().SpanCount(), attrs: hasStamp})

		if req.Header.Get("Authorization") != "Bearer k" {
			t.Errorf("missing auth header: %v", req.Header)
		}

		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	r := newRouter(zap.NewNop(), &Config{
		Endpoint: srv.URL,
		Headers:  map[string]string{"Authorization": "Bearer k"},
		Timeout:  5 * time.Second,
	})
	_ = r.start(context.Background(), nil)

	td := ptrace.NewTraces()
	stamped("checkout", "rule-a").CopyTo(td.ResourceSpans().AppendEmpty())
	stamped("cart", "rule-b").CopyTo(td.ResourceSpans().AppendEmpty())
	stamped("checkout", "rule-a").CopyTo(td.ResourceSpans().AppendEmpty())

	if err := r.pushTraces(context.Background(), td); err != nil {
		t.Fatal(err)
	}

	if len(seen) != 2 {
		t.Fatalf("made %d requests, want one per rule (2): %+v", len(seen), seen)
	}

	paths := []string{seen[0].path, seen[1].path}
	sort.Strings(paths)

	if paths[0] != "/v1/traces/r/rule-a" || paths[1] != "/v1/traces/r/rule-b" {
		t.Errorf("paths = %v, want per-rule paths", paths)
	}

	for _, s := range seen {
		if s.attrs {
			t.Error("the routing stamp must be stripped before sending — it is not tenant data")
		}
	}
}

// Spans with no stamp (a server predating per-rule routing) keep going to the
// tenant-wide path, so an agent upgrade alone never changes behaviour.
func TestPushTraces_UnstampedGoesToTenantPath(t *testing.T) {
	var paths []string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		paths = append(paths, req.URL.Path)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	r := newRouter(zap.NewNop(), &Config{Endpoint: srv.URL, Timeout: 5 * time.Second})
	_ = r.start(context.Background(), nil)

	td := ptrace.NewTraces()
	stamped("legacy", "").CopyTo(td.ResourceSpans().AppendEmpty())

	if err := r.pushTraces(context.Background(), td); err != nil {
		t.Fatal(err)
	}

	if len(paths) != 1 || paths[0] != "/v1/traces" {
		t.Errorf("paths = %v, want [/v1/traces]", paths)
	}
}

// A rejected push must surface as an error so exporterhelper retries it —
// silently dropping spans would lose data the tenant is paying to collect.
func TestPushTraces_ErrorOnNon2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope", http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	r := newRouter(zap.NewNop(), &Config{Endpoint: srv.URL, Timeout: 5 * time.Second})
	_ = r.start(context.Background(), nil)

	td := ptrace.NewTraces()
	stamped("checkout", "rule-a").CopyTo(td.ResourceSpans().AppendEmpty())

	if err := r.pushTraces(context.Background(), td); err == nil {
		t.Fatal("expected a non-2xx push to error")
	}
}
