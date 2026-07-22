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

// Package resolveprovider implements a confmap.Provider (scheme "leansignal")
// that resolves a tenant's region from the LeanSignal control-center at startup
// and derives every backend URL the agent needs from the tenant slug + region.
//
// The agent is parametrized only with its tenant slug (LEANSIGNAL_TENANT); the
// control-center URL is stable (default https://cc.leansignal.io). On the first
// ${leansignal:...} lookup the provider calls the SAME public resolve endpoint
// the browser SPA uses at boot — GET /resolve_tenant?tenant=<slug>&aat=<token> —
// which returns {"api_url":"<slug>-api.<region-domain>", "tenant":true}. The
// region domain is recovered from api_url and every other host is derived:
//
//	region     = <region-domain>                                   (e.g. eu11.leansignal.io)
//	api        = https://<slug>-api.<region>
//	grpc       = <slug>-grpc.<region>:443
//	dataplane  = https://<slug>-metrics-ingest.<region>
//	loki       = https://<slug>-logs-ingest.<region>
//	tempo      = https://<slug>-traces-ingest.<region>
//
// The three ingest keys (dataplane/loki/tempo) are BASE origins; the config
// appends the signal-specific path (/api/v1/write, /otlp/v1/logs, /v1/traces),
// exactly as it does for the explicit ${env:...} form.
//
// Explicit overrides: each key honors a matching LEANSIGNAL_*_ENDPOINT env var
// (see overrideEnvByKey). When set, that value is returned verbatim and no
// control-center lookup happens for it — so an operator can pin any single
// endpoint (e.g. an in-cluster gRPC host) while the rest are still derived, and a
// fully-pinned agent never calls control-center at all (tenant slug not required).
//
// Because it is a confmap provider compiled into the collector binary, it runs
// identically under every install method (systemd, docker, Kubernetes, manual) —
// no per-platform wrapper or init step. The resolve happens exactly once per
// process (memoized) regardless of how many ${leansignal:...} refs the config has.
//
// Referencing any ${leansignal:...} key requires LEANSIGNAL_TENANT to be set.
// Configs that set the endpoints explicitly (${env:...}) never trigger a resolve.
package resolveprovider // import "github.com/leansignal/leansignal-agent/components/resolveprovider"

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/collector/confmap"
	"go.uber.org/zap"
)

const (
	schemeName = "leansignal"

	// defaultCCURL is the stable control-center origin. Overridable for
	// self-hosted / non-prod control planes via LEANSIGNAL_CC_URL.
	defaultCCURL = "https://cc.leansignal.io"

	// defaultResolveAAT is the public resolve-endpoint token (the same value the
	// lean-ui bundle ships as its VITE_RESOLVE_AAT default). It is not a secret —
	// the resolve endpoint only reveals whether a tenant exists and its region.
	// Overridable via LEANSIGNAL_RESOLVE_AAT.
	defaultResolveAAT = "fad77809-e6c4-49b0-a508-0e1e469e6553"

	envTenant = "LEANSIGNAL_TENANT"
	envCCURL  = "LEANSIGNAL_CC_URL"
	envAAT    = "LEANSIGNAL_RESOLVE_AAT"
	// envDomain short-circuits the resolve call: when set, the region domain is
	// taken verbatim and no request is made to control-center (air-gapped / tests).
	envDomain = "LEANSIGNAL_DOMAIN"

	resolveTimeout = 10 * time.Second
	resolveRetries = 3
)

// overrideEnvByKey maps each ${leansignal:<key>} to the explicit env var that
// pins it. A non-empty value short-circuits derivation (and, if every requested
// key is pinned, the control-center resolve is never performed).
var overrideEnvByKey = map[string]string{
	"region":    envDomain, // LEANSIGNAL_DOMAIN
	"api":       "LEANSIGNAL_API_ENDPOINT",
	"grpc":      "LEANSIGNAL_ENDPOINT",
	"dataplane": "LEANSIGNAL_DATAPLANE_ENDPOINT",
	"loki":      "LEANSIGNAL_LOKI_ENDPOINT",
	"tempo":     "LEANSIGNAL_TEMPO_ENDPOINT",
}

type provider struct {
	logger *zap.Logger
	client *http.Client

	once sync.Once
	vals map[string]string
	err  error
}

// NewFactory returns a factory for the "leansignal" confmap provider.
//
// Usage in config: ${leansignal:tempo}, ${leansignal:loki},
// ${leansignal:dataplane}, ${leansignal:grpc}, ${leansignal:api},
// ${leansignal:region}.
func NewFactory() confmap.ProviderFactory {
	return confmap.NewProviderFactory(newProvider)
}

func newProvider(ps confmap.ProviderSettings) confmap.Provider {
	return &provider{
		logger: ps.Logger,
		client: &http.Client{Timeout: resolveTimeout},
	}
}

func (p *provider) Retrieve(ctx context.Context, uri string, _ confmap.WatcherFunc) (*confmap.Retrieved, error) {
	if !strings.HasPrefix(uri, schemeName+":") {
		return nil, fmt.Errorf("%q uri is not supported by %q provider", uri, schemeName)
	}
	key := strings.TrimSpace(uri[len(schemeName)+1:])

	// Explicit per-key override wins and never triggers a control-center lookup.
	if envName, ok := overrideEnvByKey[key]; ok {
		if v := strings.TrimSpace(os.Getenv(envName)); v != "" {
			return confmap.NewRetrievedFromYAML([]byte(v))
		}
	}

	p.once.Do(func() { p.vals, p.err = p.resolve(ctx) })
	if p.err != nil {
		return nil, p.err
	}

	val, ok := p.vals[key]
	if !ok {
		keys := make([]string, 0, len(p.vals))
		for k := range p.vals {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		return nil, fmt.Errorf("unknown %q key %q (valid: %s)", schemeName, key, strings.Join(keys, ", "))
	}
	return confmap.NewRetrievedFromYAML([]byte(val))
}

func (*provider) Scheme() string { return schemeName }

func (*provider) Shutdown(context.Context) error { return nil }

// resolve performs the one-time control-center lookup and endpoint derivation.
func (p *provider) resolve(ctx context.Context) (map[string]string, error) {
	slug := strings.TrimSpace(os.Getenv(envTenant))
	if slug == "" {
		return nil, fmt.Errorf("%s must be set (the tenant slug) to use ${%s:...} config references", envTenant, schemeName)
	}

	domain := strings.TrimSpace(os.Getenv(envDomain))
	if domain == "" {
		ccURL := envOr(envCCURL, defaultCCURL)
		aat := envOr(envAAT, defaultResolveAAT)
		d, err := p.resolveRegion(ctx, ccURL, slug, aat)
		if err != nil {
			return nil, err
		}
		domain = d
		p.logger.Info("leansignal: resolved tenant region from control-center",
			zap.String("tenant", slug), zap.String("region", domain))
	} else {
		p.logger.Info("leansignal: using region override (no control-center lookup)",
			zap.String("tenant", slug), zap.String("region", domain))
	}

	return deriveEndpoints(slug, domain), nil
}

// resolveRegion calls GET <ccURL>/resolve_tenant?tenant=<slug>&aat=<aat> and
// recovers the region domain from the returned api_url.
func (p *provider) resolveRegion(ctx context.Context, ccURL, slug, aat string) (string, error) {
	base := strings.TrimRight(ccURL, "/")
	q := url.Values{"tenant": {slug}, "aat": {aat}}
	endpoint := base + "/resolve_tenant?" + q.Encode()

	var lastErr error
	for attempt := 1; attempt <= resolveRetries; attempt++ {
		apiURL, err := p.doResolve(ctx, endpoint, slug)
		if err == nil {
			region := strings.TrimPrefix(apiURL, slug+"-api.")
			if region == apiURL || region == "" {
				return "", fmt.Errorf("resolve_tenant returned unexpected api_url %q for tenant %q (want <slug>-api.<region>)", apiURL, slug)
			}
			return region, nil
		}
		lastErr = err
		if attempt < resolveRetries {
			p.logger.Warn("leansignal: resolve attempt failed, retrying",
				zap.Int("attempt", attempt), zap.Error(err))
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(time.Duration(attempt) * time.Second):
			}
		}
	}
	return "", fmt.Errorf("resolve tenant %q via %s: %w", slug, base, lastErr)
}

func (p *provider) doResolve(ctx context.Context, endpoint, slug string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", err
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// 404 = bad/absent aat token (the endpoint hides itself); anything else is
		// a transient/control-plane error.
		return "", fmt.Errorf("resolve_tenant HTTP %d", resp.StatusCode)
	}

	var body struct {
		APIURL string `json:"api_url"`
		Tenant bool   `json:"tenant"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", fmt.Errorf("decode resolve_tenant response: %w", err)
	}
	if !body.Tenant || body.APIURL == "" {
		return "", fmt.Errorf("tenant %q does not exist (resolve_tenant: tenant=%t)", slug, body.Tenant)
	}
	return body.APIURL, nil
}

// deriveEndpoints builds every backend URL from the slug + region domain. The
// per-signal ingest host pattern (<slug>-{metrics,logs,traces}-ingest) mirrors
// control-center's SetAllocated host renaming.
func deriveEndpoints(slug, region string) map[string]string {
	return map[string]string{
		"region":    region,
		"api":       "https://" + slug + "-api." + region,
		"grpc":      slug + "-grpc." + region + ":443",
		"dataplane": "https://" + slug + "-metrics-ingest." + region,
		"loki":      "https://" + slug + "-logs-ingest." + region,
		"tempo":     "https://" + slug + "-traces-ingest." + region,
	}
}

func envOr(name, def string) string {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		return v
	}
	return def
}
