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

package leansignalmetricstracker

import (
	"context"
	"sync"
	"testing"

	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.uber.org/zap"
)

// nopMetricsConsumer is a no-op consumer.Metrics used as the pipeline's next consumer.
type nopMetricsConsumer struct{}

func (nopMetricsConsumer) Capabilities() consumer.Capabilities {
	return consumer.Capabilities{MutatesData: false}
}

func (nopMetricsConsumer) ConsumeMetrics(_ context.Context, _ pmetric.Metrics) error { return nil }

// buildGaugeMetrics returns a Metrics payload with a single gauge metric carrying
// n data points, each with a distinct attribute (so n unique series).
func buildGaugeMetrics(name string, n int) pmetric.Metrics {
	md := pmetric.NewMetrics()
	sm := md.ResourceMetrics().AppendEmpty().ScopeMetrics().AppendEmpty()
	m := sm.Metrics().AppendEmpty()
	m.SetName(name)
	g := m.SetEmptyGauge()
	for i := 0; i < n; i++ {
		dp := g.DataPoints().AppendEmpty()
		dp.Attributes().PutStr("idx", string(rune('a'+i%26)))
	}
	return md
}

// TestConsumeMetricsConcurrent guards against the shared-state regression where a
// single processor instance kept one mutable CollectorTimeseries. The OTel pipeline
// calls ConsumeMetrics concurrently (one processor instance is shared by every
// receiver feeding the pipeline), so this must be safe for concurrent use.
//
// Run with -race to detect the regression: a shared batch collector triggers
// "fatal error: concurrent map writes" / a data race here.
func TestConsumeMetricsConcurrent(t *testing.T) {
	p := newMetricsTrackerProcessor(zap.NewNop(), nopMetricsConsumer{}, &Config{})

	const goroutines = 8
	const iterations = 200

	var wg sync.WaitGroup
	for r := 0; r < goroutines; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				if err := p.ConsumeMetrics(context.Background(), buildGaugeMetrics("m", 30)); err != nil {
					t.Errorf("ConsumeMetrics returned error: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()
}
