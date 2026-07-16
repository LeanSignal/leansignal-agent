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

// leansignallogdemandfilter/processor.go
package leansignallogdemandfilter

import (
	"context"
	"strings"
	"sync"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.uber.org/zap"
)

// LogDemandProvider is the interface the filter expects the
// leansignal_edge_controller extension to satisfy.  It is defined here so that
// the filter owns its own abstraction and has no module-level dependency on
// the edge-controller package.
type LogDemandProvider interface {
	GetLogDemands() []string // normalized LogQL stream selectors
}

// logDemandFilterProcessor filters OTLP logs by the current log-demand list
// (LogQL stream selectors) held in the leansignal_edge_controller extension.
// A ResourceLogs group is forwarded iff its computed Loki stream labels match
// ANY demanded selector; everything else is dropped.  Loki builds stream
// labels from resource attributes, so ResourceLogs granularity IS stream
// granularity.
//
// The extension is discovered at Start() time via component.Host.GetExtensions().
// FAIL-CLOSED: if no LogDemandProvider extension is found, or the selector list
// is empty (or entirely unparseable), ALL logs are blocked — the tenant Loki
// only ever receives what is explicitly demanded.
type logDemandFilterProcessor struct {
	logger   *zap.Logger
	next     consumer.Logs
	cfg      *Config
	provider LogDemandProvider // resolved in Start() from the registered extensions

	// Parsed-selector cache: selectors are parsed once per demand-list change
	// (keyed by the joined raw list) and reused for every subsequent batch.
	mu           sync.Mutex
	cachedKey    string
	cachedParsed []*selector
}

func newLogDemandFilterProcessor(
	logger *zap.Logger,
	next consumer.Logs,
	cfg *Config,
) *logDemandFilterProcessor {
	return &logDemandFilterProcessor{
		logger: logger,
		next:   next,
		cfg:    cfg,
	}
}

// Start resolves the LogDemandProvider by iterating the registered extensions
// and finding the first one that satisfies the LogDemandProvider interface
// (i.e. the leansignal_edge_controller extension).
func (p *logDemandFilterProcessor) Start(_ context.Context, host component.Host) error {
	for _, ext := range host.GetExtensions() {
		if dp, ok := ext.(LogDemandProvider); ok {
			p.provider = dp
			p.logger.Info("log demand filter: connected to LogDemandProvider extension")
			return nil
		}
	}
	p.logger.Warn("log demand filter: no LogDemandProvider extension found; all logs will be blocked")
	return nil
}

// Shutdown is part of component.Component.
func (p *logDemandFilterProcessor) Shutdown(_ context.Context) error {
	return nil
}

// Capabilities declares that this processor mutates data (it removes logs).
func (p *logDemandFilterProcessor) Capabilities() consumer.Capabilities {
	return consumer.Capabilities{MutatesData: true}
}

// ConsumeLogs filters the incoming batch by the current log-demand selectors.
//
// The selector list is read fresh on every call directly from the edge
// controller's demand cache — there is no separate propagation mechanism.  The
// moment the edge controller receives a demand_set command and updates the
// cache, the very next batch processed here uses the new list.
func (p *logDemandFilterProcessor) ConsumeLogs(ctx context.Context, ld plog.Logs) error {
	totalRecords := ld.LogRecordCount()

	// FAIL-CLOSED: no provider → drop everything.
	if p.provider == nil {
		p.logger.Info("log demand filter: no LogDemandProvider extension, blocking all logs",
			zap.Int("received", totalRecords),
			zap.Int("allowed", 0),
		)
		return nil
	}

	// FAIL-CLOSED: no demand received yet (or demand is empty) → drop everything.
	raw := p.provider.GetLogDemands()
	if len(raw) == 0 {
		p.logger.Info("log demand filter: no log demand yet, blocking all logs",
			zap.Int("received", totalRecords),
			zap.Int("allowed", 0),
		)
		return nil
	}

	// FAIL-CLOSED: every selector failed to parse → drop everything.
	selectors := p.parsedSelectors(raw)
	if len(selectors) == 0 {
		p.logger.Warn("log demand filter: no parseable selectors, blocking all logs",
			zap.Int("received", totalRecords),
			zap.Int("allowed", 0),
			zap.Int("demand_size", len(raw)),
		)
		return nil
	}

	// Keep a ResourceLogs group iff its stream labels match ANY demanded
	// selector.  RemoveIf prunes in place at ResourceLogs granularity — the
	// granularity Loki derives streams at (resource attributes).
	ld.ResourceLogs().RemoveIf(func(rl plog.ResourceLogs) bool {
		labels := streamLabels(rl.Resource().Attributes())
		for _, sel := range selectors {
			if sel.matches(labels) {
				return false // keep
			}
		}
		if p.cfg.LogFiltered {
			p.logger.Debug("log demand filter: dropping resource logs",
				zap.Any("stream_labels", labels),
			)
		}
		return true // drop
	})

	allowedRecords := ld.LogRecordCount()
	p.logger.Info("log demand filter: batch filtered",
		zap.Int("received", totalRecords),
		zap.Int("allowed", allowedRecords),
		zap.Int("dropped", totalRecords-allowedRecords),
		zap.Int("demand_size", len(raw)),
	)

	return p.next.ConsumeLogs(ctx, ld)
}

// parsedSelectors returns the parsed selectors for the given raw demand list,
// re-parsing only when the list changed since the previous batch (cache keyed
// by the joined raw list).  Selectors that fail to parse are skipped with a
// warning so one bad selector cannot poison the rest of the demand set.
func (p *logDemandFilterProcessor) parsedSelectors(raw []string) []*selector {
	key := strings.Join(raw, "\x00")

	p.mu.Lock()
	defer p.mu.Unlock()
	if key == p.cachedKey {
		return p.cachedParsed
	}

	parsed := make([]*selector, 0, len(raw))
	for _, s := range raw {
		sel, err := parseSelector(s)
		if err != nil {
			p.logger.Warn("log demand filter: skipping unparseable selector",
				zap.String("selector", s),
				zap.Error(err),
			)
			continue
		}
		parsed = append(parsed, sel)
	}
	p.cachedKey = key
	p.cachedParsed = parsed
	return parsed
}
