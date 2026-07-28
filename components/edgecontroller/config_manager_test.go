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

//go:build !windows

// leansignaledgecontroller/config_manager_test.go
package leansignaledgecontroller

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.uber.org/zap"
)

func TestParseConfigFlags(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want []string
	}{
		{
			name: "separate values, install layout",
			args: []string{"--config", "file:/etc/leansignal-agent/config.yaml", "--config", "file:/etc/leansignal-agent/localstore-logs.yaml"},
			want: []string{"file:/etc/leansignal-agent/config.yaml", "file:/etc/leansignal-agent/localstore-logs.yaml"},
		},
		{
			name: "equals form",
			args: []string{"--config=file:/etc/agent.yaml"},
			want: []string{"file:/etc/agent.yaml"},
		},
		{
			name: "single dash",
			args: []string{"-config", "file:/a.yaml", "-config=file:/b.yaml"},
			want: []string{"file:/a.yaml", "file:/b.yaml"},
		},
		{
			name: "ignores unrelated flags",
			args: []string{"--feature-gates", "foo", "--config", "file:/a.yaml", "--set", "x=y"},
			want: []string{"file:/a.yaml"},
		},
		{
			name: "no config flag",
			args: []string{"--help"},
			want: nil,
		},
		{
			name: "dangling flag is not a value",
			args: []string{"--config"},
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseConfigFlags(tt.args)
			if len(got) != len(tt.want) {
				t.Fatalf("parseConfigFlags(%v) = %v, want %v", tt.args, got, tt.want)
			}

			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("uri %d = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestURIPath(t *testing.T) {
	tests := []struct {
		uri      string
		wantPath string
		wantOK   bool
	}{
		{"file:/etc/agent.yaml", "/etc/agent.yaml", true},
		{"file:relative.yaml", "relative.yaml", true},
		{"/etc/agent.yaml", "/etc/agent.yaml", true},
		{"env:CONFIG", "", false},
		{"yaml:service::pipelines::metrics::exporters: [otlp]", "", false},
		{"leansignal:grpc", "", false},
		{"https://example.com/config.yaml", "", false},
		{"file:", "", false},
		{"", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.uri, func(t *testing.T) {
			path, ok := uriPath(tt.uri)
			if ok != tt.wantOK || path != tt.wantPath {
				t.Errorf("uriPath(%q) = (%q, %v), want (%q, %v)", tt.uri, path, ok, tt.wantPath, tt.wantOK)
			}
		})
	}
}

// newTestManager builds a manager over a temp config dir with a stub validator,
// so no real collector binary and no real SIGHUP are involved.
func newTestManager(t *testing.T, validatorExitCode int, extraURIs ...string) (*configManager, string) {
	t.Helper()

	dir := t.TempDir()
	primary := filepath.Join(dir, "config.yaml")

	if err := os.WriteFile(primary, []byte("service:\n  extensions: []\n"), 0o644); err != nil {
		t.Fatalf("seed config: %v", err)
	}

	// A stub standing in for `leansignal-agent validate`: it echoes its args so
	// tests can assert on them, then exits with the requested code.
	validator := filepath.Join(dir, "validator.sh")
	script := "#!/bin/sh\necho \"args: $*\"\n"

	if validatorExitCode != 0 {
		script += "echo 'error decoding: invalid keys: nosuchreceiver' >&2\n"
	}

	script += "exit " + string(rune('0'+validatorExitCode)) + "\n"

	if err := os.WriteFile(validator, []byte(script), 0o700); err != nil {
		t.Fatalf("write validator: %v", err)
	}

	uris := append([]string{fileScheme + primary}, extraURIs...)

	m := &configManager{
		logger:       zap.NewNop(),
		uris:         uris,
		primary:      primary,
		writeEnabled: true,
		executable:   validator,
		reloadFn:     func() error { return nil },
	}

	return m, primary
}

func TestSnapshotReadsEveryFileSource(t *testing.T) {
	m, primary := newTestManager(t, 0, "env:SOMETHING")

	snap := m.Snapshot()

	if snap.GetError() != "" {
		t.Fatalf("unexpected snapshot error: %s", snap.GetError())
	}

	if got := len(snap.GetFiles()); got != 2 {
		t.Fatalf("got %d files, want 2", got)
	}

	if snap.GetPrimaryPath() != primary {
		t.Errorf("primary_path = %q, want %q", snap.GetPrimaryPath(), primary)
	}

	if !snap.GetWriteEnabled() {
		t.Error("write_enabled = false, want true")
	}

	first := snap.GetFiles()[0]
	if string(first.GetContent()) != "service:\n  extensions: []\n" {
		t.Errorf("content = %q", first.GetContent())
	}

	if !first.GetWritable() {
		t.Error("expected the temp-dir config to be writable")
	}

	// The non-file source is reported but carries nothing.
	second := snap.GetFiles()[1]
	if second.GetPath() != "env:SOMETHING" || second.GetError() == "" || second.GetWritable() {
		t.Errorf("non-file source reported wrong: %+v", second)
	}
}

// A config the agent does not own is still replaceable when it sits in a
// directory the agent can write: the write is a rename, not an in-place edit.
// This is exactly the Helm chart's writable mode, where a root-run init
// container seeds a root-owned 0644 config into a group-writable volume.
func TestSnapshotWritableWhenOnlyTheDirectoryIsWritable(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: permission bits do not constrain the probe")
	}

	m, primary := newTestManager(t, 0)

	// Read-only file, writable directory.
	if err := os.Chmod(primary, 0o444); err != nil {
		t.Fatalf("chmod: %v", err)
	}

	if f, err := os.OpenFile(primary, os.O_WRONLY, 0); err == nil {
		_ = f.Close()
		t.Skip("filesystem ignores the read-only bit for the owner")
	}

	if !m.Snapshot().GetFiles()[0].GetWritable() {
		t.Error("writable = false for a read-only file in a writable directory")
	}
}

// The directory is what decides it — a read-only one (the ConfigMap-mount case,
// which surfaces as EROFS) must report false.
func TestSnapshotNotWritableWhenTheDirectoryIsReadOnly(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: permission bits do not constrain the probe")
	}

	m, primary := newTestManager(t, 0)
	dir := filepath.Dir(primary)

	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatalf("chmod: %v", err)
	}

	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) }) // so t.TempDir cleanup works

	if m.Snapshot().GetFiles()[0].GetWritable() {
		t.Error("writable = true for a file in a read-only directory")
	}
}

func TestSnapshotReportsMissingFile(t *testing.T) {
	m, primary := newTestManager(t, 0)

	if err := os.Remove(primary); err != nil {
		t.Fatalf("remove: %v", err)
	}

	snap := m.Snapshot()
	if len(snap.GetFiles()) != 1 || snap.GetFiles()[0].GetError() == "" {
		t.Fatalf("expected a per-file read error, got %+v", snap.GetFiles())
	}
}

func TestSnapshotWithNoConfigSources(t *testing.T) {
	m := &configManager{logger: zap.NewNop()}

	if snap := m.Snapshot(); snap.GetError() == "" {
		t.Error("expected an error when the command line has no --config")
	}
}

func TestSnapshotMarksNotWritableWhenDisabled(t *testing.T) {
	m, _ := newTestManager(t, 0)
	m.writeEnabled = false

	snap := m.Snapshot()
	if snap.GetWriteEnabled() {
		t.Error("write_enabled = true, want false")
	}

	if snap.GetFiles()[0].GetWritable() {
		t.Error("file reported writable while remote writes are disabled")
	}

	// Reading must still work — the kill switch gates writes only.
	if len(snap.GetFiles()[0].GetContent()) == 0 {
		t.Error("expected the config to still be readable")
	}
}

func TestApplyWritesValidatedConfigAndBacksUpThePrevious(t *testing.T) {
	m, primary := newTestManager(t, 0)

	newContent := []byte("service:\n  pipelines: {}\n")

	msg, reload, err := m.Apply(context.Background(), primary, newContent, false)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	if !reload {
		t.Error("expected the caller to be told to reload")
	}

	if !strings.Contains(msg, primary) {
		t.Errorf("message does not name the file: %q", msg)
	}

	got, err := os.ReadFile(primary)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}

	if string(got) != string(newContent) {
		t.Errorf("config = %q, want %q", got, newContent)
	}

	backup, err := os.ReadFile(primary + backupSuffix)
	if err != nil {
		t.Fatalf("read backup: %v", err)
	}

	if string(backup) != "service:\n  extensions: []\n" {
		t.Errorf("backup = %q, want the previous contents", backup)
	}

	// No staging file left behind.
	entries, _ := os.ReadDir(filepath.Dir(primary))
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("staging file left behind: %s", e.Name())
		}
	}
}

func TestApplyRejectsInvalidConfigAndLeavesTheOriginal(t *testing.T) {
	m, primary := newTestManager(t, 1) // validator fails

	original, _ := os.ReadFile(primary)

	_, _, err := m.Apply(context.Background(), primary, []byte("service:\n  bogus: true\n"), false)
	if err == nil {
		t.Fatal("expected the invalid config to be rejected")
	}

	if !strings.Contains(err.Error(), "nosuchreceiver") {
		t.Errorf("error should carry the validator output, got: %v", err)
	}

	after, _ := os.ReadFile(primary)
	if string(after) != string(original) {
		t.Error("the original config was modified despite validation failing")
	}

	if _, err := os.Stat(primary + backupSuffix); err == nil {
		t.Error("a backup was written for a rejected config")
	}
}

func TestApplyValidatesTheMergedConfigWithTheCandidateSubstituted(t *testing.T) {
	overlay := "file:/etc/leansignal-agent/localstore-logs.yaml"
	m, primary := newTestManager(t, 0, overlay)

	// The stub validator echoes its args into the success message path; capture
	// them by making validation fail and reading the error instead.
	m.executable = filepath.Join(filepath.Dir(primary), "echoing.sh")
	if err := os.WriteFile(m.executable, []byte("#!/bin/sh\necho \"$*\" >&2\nexit 1\n"), 0o700); err != nil {
		t.Fatalf("write validator: %v", err)
	}

	_, _, err := m.Apply(context.Background(), primary, []byte("service: {}\n"), false)
	if err == nil {
		t.Fatal("expected failure from the stub validator")
	}

	got := err.Error()

	if !strings.Contains(got, "validate") {
		t.Errorf("validator was not called with the validate subcommand: %s", got)
	}

	// The overlay must be passed through untouched...
	if !strings.Contains(got, overlay) {
		t.Errorf("overlay config was not passed to the validator: %s", got)
	}

	// ...and the file under edit must have been swapped for the staged
	// candidate, whose path is rewritten back to the real one in the message.
	if strings.Count(got, primary) == 0 {
		t.Errorf("candidate path was not rewritten to the real path: %s", got)
	}
}

func TestApplyRefusesWhenValidationCannotRun(t *testing.T) {
	m, primary := newTestManager(t, 0)
	m.executable = "" // e.g. os.Executable() failed at startup

	_, _, err := m.Apply(context.Background(), primary, []byte("service: {}\n"), false)
	if err == nil {
		t.Fatal("expected the write to be refused when it cannot be validated")
	}

	if !strings.Contains(err.Error(), "validation unavailable") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestApplyRejections(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		content  string
		disabled bool
		wantErr  string
	}{
		{
			name:     "remote writes disabled",
			content:  "service: {}\n",
			disabled: true,
			wantErr:  "remote config write is disabled",
		},
		{
			name:    "unknown path",
			path:    "/etc/passwd",
			content: "service: {}\n",
			wantErr: "not one of this agent's config files",
		},
		{
			name:    "empty config",
			content: "",
			wantErr: "refusing to write an empty config",
		},
		{
			name:    "malformed yaml",
			content: "service:\n\tbad: [unclosed\n",
			wantErr: "not valid YAML",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, primary := newTestManager(t, 0)
			m.writeEnabled = !tt.disabled

			path := tt.path
			if path == "" {
				path = primary
			}

			_, _, err := m.Apply(context.Background(), path, []byte(tt.content), false)
			if err == nil {
				t.Fatalf("expected an error containing %q", tt.wantErr)
			}

			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %v, want it to contain %q", err, tt.wantErr)
			}

			after, _ := os.ReadFile(primary)
			if string(after) != "service:\n  extensions: []\n" {
				t.Error("the config on disk was modified by a rejected write")
			}
		})
	}
}

func TestApplyEmptyPathUsesPrimary(t *testing.T) {
	m, primary := newTestManager(t, 0)

	if _, _, err := m.Apply(context.Background(), "", []byte("service: {}\n"), false); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	got, _ := os.ReadFile(primary)
	if string(got) != "service: {}\n" {
		t.Errorf("primary config = %q, want the new content", got)
	}
}

func TestApplySkipReloadReportsRestartNeeded(t *testing.T) {
	m, primary := newTestManager(t, 0)

	msg, reload, err := m.Apply(context.Background(), primary, []byte("service: {}\n"), true)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	if reload {
		t.Error("reload = true, want false when skip_reload is set")
	}

	if !strings.Contains(msg, "restart") {
		t.Errorf("message should mention a restart, got %q", msg)
	}
}

func TestNewConfigManagerPicksFirstFileSourceAsPrimary(t *testing.T) {
	m := newConfigManager(zap.NewNop(), &Config{RemoteConfigWrite: true}, []string{
		"--config", "env:BASE",
		"--config", "file:/etc/leansignal-agent/config.yaml",
		"--config", "file:/etc/leansignal-agent/localstore-logs.yaml",
	})

	if m.primary != "/etc/leansignal-agent/config.yaml" {
		t.Errorf("primary = %q, want the first file: source", m.primary)
	}

	if !m.writeEnabled {
		t.Error("writeEnabled = false, want true")
	}
}

func TestNewConfigManagerHonoursConfigFileOverride(t *testing.T) {
	m := newConfigManager(zap.NewNop(), &Config{
		RemoteConfigWrite: false,
		ConfigFile:        "/etc/leansignal-agent/localstore-logs.yaml",
	}, []string{"--config", "file:/etc/leansignal-agent/config.yaml"})

	if m.primary != "/etc/leansignal-agent/localstore-logs.yaml" {
		t.Errorf("primary = %q, want the configured override", m.primary)
	}

	if m.writeEnabled {
		t.Error("writeEnabled = true, want false")
	}
}
