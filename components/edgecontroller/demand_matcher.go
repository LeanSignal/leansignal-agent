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

// leansignaledgecontroller/demand_matcher.go
package leansignaledgecontroller

import (
	"sort"
	"strings"
)

// expandDemandNames expands a demand list (metric names extracted from PromQL)
// into the set of Prometheus series names the demand filter would forward.
//
// It mirrors leansignaldemandfilter.isMetricDemanded at name level: the filter
// keeps a whole histogram/summary family when any of its component series is
// demanded, so all sibling series of a demanded component count as stored.
// Known-cache names are already final Prometheus series names (the tracker
// emits separate _bucket/_sum/_count entries and applies the _total suffix),
// so plain set membership against this expansion matches the filter's verdict.
//
// Rules per demanded name d:
//   - d itself is always included (gauges, counters already _total-suffixed,
//     non-monotonic sums, exponential histograms, exact component names).
//   - d ending in _bucket/_sum/_count: the whole family is kept, so the base
//     and all three component names are included (base covers the summary
//     quantile series when d is a _sum/_count of a summary).
//   - bare d: a summary demanded by base also stores d_sum and d_count, so
//     both are included. d_bucket is NOT added — classic histograms are never
//     matched by base name in the filter.
func expandDemandNames(demands []string) map[string]struct{} {
	set := make(map[string]struct{}, len(demands)*3)
	for _, d := range demands {
		if d == "" {
			continue
		}
		set[d] = struct{}{}

		base := d
		suffixed := false
		for _, suffix := range []string{"_bucket", "_sum", "_count"} {
			if strings.HasSuffix(d, suffix) && len(d) > len(suffix) {
				base = strings.TrimSuffix(d, suffix)
				suffixed = true
				break
			}
		}

		if suffixed {
			set[base] = struct{}{}
			set[base+"_bucket"] = struct{}{}
		}
		set[base+"_sum"] = struct{}{}
		set[base+"_count"] = struct{}{}
	}
	return set
}

// diagnoseDemand partitions the demand list into names that match at least one
// known series (matched) and names that match none (missing / "not found").
// A demanded name matches when its family expansion (expandDemandNames) shares
// any name with knownNames — the same semantics the demand filter uses. Both
// returned slices are sorted.
func diagnoseDemand(demands []string, knownNames map[string]struct{}) (matched, missing []string) {
	for _, d := range demands {
		if d == "" {
			continue
		}
		found := false
		for name := range expandDemandNames([]string{d}) {
			if _, ok := knownNames[name]; ok {
				found = true
				break
			}
		}
		if found {
			matched = append(matched, d)
		} else {
			missing = append(missing, d)
		}
	}
	sort.Strings(matched)
	sort.Strings(missing)
	return matched, missing
}
