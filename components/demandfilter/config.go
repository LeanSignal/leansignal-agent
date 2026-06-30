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

package leansignaldemandfilter

// Config holds the configuration for the leansignal_demand_filter processor.
type Config struct {
	// LogFiltered, when true, emits a DEBUG log line for every metric that is
	// dropped because it does not appear in the current demand list.
	LogFiltered bool `mapstructure:"log_filtered"`
}
