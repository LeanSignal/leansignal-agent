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

// leansignaltracedemandfilter/resourceattrs.go
package leansignaltracedemandfilter

import (
	"go.opentelemetry.io/collector/pdata/pcommon"
)

// resourceLabels flattens one resource's attributes into the label map a
// demand selector is evaluated against.  Keys carry the TraceQL "resource."
// scope prefix (e.g. `resource.service.name`) so selectors match exactly the
// names dashboard TraceQL queries use.  Unlike the logs filter there is no
// promotion/translation layer to replicate — Tempo indexes resource attributes
// under these same keys.  Values are stringified with AsString (the TraceQL
// string form for non-string attributes).
func resourceLabels(attrs pcommon.Map) map[string]string {
	labels := make(map[string]string, attrs.Len())
	for k, v := range attrs.All() {
		labels["resource."+k] = v.AsString()
	}
	return labels
}
