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

// leansignaledgecontroller/query_handler.go
//
// Handles QueryRequests pushed by lean-api over the control stream: the agent
// runs the (read-only, allow-listed) HTTP query against its private local
// store — VictoriaMetrics (QUERY_TARGET_VM, the default), Loki
// (QUERY_TARGET_LOKI), or Tempo (QUERY_TARGET_TEMPO) — and returns a
// QueryResponse. This is the read side of the query tunnel (lean-api's
// avm_proxy/aloki_proxy/atempo_proxy → this agent → local store).
package leansignaledgecontroller

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"go.uber.org/zap"

	agentv1 "github.com/leansignal/leansignal-agent/proto/gen/leansignal/agent/v1"
)

const (
	// maxConcurrentQueries bounds in-flight local-store queries (VM + Loki +
	// Tempo share it).
	maxConcurrentQueries = 8
	// queryTimeout caps a single local-store HTTP call (shorter than lean-api's
	// SendQuery deadline so the agent replies before the server gives up).
	queryTimeout = 20 * time.Second
	// maxQueryResponseBytes caps the response body so the QueryResponse envelope
	// stays under the gRPC maxMessageBytes limit (leaving headroom for headers).
	maxQueryResponseBytes = maxMessageBytes - (1 << 20) // 15 MiB
)

// queryHTTPClient runs the local-store query. Its Timeout backstops queryTimeout.
var queryHTTPClient = &http.Client{Timeout: queryTimeout}

// handleQueryRequest executes one QueryRequest against the targeted local store
// (VictoriaMetrics or Loki, per QueryRequest.target) and always sends exactly
// one QueryResponse (carrying either the store's HTTP response or an agent-side
// error code in status_code + error).
func (e *edgeControllerExtension) handleQueryRequest(correlationID uint64, req *agentv1.QueryRequest) {
	// Bound concurrency; reject (429) rather than queue unboundedly. The
	// semaphore is shared across targets — one budget for all local queries.
	select {
	case e.querySem <- struct{}{}:
		defer func() { <-e.querySem }()
	default:
		e.sendQueryResponse(correlationID, errResp(http.StatusTooManyRequests, "too many concurrent queries"))
		return
	}

	// Resolve the target store: base URL + per-target read allow-list.
	var (
		baseURL   string
		isAllowed func(method, cleanPath string) bool
	)
	switch req.GetTarget() {
	case agentv1.QueryTarget_QUERY_TARGET_LOKI:
		if e.config.LocalLokiQueryURL == "" {
			e.sendQueryResponse(correlationID, errResp(http.StatusServiceUnavailable, "query disabled: local_loki_query_url not configured"))
			return
		}
		baseURL = e.config.LocalLokiQueryURL
		isAllowed = isAllowedLokiPath
	case agentv1.QueryTarget_QUERY_TARGET_TEMPO:
		if e.config.LocalTempoQueryURL == "" {
			e.sendQueryResponse(correlationID, errResp(http.StatusServiceUnavailable, "query disabled: local_tempo_query_url not configured"))
			return
		}
		baseURL = e.config.LocalTempoQueryURL
		isAllowed = isAllowedTempoPath
	default: // QUERY_TARGET_VM — also what old servers send (zero value)
		if e.config.LocalVMQueryURL == "" {
			e.sendQueryResponse(correlationID, errResp(http.StatusServiceUnavailable, "query disabled: local_vm_query_url not configured"))
			return
		}
		baseURL = e.config.LocalVMQueryURL
		isAllowed = isAllowedVMPath
	}

	method := strings.ToUpper(req.GetMethod())
	if method == "" {
		method = http.MethodGet
	}
	cleanPath := path.Clean("/" + strings.TrimPrefix(req.GetPath(), "/"))
	if !isAllowed(method, cleanPath) {
		e.logger.Warn("query rejected by allowlist",
			zap.String("method", method),
			zap.String("path", cleanPath),
			zap.String("target", req.GetTarget().String()),
		)
		e.sendQueryResponse(correlationID, errResp(http.StatusForbidden, "path not allowed"))
		return
	}

	target, err := url.Parse(baseURL)
	if err != nil {
		e.sendQueryResponse(correlationID, errResp(http.StatusInternalServerError, "invalid local query base url"))
		return
	}
	target.Path = cleanPath
	target.RawQuery = req.GetRawQuery()

	ctx, cancel := context.WithTimeout(e.rootCtx, queryTimeout)
	defer cancel()

	httpReq, err := http.NewRequestWithContext(ctx, method, target.String(), bytes.NewReader(req.GetBody()))
	if err != nil {
		e.sendQueryResponse(correlationID, errResp(http.StatusInternalServerError, "build request: "+err.Error()))
		return
	}
	for _, h := range req.GetHeaders() {
		if strings.EqualFold(h.GetName(), "Host") {
			continue
		}
		for _, v := range h.GetValues() {
			httpReq.Header.Add(h.GetName(), v)
		}
	}

	resp, err := queryHTTPClient.Do(httpReq)
	if err != nil {
		e.logger.Warn("local store query failed",
			zap.String("path", cleanPath),
			zap.String("target", req.GetTarget().String()),
			zap.Error(err))
		e.sendQueryResponse(correlationID, errResp(http.StatusBadGateway, "local store unreachable: "+err.Error()))
		return
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxQueryResponseBytes+1))
	if err != nil {
		e.sendQueryResponse(correlationID, errResp(http.StatusBadGateway, "read response: "+err.Error()))
		return
	}
	if len(body) > maxQueryResponseBytes {
		e.sendQueryResponse(correlationID, errResp(http.StatusRequestEntityTooLarge, "response too large"))
		return
	}

	out := &agentv1.QueryResponse{
		StatusCode: int32(resp.StatusCode),
		Body:       body,
	}
	for _, name := range []string{"Content-Type", "Content-Encoding"} {
		if v := resp.Header.Values(name); len(v) > 0 {
			out.Headers = append(out.Headers, &agentv1.Header{Name: name, Values: v})
		}
	}
	e.sendQueryResponse(correlationID, out)
}

// sendQueryResponse sends a QueryResponse back over the control stream.
func (e *edgeControllerExtension) sendQueryResponse(correlationID uint64, resp *agentv1.QueryResponse) {
	if err := e.sendAgentMessage(&agentv1.AgentMessage{
		CorrelationId: correlationID,
		Body:          &agentv1.AgentMessage_QueryResponse{QueryResponse: resp},
	}); err != nil {
		e.logger.Warn("Failed to send query response", zap.Error(err))
	}
}

// errResp builds a QueryResponse carrying an agent-side failure.
func errResp(status int, msg string) *agentv1.QueryResponse {
	return &agentv1.QueryResponse{StatusCode: int32(status), Error: msg}
}

// isAllowedVMPath restricts the tunnel to read-only VictoriaMetrics query APIs.
// The path must already be cleaned (no "..", leading slash). Anything else —
// admin, import, delete, write — is refused.
func isAllowedVMPath(method, cleanPath string) bool {
	if method != http.MethodGet && method != http.MethodPost {
		return false
	}
	switch cleanPath {
	case "/api/v1/query",
		"/api/v1/query_range",
		"/api/v1/series",
		"/api/v1/labels",
		"/api/v1/metadata":
		return true
	}
	// /api/v1/label/<name>/values
	if strings.HasPrefix(cleanPath, "/api/v1/label/") && strings.HasSuffix(cleanPath, "/values") {
		return true
	}
	// /api/v1/status/* (read-only: buildinfo, tsdb stats, etc.)
	if strings.HasPrefix(cleanPath, "/api/v1/status/") {
		return true
	}
	return false
}

// isAllowedLokiPath restricts the tunnel to read-only Loki query APIs. The path
// must already be cleaned (no "..", leading slash). Anything else — push,
// delete, admin, and notably /loki/api/v1/tail (WebSocket, doesn't fit the
// one-request/one-response tunnel) — is refused.
func isAllowedLokiPath(method, cleanPath string) bool {
	if method != http.MethodGet && method != http.MethodPost {
		return false
	}
	switch cleanPath {
	case "/loki/api/v1/query",
		"/loki/api/v1/query_range",
		"/loki/api/v1/labels",
		"/loki/api/v1/series",
		"/loki/api/v1/index/stats",
		"/loki/api/v1/index/volume",
		"/loki/api/v1/patterns":
		return true
	}
	// /loki/api/v1/label/<name>/values
	if strings.HasPrefix(cleanPath, "/loki/api/v1/label/") && strings.HasSuffix(cleanPath, "/values") {
		return true
	}
	return false
}

// isAllowedTempoPath restricts the tunnel to read-only Tempo query APIs. The
// path must already be cleaned (no "..", leading slash). Anything else —
// ingest, flush, admin, and the metrics-generator endpoints (/api/metrics/*,
// not enabled on either Tempo) — is refused.
func isAllowedTempoPath(method, cleanPath string) bool {
	if method != http.MethodGet && method != http.MethodPost {
		return false
	}
	switch cleanPath {
	case "/api/echo",
		"/api/search",
		"/api/search/tags",
		"/api/v2/search/tags":
		return true
	}
	// /api/traces/<traceID> and /api/v2/traces/<traceID>
	if strings.HasPrefix(cleanPath, "/api/traces/") || strings.HasPrefix(cleanPath, "/api/v2/traces/") {
		return true
	}
	// /api/search/tag/<tag>/values and /api/v2/search/tag/<tag>/values
	if (strings.HasPrefix(cleanPath, "/api/search/tag/") || strings.HasPrefix(cleanPath, "/api/v2/search/tag/")) &&
		strings.HasSuffix(cleanPath, "/values") {
		return true
	}
	return false
}
