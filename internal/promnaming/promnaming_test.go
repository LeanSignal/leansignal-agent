// Copyright 2026 LeanSignal
// SPDX-License-Identifier: Apache-2.0

package promnaming

import (
	"testing"

	"go.opentelemetry.io/collector/pdata/pmetric"
)

func TestBaseName(t *testing.T) {
	monoSum := func(m pmetric.Metric) {
		s := m.SetEmptySum()
		s.SetIsMonotonic(true)
		s.SetAggregationTemporality(pmetric.AggregationTemporalityCumulative)
	}
	nonMonoSum := func(m pmetric.Metric) {
		s := m.SetEmptySum()
		s.SetIsMonotonic(false)
		s.SetAggregationTemporality(pmetric.AggregationTemporalityCumulative)
	}
	gauge := func(m pmetric.Metric) { m.SetEmptyGauge() }
	histogram := func(m pmetric.Metric) {
		m.SetEmptyHistogram().SetAggregationTemporality(pmetric.AggregationTemporalityCumulative)
	}

	cases := []struct {
		name, unit string
		set        func(pmetric.Metric)
		want       string
	}{
		// The real hostmetrics series that were being dropped.
		{"system.cpu.time", "s", monoSum, "system_cpu_time_seconds_total"},
		{"system.network.io", "By", monoSum, "system_network_io_bytes_total"},
		{"system.memory.usage", "By", nonMonoSum, "system_memory_usage_bytes"},
		{"system.filesystem.usage", "By", nonMonoSum, "system_filesystem_usage_bytes"},
		// Unitless gauges (load average uses an annotation unit) — unchanged.
		{"system.cpu.load_average.1m", "{run_queue_length}", gauge, "system_cpu_load_average_1m"},
		{"my.gauge", "", gauge, "my_gauge"},
		// Histogram base carries the unit; callers append _bucket/_sum/_count.
		{"request.latency", "s", histogram, "request_latency_seconds"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := pmetric.NewMetric()
			m.SetName(tc.name)
			m.SetUnit(tc.unit)
			tc.set(m)
			if got := BaseName(m); got != tc.want {
				t.Errorf("BaseName(%s/%s) = %q, want %q", tc.name, tc.unit, got, tc.want)
			}
		})
	}
}
