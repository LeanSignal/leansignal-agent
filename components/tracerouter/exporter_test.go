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

// Each rule's spans go in their own request, tagged with that rule — the tag is
// what the ingress turns into a per-rule Tempo org, and the org is the only
// granularity at which Tempo can later delete them.
func TestPushTraces_OneRequestPerRule(t *testing.T) {
	type got struct {
		path  string
		rule  string
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

		seen = append(seen, got{
			path:  req.URL.Path,
			rule:  req.Header.Get(RuleHeader),
			spans: r.Traces().SpanCount(),
			attrs: hasStamp,
		})

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

	// The PATH is the same for every rule — a per-rule path would need its own
	// Ingress, which control-center does not rename on allocation.
	for _, s := range seen {
		if s.path != "/v1/traces" {
			t.Errorf("path = %q, want the plain /v1/traces", s.path)
		}
	}

	rules := []string{seen[0].rule, seen[1].rule}
	sort.Strings(rules)

	if rules[0] != "rule-a" || rules[1] != "rule-b" {
		t.Errorf("rule headers = %v, want one request per rule", rules)
	}

	for _, s := range seen {
		if s.attrs {
			t.Error("the routing stamp must be stripped before sending — it is not tenant data")
		}
	}
}

// Spans with no stamp (a server predating per-rule routing) carry NO rule
// header, so lean-api answers with the tenant-wide org — an agent upgrade alone
// never changes behaviour.
func TestPushTraces_UnstampedCarriesNoRuleHeader(t *testing.T) {
	var paths, rules []string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		paths = append(paths, req.URL.Path)
		rules = append(rules, req.Header.Get(RuleHeader))
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

	if rules[0] != "" {
		t.Errorf("rule header = %q, want empty for unstamped spans", rules[0])
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
