// Copyright 2026 LeanSignal
//
// SPDX-License-Identifier: Apache-2.0

// Package tracedemand holds the trace-routing value type shared by the edge
// controller (which receives it in the demand set) and the trace demand filter
// (which routes spans by it). It exists only so neither package has to import
// the other.
package tracedemand

// Route pairs a demanded trace resource selector with the id of the filter —
// the ingestion rule — that demands it.
//
// The filter id is the routing key: spans matching this selector are pushed to
// that rule's own Tempo org, because Tempo cannot delete a subset of a org's
// data. One org per rule is what makes deleting a rule able to purge its spans;
// a resource matched by several rules is therefore exported once per rule.
type Route struct {
	Selector string
	FilterID string
}
