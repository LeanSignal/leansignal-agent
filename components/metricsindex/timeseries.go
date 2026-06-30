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

package leansignalmetricsindex

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync"
)

// HashKey represents an xxhash128 fingerprint for a timeseries.
type HashKey [16]byte

// MarshalJSON encodes HashKey as a hex string (32 characters).
func (h HashKey) MarshalJSON() ([]byte, error) {
	return json.Marshal(hex.EncodeToString(h[:]))
}

// UnmarshalJSON decodes a hex string into HashKey.
func (h *HashKey) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}
	decoded, err := hex.DecodeString(s)
	if err != nil {
		return err
	}
	if len(decoded) != 16 {
		return fmt.Errorf("invalid hash key length: expected 16 bytes, got %d", len(decoded))
	}
	copy(h[:], decoded)
	return nil
}

// String returns the hex representation of the HashKey.
func (h HashKey) String() string {
	return hex.EncodeToString(h[:])
}

// LabelPair represents a key-value pair for a metric label.
type LabelPair struct {
	Name  string
	Value string
}

// TimeseriesEntry represents a single timeseries with its metadata and sample count.
type TimeseriesEntry struct {
	MetricName string
	Labels     []LabelPair // Labels sorted alphabetically by name
	Samples    int
}

// TimeseriesBatch represents a batch of timeseries data sent from the processor.
type TimeseriesBatch struct {
	Data map[HashKey]*TimeseriesEntry
}

// TimeseriesReceiver is an interface for components that want to receive timeseries batches.
type TimeseriesReceiver interface {
	ReceiveTimeseriesBatch(batch *TimeseriesBatch)
}

// timeseriesRegistry is a global registry for timeseries receivers.
var (
	registryMu sync.RWMutex
	receivers  []TimeseriesReceiver
)

// RegisterTimeseriesReceiver registers a receiver to get timeseries batches.
func RegisterTimeseriesReceiver(receiver TimeseriesReceiver) {
	registryMu.Lock()
	defer registryMu.Unlock()
	receivers = append(receivers, receiver)
}

// UnregisterTimeseriesReceiver removes a receiver from the registry.
func UnregisterTimeseriesReceiver(receiver TimeseriesReceiver) {
	registryMu.Lock()
	defer registryMu.Unlock()
	for i, r := range receivers {
		if r == receiver {
			receivers = append(receivers[:i], receivers[i+1:]...)
			return
		}
	}
}

// BroadcastTimeseriesBatch sends a batch to all registered receivers.
func BroadcastTimeseriesBatch(batch *TimeseriesBatch) {
	registryMu.RLock()
	defer registryMu.RUnlock()
	for _, receiver := range receivers {
		receiver.ReceiveTimeseriesBatch(batch)
	}
}
