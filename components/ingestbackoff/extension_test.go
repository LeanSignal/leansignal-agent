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

package leansignalingestbackoff

import (
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/zap"
)

// fakeTransport returns the queued statuses in order and counts real calls.
type fakeTransport struct {
	calls    atomic.Int64
	statuses []int
}

func (f *fakeTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	n := int(f.calls.Add(1)) - 1

	status := http.StatusOK
	if n < len(f.statuses) {
		status = f.statuses[n]
	}

	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader("")),
		Request:    req,
	}, nil
}

func newTestValve(t *testing.T, statuses ...int) (*backoffExtension, http.RoundTripper, *fakeTransport, *time.Time) {
	t.Helper()

	clock := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	ext := newBackoffExtension(zap.NewNop(), &Config{RetryInterval: time.Minute})
	ext.now = func() time.Time { return clock }

	base := &fakeTransport{statuses: statuses}

	rt, err := ext.RoundTripper(base)
	if err != nil {
		t.Fatalf("RoundTripper: %v", err)
	}

	return ext, rt, base, &clock
}

func push(t *testing.T, rt http.RoundTripper) int {
	t.Helper()

	req, _ := http.NewRequest(http.MethodPost, "https://t-metrics-ingest.example.io/api/v1/write", strings.NewReader("x"))

	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	defer resp.Body.Close()

	if _, err := io.ReadAll(resp.Body); err != nil {
		t.Fatalf("read body: %v", err)
	}

	return resp.StatusCode
}

func TestPassThroughWhileHealthy(t *testing.T) {
	_, rt, base, _ := newTestValve(t, http.StatusOK, http.StatusNoContent)

	if got := push(t, rt); got != http.StatusOK {
		t.Fatalf("first push = %d, want 200", got)
	}
	if got := push(t, rt); got != http.StatusNoContent {
		t.Fatalf("second push = %d, want 204", got)
	}
	if base.calls.Load() != 2 {
		t.Errorf("base calls = %d, want 2 (no suppression while healthy)", base.calls.Load())
	}
}

func TestForbiddenPausesWithoutNetwork(t *testing.T) {
	_, rt, base, _ := newTestValve(t, http.StatusForbidden)

	if got := push(t, rt); got != http.StatusForbidden {
		t.Fatalf("rejected push = %d, want 403", got)
	}

	// Every attempt inside the hold window is answered locally.
	for i := 0; i < 5; i++ {
		if got := push(t, rt); got != http.StatusForbidden {
			t.Fatalf("suppressed push = %d, want synthetic 403", got)
		}
	}

	if base.calls.Load() != 1 {
		t.Errorf("base calls = %d, want 1 (suppressed pushes must not touch the network)", base.calls.Load())
	}
}

// After retry_interval exactly ONE probe goes through; its 403 re-arms the
// pause for the peers that follow.
func TestSingleProbeAfterInterval(t *testing.T) {
	_, rt, base, clock := newTestValve(t, http.StatusForbidden, http.StatusForbidden)

	push(t, rt) // arm

	*clock = clock.Add(61 * time.Second)

	push(t, rt) // the probe (hits base, 403 again)
	push(t, rt) // suppressed — the probe claimed the slot
	push(t, rt) // suppressed

	if base.calls.Load() != 2 {
		t.Errorf("base calls = %d, want 2 (one arm + one probe)", base.calls.Load())
	}
}

func TestProbeSuccessResumes(t *testing.T) {
	_, rt, base, clock := newTestValve(t, http.StatusForbidden, http.StatusOK, http.StatusOK)

	push(t, rt) // arm

	*clock = clock.Add(61 * time.Second)

	if got := push(t, rt); got != http.StatusOK { // probe succeeds
		t.Fatalf("probe = %d, want 200", got)
	}

	// Fully resumed: the next push flows immediately, no interval wait.
	if got := push(t, rt); got != http.StatusOK {
		t.Fatalf("post-resume push = %d, want 200", got)
	}

	if base.calls.Load() != 3 {
		t.Errorf("base calls = %d, want 3", base.calls.Load())
	}
}

// Non-403 failures (5xx, 429) are the exporter's business — they must not arm
// the valve.
func TestOtherStatusesDoNotPause(t *testing.T) {
	_, rt, base, _ := newTestValve(t, http.StatusBadGateway, http.StatusTooManyRequests, http.StatusOK)

	push(t, rt)
	push(t, rt)
	push(t, rt)

	if base.calls.Load() != 3 {
		t.Errorf("base calls = %d, want 3 (no suppression on non-403)", base.calls.Load())
	}
}

func TestValidate(t *testing.T) {
	if err := (&Config{RetryInterval: 0}).Validate(); err == nil {
		t.Error("zero retry_interval must fail validation")
	}
	if err := (&Config{RetryInterval: time.Minute}).Validate(); err != nil {
		t.Errorf("valid config rejected: %v", err)
	}
}
