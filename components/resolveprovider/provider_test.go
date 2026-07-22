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

package resolveprovider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.opentelemetry.io/collector/confmap"
	"go.uber.org/zap"
)

func newTestProvider() confmap.Provider {
	return newProvider(confmap.ProviderSettings{Logger: zap.NewNop()})
}

func retrieve(t *testing.T, p confmap.Provider, key string) string {
	t.Helper()
	r, err := p.Retrieve(context.Background(), schemeName+":"+key, nil)
	if err != nil {
		t.Fatalf("Retrieve(%s): %v", key, err)
	}
	v, err := r.AsRaw()
	if err != nil {
		t.Fatalf("AsRaw(%s): %v", key, err)
	}
	s, ok := v.(string)
	if !ok {
		t.Fatalf("Retrieve(%s) = %T, want string", key, v)
	}
	return s
}

func TestDeriveEndpoints(t *testing.T) {
	got := deriveEndpoints("lean", "eu11.leansignal.io")
	want := map[string]string{
		"region":    "eu11.leansignal.io",
		"api":       "https://lean-api.eu11.leansignal.io",
		"grpc":      "lean-grpc.eu11.leansignal.io:443",
		"dataplane": "https://lean-metrics-ingest.eu11.leansignal.io",
		"loki":      "https://lean-logs-ingest.eu11.leansignal.io",
		"tempo":     "https://lean-traces-ingest.eu11.leansignal.io",
	}
	for k, w := range want {
		if got[k] != w {
			t.Errorf("deriveEndpoints[%s] = %q, want %q", k, got[k], w)
		}
	}
}

func TestResolveViaControlCenter(t *testing.T) {
	var gotTenant, gotAAT string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/resolve_tenant" {
			http.NotFound(w, r)
			return
		}
		gotTenant = r.URL.Query().Get("tenant")
		gotAAT = r.URL.Query().Get("aat")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"api_url":"lean-api.eu11.leansignal.io","tenant":true}`))
	}))
	defer srv.Close()

	t.Setenv(envTenant, "lean")
	t.Setenv(envCCURL, srv.URL)
	t.Setenv(envAAT, "test-token")

	p := newTestProvider()
	if got := retrieve(t, p, "tempo"); got != "https://lean-traces-ingest.eu11.leansignal.io" {
		t.Errorf("tempo = %q", got)
	}
	// Second lookup must reuse the memoized resolve (server hit at most once).
	if got := retrieve(t, p, "grpc"); got != "lean-grpc.eu11.leansignal.io:443" {
		t.Errorf("grpc = %q", got)
	}
	if gotTenant != "lean" {
		t.Errorf("resolve called with tenant=%q, want lean", gotTenant)
	}
	if gotAAT != "test-token" {
		t.Errorf("resolve called with aat=%q, want test-token", gotAAT)
	}
}

func TestDomainOverrideSkipsResolve(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("resolve endpoint must not be called when LEANSIGNAL_DOMAIN is set")
	}))
	defer srv.Close()

	t.Setenv(envTenant, "acme")
	t.Setenv(envCCURL, srv.URL)
	t.Setenv(envDomain, "us22.leansignal.io")

	p := newTestProvider()
	if got := retrieve(t, p, "loki"); got != "https://acme-logs-ingest.us22.leansignal.io" {
		t.Errorf("loki = %q", got)
	}
}

func TestMissingTenantIsError(t *testing.T) {
	t.Setenv(envTenant, "")
	p := newTestProvider()
	if _, err := p.Retrieve(context.Background(), schemeName+":tempo", nil); err == nil {
		t.Fatal("expected error when LEANSIGNAL_TENANT is unset")
	}
}

func TestUnknownKeyIsError(t *testing.T) {
	t.Setenv(envTenant, "acme")
	t.Setenv(envDomain, "eu11.leansignal.io")
	p := newTestProvider()
	if _, err := p.Retrieve(context.Background(), schemeName+":bogus", nil); err == nil {
		t.Fatal("expected error for unknown leansignal key")
	}
}

func TestUnknownTenantIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"api_url":"","tenant":false}`))
	}))
	defer srv.Close()

	t.Setenv(envTenant, "ghost")
	t.Setenv(envCCURL, srv.URL)
	p := newTestProvider()
	if _, err := p.Retrieve(context.Background(), schemeName+":tempo", nil); err == nil {
		t.Fatal("expected error for non-existent tenant")
	}
}

func TestExplicitOverrideWinsWithoutResolve(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("resolve endpoint must not be called when the key is explicitly overridden")
	}))
	defer srv.Close()

	// Only tempo is pinned; no tenant/domain set. The pinned key must resolve
	// from env without any control-center call.
	t.Setenv(envTenant, "")
	t.Setenv(envCCURL, srv.URL)
	t.Setenv("LEANSIGNAL_TEMPO_ENDPOINT", "https://custom-traces.example.internal")

	p := newTestProvider()
	if got := retrieve(t, p, "tempo"); got != "https://custom-traces.example.internal" {
		t.Errorf("tempo override = %q", got)
	}
}

func TestPartialOverrideStillDerivesRest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"api_url":"lean-api.eu11.leansignal.io","tenant":true}`))
	}))
	defer srv.Close()

	t.Setenv(envTenant, "lean")
	t.Setenv(envCCURL, srv.URL)
	t.Setenv("LEANSIGNAL_ENDPOINT", "lean-api-grpc.tenant-0001.svc:9090") // pin gRPC in-cluster

	p := newTestProvider()
	if got := retrieve(t, p, "grpc"); got != "lean-api-grpc.tenant-0001.svc:9090" {
		t.Errorf("grpc (pinned) = %q", got)
	}
	if got := retrieve(t, p, "tempo"); got != "https://lean-traces-ingest.eu11.leansignal.io" {
		t.Errorf("tempo (derived) = %q", got)
	}
}

func TestWrongSchemeIsError(t *testing.T) {
	p := newTestProvider()
	if _, err := p.Retrieve(context.Background(), "env:FOO", nil); err == nil {
		t.Fatal("expected error for non-leansignal scheme")
	}
}
