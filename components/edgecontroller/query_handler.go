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
// VictoriaMetrics and returns a QueryResponse. This is the read side of the
// query tunnel (lean-api's avm_proxy → this agent → local VM).
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

	agentv1 "github.com/leansignal/leansignal-agent/proto/gen/leansignal/agent/v1"
	"go.uber.org/zap"
)

const (
	// maxConcurrentQueries bounds in-flight local-VM queries.
	maxConcurrentQueries = 8
	// queryTimeout caps a single local-VM HTTP call (shorter than lean-api's
	// SendQuery deadline so the agent replies before the server gives up).
	queryTimeout = 20 * time.Second
	// maxQueryResponseBytes caps the response body so the QueryResponse envelope
	// stays under the gRPC maxMessageBytes limit (leaving headroom for headers).
	maxQueryResponseBytes = maxMessageBytes - (1 << 20) // 15 MiB
)

// queryHTTPClient runs the local-VM query. Its Timeout backstops queryTimeout.
var queryHTTPClient = &http.Client{Timeout: queryTimeout}

// handleQueryRequest executes one QueryRequest against the local VM and always
// sends exactly one QueryResponse (carrying either the VM's HTTP response or an
// agent-side error code in status_code + error).
func (e *edgeControllerExtension) handleQueryRequest(correlationID uint64, req *agentv1.QueryRequest) {
	// Bound concurrency; reject (429) rather than queue unboundedly.
	select {
	case e.querySem <- struct{}{}:
		defer func() { <-e.querySem }()
	default:
		e.sendQueryResponse(correlationID, errResp(http.StatusTooManyRequests, "too many concurrent queries"))
		return
	}

	if e.config.LocalVMQueryURL == "" {
		e.sendQueryResponse(correlationID, errResp(http.StatusServiceUnavailable, "query disabled: local_vm_query_url not configured"))
		return
	}

	method := strings.ToUpper(req.GetMethod())
	if method == "" {
		method = http.MethodGet
	}
	cleanPath := path.Clean("/" + strings.TrimPrefix(req.GetPath(), "/"))
	if !isAllowedVMPath(method, cleanPath) {
		e.logger.Warn("query rejected by allowlist", zap.String("method", method), zap.String("path", cleanPath))
		e.sendQueryResponse(correlationID, errResp(http.StatusForbidden, "path not allowed"))
		return
	}

	target, err := url.Parse(e.config.LocalVMQueryURL)
	if err != nil {
		e.sendQueryResponse(correlationID, errResp(http.StatusInternalServerError, "invalid local_vm_query_url"))
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
		e.logger.Warn("local VM query failed", zap.String("path", cleanPath), zap.Error(err))
		e.sendQueryResponse(correlationID, errResp(http.StatusBadGateway, "local VM unreachable: "+err.Error()))
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
