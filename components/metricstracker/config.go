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

// leansignalmetricstracker/config.go
package leansignalmetricstracker

// Config defines settings for the leansignalmetrics_tracker processor.
//
// Example YAML:
//
//	processors:
//	  leansignalmetrics_tracker:
//	    log_metrics: true
//	    log_series: true
type Config struct {
	// LogMetrics controls whether we log first-seen metric names.
	LogMetrics bool `mapstructure:"log_metrics"`

	// LogSeries controls whether we log first-seen time series
	// (metric name + resource attrs + datapoint attrs).
	LogSeries bool `mapstructure:"log_series"`
}
