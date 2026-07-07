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

// leansignaledgecontroller/diagnostics.go
package leansignaledgecontroller

import (
	"bytes"
	"os"
	"path/filepath"

	"go.uber.org/zap"
	"gopkg.in/yaml.v3"
)

// Filenames written by the get_diagnosis command, one per timeseries cache.
const (
	knownCacheFile      = "KnownTimeseriesCache.yaml"
	discoveredCacheFile = "DiscoveredTimeseriesCache.yaml"
	demandCacheFile     = "DemandTimeseriesCache.yaml"

	// defaultDiagnosticsDir is a fixed, predictable location used when
	// config.DiagnosticsDir is unset — findable whether the agent runs as root
	// in a container or as a user locally (unlike the working directory or the
	// OS-specific temp path). Override via the diagnostics_dir config key.
	defaultDiagnosticsDir = "/tmp/leansignal-agent"
)

// Top-level YAML documents (struct field order is preserved by yaml.v3, unlike
// maps which get alphabetised — so the files read in a predictable order).
type knownCacheDoc struct {
	Count   int              `yaml:"count"`
	Entries []KnownEntryView `yaml:"entries"`
}

type discoveredCacheDoc struct {
	Count   int                   `yaml:"count"`
	Entries []DiscoveredEntryView `yaml:"entries"`
}

type demandCacheDoc struct {
	Count      int      `yaml:"count"`
	Hash       uint64   `yaml:"hash"`
	LastUpdate int64    `yaml:"last_update"`
	Metrics    []string `yaml:"metrics"`
}

// writeCacheFiles dumps the three timeseries caches to human-readable YAML files
// (one per cache), overwriting on each get_diagnosis. Files go in
// config.DiagnosticsDir (OS temp dir when unset). This is preferred over logging
// because the known cache can hold hundreds of entries. Errors are logged, not
// fatal — the diagnosis summary line is emitted regardless.
func (e *edgeControllerExtension) writeCacheFiles() {
	dir := e.config.DiagnosticsDir
	if dir == "" {
		dir = defaultDiagnosticsDir
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		e.logger.Warn("diagnosis: cannot create diagnostics dir", zap.String("dir", dir), zap.Error(err))
		return
	}

	known := e.knownTimeseriesCache.Snapshot()
	discovered := e.discoveredTimeseriesCache.Snapshot()
	demand := e.demandTimeseriesCache.GetDemands()

	files := []struct {
		name string
		doc  any
	}{
		{knownCacheFile, knownCacheDoc{Count: len(known), Entries: known}},
		{discoveredCacheFile, discoveredCacheDoc{Count: len(discovered), Entries: discovered}},
		{demandCacheFile, demandCacheDoc{
			Count:      len(demand.Timeseries),
			Hash:       demand.DemandHash,
			LastUpdate: demand.LastUpdate,
			Metrics:    demand.Timeseries,
		}},
	}

	written := make([]string, 0, len(files))
	for _, f := range files {
		data, err := marshalYAML(f.doc)
		if err != nil {
			e.logger.Warn("diagnosis: yaml marshal failed", zap.String("file", f.name), zap.Error(err))
			continue
		}
		path := filepath.Join(dir, f.name)
		if err := os.WriteFile(path, data, 0o644); err != nil {
			e.logger.Warn("diagnosis: write failed", zap.String("path", path), zap.Error(err))
			continue
		}
		written = append(written, path)
	}

	e.logger.Info("diagnosis: caches written to disk",
		zap.String("dir", dir),
		zap.Strings("files", written),
	)
}

// marshalYAML renders v as YAML with 2-space indentation.
func marshalYAML(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(v); err != nil {
		_ = enc.Close()
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
