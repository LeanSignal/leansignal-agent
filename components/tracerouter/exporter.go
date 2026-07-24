// Copyright 2026 LeanSignal
//
// SPDX-License-Identifier: Apache-2.0

// leansignaltracerouter/exporter.go
package leansignaltracerouter

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"strings"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/pdata/ptrace"
	"go.opentelemetry.io/collector/pdata/ptrace/ptraceotlp"
	"go.uber.org/zap"

	tracedemandfilter "github.com/leansignal/leansignal-agent/components/tracedemandfilter"
)

// router exports spans to ONE Tempo org per ingestion rule.
//
// leansignal_trace_demand_filter stamps every emitted ResourceSpans with the id
// of the rule that demanded it (duplicating a resource matched by several
// rules). This exporter groups by that stamp and pushes each group to
// `<endpoint>/v1/traces/r/<filter-id>`; the tenant ingress forward-auths the
// path and lean-api answers with `X-Scope-OrgID: <tenant>__<filter-id>`, so the
// spans land in that rule's own Tempo org.
//
// Why not the stock otlphttp exporter: its endpoint is fixed at config time,
// and the set of orgs changes with the demand set. Only the push differs
// though, so queueing/retry stay with exporterhelper.
//
// The stamp is stripped before sending — it is agent-internal routing, not
// tenant data. Spans arriving without one (e.g. a server that predates per-rule
// routing) go to `<endpoint>/v1/traces`, the tenant-wide org, unchanged.
type router struct {
	logger *zap.Logger
	cfg    *Config
	client *http.Client
}

func newRouter(logger *zap.Logger, cfg *Config) *router {
	return &router{logger: logger, cfg: cfg}
}

func (r *router) start(_ context.Context, _ component.Host) error {
	r.client = &http.Client{Timeout: r.cfg.Timeout}

	return nil
}

// capabilities: the stamp is removed from the batch, so the data is mutated.
func (r *router) capabilities() consumer.Capabilities {
	return consumer.Capabilities{MutatesData: true}
}

// pushTraces splits the batch by rule and pushes each group.
//
// A failure on ANY group fails the whole push, so exporterhelper retries it.
// Retrying re-sends the groups that already succeeded — trace ingest is
// append-only and Tempo dedupes nothing, so that costs duplicate spans on
// retry. Accepted deliberately: the alternative (partial success bookkeeping)
// would drop data whenever one org's push failed.
func (r *router) pushTraces(ctx context.Context, td ptrace.Traces) error {
	groups := groupByFilterID(td)

	for filterID, batch := range groups {
		if err := r.push(ctx, filterID, batch); err != nil {
			return fmt.Errorf("push to rule %q: %w", filterID, err)
		}
	}

	return nil
}

// groupByFilterID buckets ResourceSpans by their rule stamp, stripping it. The
// empty-string key holds unstamped spans (tenant-wide org).
func groupByFilterID(td ptrace.Traces) map[string]ptrace.Traces {
	out := make(map[string]ptrace.Traces)
	src := td.ResourceSpans()

	for i := 0; i < src.Len(); i++ {
		rs := src.At(i)

		filterID := ""
		if v, ok := rs.Resource().Attributes().Get(tracedemandfilter.FilterIDAttr); ok {
			filterID = v.Str()
		}

		batch, ok := out[filterID]
		if !ok {
			batch = ptrace.NewTraces()
			out[filterID] = batch
		}

		dst := batch.ResourceSpans().AppendEmpty()
		rs.CopyTo(dst)
		dst.Resource().Attributes().Remove(tracedemandfilter.FilterIDAttr)
	}

	return out
}

// push sends one group as OTLP/HTTP protobuf to the path that names its rule.
func (r *router) push(ctx context.Context, filterID string, td ptrace.Traces) error {
	body, err := ptraceotlp.NewExportRequestFromTraces(td).MarshalProto()
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.pathFor(filterID), bytes.NewReader(body))
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/x-protobuf")

	for k, v := range r.cfg.Headers {
		req.Header.Set(k, v)
	}

	resp, err := r.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d from %s", resp.StatusCode, r.pathFor(filterID))
	}

	return nil
}

// pathFor returns the push URL for a rule — or the plain traces path when the
// batch carries no rule stamp.
func (r *router) pathFor(filterID string) string {
	base := strings.TrimRight(r.cfg.Endpoint, "/")
	if filterID == "" {
		return base + "/v1/traces"
	}

	return base + "/v1/traces/r/" + filterID
}
