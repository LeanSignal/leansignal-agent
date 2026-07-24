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

// Package leansignalingestbackoff pauses a tenant-ingest exporter after the
// ingest edge rejects a push with 403 — LeanSignal's "ingest limit exceeded"
// answer (storage ceiling or monthly ingest budget, enforced by lean-api's
// forward-auth at the ingress). Without it the exporter keeps attempting every
// batch against a limit that clears on retention or month rollover, i.e.
// hours-to-days later.
//
// It plugs into the exporter's `auth` slot (extensionauth.HTTPClient) as a
// transport wrapper, one instance per signal (`leansignal_ingest_backoff/
// {metrics,logs,traces}`) so one signal's limit never pauses another. While
// paused it answers each attempt with a locally synthesized 403 — the exporter
// treats that as a permanent error and drops the batch WITHOUT touching the
// network (no retry-queue growth, no connection churn). Every retry_interval
// exactly ONE push is let through as a probe, concurrent batches stay
// suppressed; a probe 403 re-arms the pause, anything else resumes pushing.
// Data dropped while paused is not lost fidelity: the co-located local stores
// keep everything regardless.
package leansignalingestbackoff

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/extension"
	"go.opentelemetry.io/collector/extension/extensionauth"
	"go.uber.org/zap"
)

// configauth resolves these interfaces dynamically at pipeline start — assert
// them here so a drift fails the build, not the agent boot.
var (
	_ extension.Extension      = (*backoffExtension)(nil)
	_ extensionauth.HTTPClient = (*backoffExtension)(nil)
)

// backoffExtension is the per-signal valve; it is both the collector extension
// and the shared state behind every RoundTripper it hands out.
type backoffExtension struct {
	logger        *zap.Logger
	retryInterval time.Duration

	// now is the clock, swappable in tests.
	now func() time.Time

	mu sync.Mutex
	// paused: the last push that reached the edge was rejected with 403.
	paused bool
	// holdUntil: while paused, attempts before this instant are suppressed
	// locally; the first attempt at/after it becomes the probe (claiming the
	// slot pushes holdUntil forward so concurrent attempts stay suppressed).
	holdUntil time.Time
}

func newBackoffExtension(logger *zap.Logger, cfg *Config) *backoffExtension {
	return &backoffExtension{
		logger:        logger,
		retryInterval: cfg.RetryInterval,
		now:           time.Now,
	}
}

// Start implements component.Component.
func (b *backoffExtension) Start(context.Context, component.Host) error { return nil }

// Shutdown implements component.Component.
func (b *backoffExtension) Shutdown(context.Context) error { return nil }

// RoundTripper implements extensionauth.HTTPClient. Purely a valve — it never
// touches credentials (the exporters set their Authorization header via
// config; confighttp applies both).
func (b *backoffExtension) RoundTripper(base http.RoundTripper) (http.RoundTripper, error) {
	return &backoffRoundTripper{ext: b, base: base}, nil
}

// allow reports whether an attempt may reach the network. While paused, the
// first call at/after holdUntil claims the probe slot and re-arms the hold so
// the probe's outcome — not a burst of peers — decides what happens next.
func (b *backoffExtension) allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	if !b.paused {
		return true
	}

	now := b.now()
	if now.Before(b.holdUntil) {
		return false
	}

	b.holdUntil = now.Add(b.retryInterval)

	return true
}

// observe records a real response's status: 403 (re-)arms the pause, anything
// else lifts it. State transitions are logged once, not per batch.
func (b *backoffExtension) observe(status int) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if status == http.StatusForbidden {
		b.holdUntil = b.now().Add(b.retryInterval)
		if !b.paused {
			b.paused = true
			b.logger.Warn("tenant ingest rejected with 403 (ingest limit) — pausing pushes",
				zap.Duration("retry_interval", b.retryInterval))
		}

		return
	}

	if b.paused {
		b.paused = false
		b.logger.Info("tenant ingest accepting again — pushes resumed")
	}
}

type backoffRoundTripper struct {
	ext  *backoffExtension
	base http.RoundTripper
}

func (rt *backoffRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if !rt.ext.allow() {
		// Locally synthesized 403: the exporter maps it to a permanent error
		// and drops the batch immediately — no retries, no queue growth, no
		// network I/O. The exporter requires the body to be drained/closed,
		// hence a real (tiny) body.
		return &http.Response{
			Status:     "403 Forbidden (suppressed by leansignal_ingest_backoff)",
			StatusCode: http.StatusForbidden,
			Proto:      req.Proto,
			ProtoMajor: req.ProtoMajor,
			ProtoMinor: req.ProtoMinor,
			Header:     make(http.Header),
			Body: io.NopCloser(strings.NewReader(fmt.Sprintf(
				"push suppressed by leansignal_ingest_backoff: the ingest edge rejected the last push with 403 (ingest limit); next probe in <= %s", rt.ext.retryInterval))),
			Request: req,
		}, nil
	}

	resp, err := rt.base.RoundTrip(req)
	if err != nil {
		// Transport errors say nothing about limits — leave the state to the
		// exporter's own retry logic.
		return resp, err
	}

	rt.ext.observe(resp.StatusCode)

	return resp, nil
}
