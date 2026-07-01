// Copyright 2026 LeanSignal
// SPDX-License-Identifier: Apache-2.0

package leansignaledgecontroller

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	agentv1 "github.com/leansignal/leansignal-agent/proto/gen/leansignal/agent/v1"
	"go.uber.org/zap"
	"google.golang.org/grpc"
)

// queryPushServer is an in-process AgentControl server that, after the agent's
// Hello, pushes one QueryRequest and records the QueryResponse the agent returns.
type queryPushServer struct {
	agentv1.UnimplementedAgentControlServer
	corrID uint64
	req    *agentv1.QueryRequest

	mu        sync.Mutex
	responses []*agentv1.QueryResponse
	respCorr  []uint64
}

func (s *queryPushServer) Connect(stream agentv1.AgentControl_ConnectServer) error {
	for {
		msg, err := stream.Recv()
		if err != nil {
			return err
		}
		switch b := msg.GetBody().(type) {
		case *agentv1.AgentMessage_Hello:
			_ = stream.Send(&agentv1.ServerMessage{
				CorrelationId: s.corrID,
				Body:          &agentv1.ServerMessage_QueryRequest{QueryRequest: s.req},
			})
		case *agentv1.AgentMessage_QueryResponse:
			s.mu.Lock()
			s.responses = append(s.responses, b.QueryResponse)
			s.respCorr = append(s.respCorr, msg.GetCorrelationId())
			s.mu.Unlock()
		}
	}
}

func (s *queryPushServer) firstResponse() (*agentv1.QueryResponse, uint64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.responses) == 0 {
		return nil, 0, false
	}
	return s.responses[0], s.respCorr[0], true
}

// startAgentForQuery boots the fake server + a connected agent with the given
// local-VM query base.
func startAgentForQuery(t *testing.T, fake *queryPushServer, localVMQueryURL string) func() {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := grpc.NewServer()
	agentv1.RegisterAgentControlServer(srv, fake)
	go func() { _ = srv.Serve(lis) }()

	cfg := &Config{
		Endpoint:          lis.Addr().String(),
		AgentKey:          "test-key",
		Insecure:          true,
		ReconnectInterval: 500 * time.Millisecond,
		PingInterval:      500 * time.Millisecond,
		LocalVMQueryURL:   localVMQueryURL,
	}
	e := newEdgeControllerExtension(zap.NewNop(), cfg)
	if err := e.Start(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	return func() { _ = e.Shutdown(context.Background()); srv.Stop() }
}

func TestQueryTunnelSuccess(t *testing.T) {
	var hits int32
	vm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		if r.URL.Path != "/api/v1/query" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		if got := r.URL.Query().Get("query"); got != "up" {
			t.Errorf("query param: got %q want up", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[]}}`))
	}))
	defer vm.Close()

	fake := &queryPushServer{
		corrID: 42,
		req: &agentv1.QueryRequest{
			Method:   "GET",
			Path:     "/api/v1/query",
			RawQuery: "query=up",
		},
	}
	defer startAgentForQuery(t, fake, vm.URL)()

	waitFor(t, 3*time.Second, func() bool { _, _, ok := fake.firstResponse(); return ok })
	resp, corr, _ := fake.firstResponse()

	if corr != 42 {
		t.Errorf("correlation id: got %d want 42", corr)
	}
	if resp.GetStatusCode() != http.StatusOK {
		t.Errorf("status: got %d want 200 (error=%q)", resp.GetStatusCode(), resp.GetError())
	}
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Errorf("VM hits: got %d want 1", got)
	}
	if !strings.Contains(string(resp.GetBody()), "success") {
		t.Errorf("body did not tunnel through: %q", resp.GetBody())
	}
	var ct string
	for _, h := range resp.GetHeaders() {
		if h.GetName() == "Content-Type" {
			ct = strings.Join(h.GetValues(), ",")
		}
	}
	if !strings.Contains(ct, "application/json") {
		t.Errorf("Content-Type header not forwarded: %q", ct)
	}
}

func TestQueryTunnelAllowlistRejects(t *testing.T) {
	var hits int32
	vm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
	}))
	defer vm.Close()

	fake := &queryPushServer{
		corrID: 7,
		req:    &agentv1.QueryRequest{Method: "POST", Path: "/api/v1/admin/tsdb/snapshot"},
	}
	defer startAgentForQuery(t, fake, vm.URL)()

	waitFor(t, 3*time.Second, func() bool { _, _, ok := fake.firstResponse(); return ok })
	resp, _, _ := fake.firstResponse()

	if resp.GetStatusCode() != http.StatusForbidden {
		t.Errorf("status: got %d want 403", resp.GetStatusCode())
	}
	time.Sleep(100 * time.Millisecond) // let any (erroneous) HTTP call land
	if got := atomic.LoadInt32(&hits); got != 0 {
		t.Errorf("VM hits: got %d want 0 (allowlist must block before the HTTP call)", got)
	}
}

// TestQueryTunnelPathTraversalRejected ensures path.Clean defeats a "../" escape
// that would otherwise satisfy the /api/v1/label/.../values allowlist branch.
func TestQueryTunnelPathTraversalRejected(t *testing.T) {
	var hits int32
	vm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
	}))
	defer vm.Close()

	fake := &queryPushServer{
		corrID: 9,
		req:    &agentv1.QueryRequest{Method: "GET", Path: "/api/v1/label/up/../../admin/tsdb/values"},
	}
	defer startAgentForQuery(t, fake, vm.URL)()

	waitFor(t, 3*time.Second, func() bool { _, _, ok := fake.firstResponse(); return ok })
	resp, _, _ := fake.firstResponse()

	if resp.GetStatusCode() != http.StatusForbidden {
		t.Errorf("status: got %d want 403 (traversal must be cleaned then rejected)", resp.GetStatusCode())
	}
	time.Sleep(100 * time.Millisecond)
	if got := atomic.LoadInt32(&hits); got != 0 {
		t.Errorf("VM hits: got %d want 0", got)
	}
}

func TestQueryTunnelPOSTBodyAndHeaders(t *testing.T) {
	var gotMethod, gotBody, gotCT string
	vm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		gotCT = r.Header.Get("Content-Type")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"success"}`))
	}))
	defer vm.Close()

	fake := &queryPushServer{
		corrID: 5,
		req: &agentv1.QueryRequest{
			Method:  "POST",
			Path:    "/api/v1/query",
			Body:    []byte("query=up"),
			Headers: []*agentv1.Header{{Name: "Content-Type", Values: []string{"application/x-www-form-urlencoded"}}},
		},
	}
	defer startAgentForQuery(t, fake, vm.URL)()

	waitFor(t, 3*time.Second, func() bool { _, _, ok := fake.firstResponse(); return ok })
	resp, _, _ := fake.firstResponse()
	if resp.GetStatusCode() != http.StatusOK {
		t.Fatalf("status: got %d want 200 (err=%q)", resp.GetStatusCode(), resp.GetError())
	}
	if gotMethod != "POST" {
		t.Errorf("VM method: got %q want POST", gotMethod)
	}
	if gotBody != "query=up" {
		t.Errorf("VM body: got %q want query=up", gotBody)
	}
	if gotCT != "application/x-www-form-urlencoded" {
		t.Errorf("VM Content-Type: got %q (request header not forwarded)", gotCT)
	}
}

func TestQueryTunnelVMNon200PassThrough(t *testing.T) {
	vm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"status":"error","error":"bad query"}`))
	}))
	defer vm.Close()

	fake := &queryPushServer{corrID: 6, req: &agentv1.QueryRequest{Method: "GET", Path: "/api/v1/query", RawQuery: "query=*invalid*"}}
	defer startAgentForQuery(t, fake, vm.URL)()

	waitFor(t, 3*time.Second, func() bool { _, _, ok := fake.firstResponse(); return ok })
	resp, _, _ := fake.firstResponse()
	if resp.GetStatusCode() != http.StatusBadRequest {
		t.Fatalf("status: got %d want 400 (VM status must pass through)", resp.GetStatusCode())
	}
	if !strings.Contains(string(resp.GetBody()), "bad query") {
		t.Errorf("VM error body not passed through: %q", resp.GetBody())
	}
}

func TestQueryTunnelLocalVMUnreachable(t *testing.T) {
	// 127.0.0.1:1 is reliably closed → the agent's HTTP call fails → 502.
	fake := &queryPushServer{corrID: 8, req: &agentv1.QueryRequest{Method: "GET", Path: "/api/v1/query", RawQuery: "query=up"}}
	defer startAgentForQuery(t, fake, "http://127.0.0.1:1")()

	waitFor(t, 5*time.Second, func() bool { _, _, ok := fake.firstResponse(); return ok })
	resp, _, _ := fake.firstResponse()
	if resp.GetStatusCode() != http.StatusBadGateway {
		t.Fatalf("status: got %d want 502", resp.GetStatusCode())
	}
	if resp.GetError() == "" {
		t.Error("expected a non-empty error for an unreachable VM")
	}
}

func TestQueryTunnelDisabledWhenNoURL(t *testing.T) {
	// LocalVMQueryURL empty → the agent refuses with 503.
	fake := &queryPushServer{corrID: 11, req: &agentv1.QueryRequest{Method: "GET", Path: "/api/v1/query", RawQuery: "query=up"}}
	defer startAgentForQuery(t, fake, "")()

	waitFor(t, 3*time.Second, func() bool { _, _, ok := fake.firstResponse(); return ok })
	resp, _, _ := fake.firstResponse()
	if resp.GetStatusCode() != http.StatusServiceUnavailable {
		t.Fatalf("status: got %d want 503 (query disabled)", resp.GetStatusCode())
	}
}

func TestIsAllowedVMPath(t *testing.T) {
	cases := []struct {
		method, path string
		want         bool
	}{
		{"GET", "/api/v1/query", true},
		{"POST", "/api/v1/query", true},
		{"GET", "/api/v1/query_range", true},
		{"GET", "/api/v1/series", true},
		{"GET", "/api/v1/labels", true},
		{"GET", "/api/v1/label/__name__/values", true},
		{"GET", "/api/v1/metadata", true},
		{"GET", "/api/v1/status/tsdb", true},
		{"GET", "/api/v1/admin/tsdb/snapshot", false},
		{"POST", "/api/v1/write", false},
		{"DELETE", "/api/v1/query", false},
		{"PUT", "/api/v1/query", false},
		{"GET", "/api/v1/import", false},
	}
	for _, c := range cases {
		if got := isAllowedVMPath(c.method, c.path); got != c.want {
			t.Errorf("isAllowedVMPath(%q, %q) = %v, want %v", c.method, c.path, got, c.want)
		}
	}
}
