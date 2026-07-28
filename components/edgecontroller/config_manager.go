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

// leansignaledgecontroller/config_manager.go
//
// Remote read/write of the collector's own config files, so an operator can
// inspect and edit the agent config from the LeanSignal UI instead of SSHing to
// the host.
//
// The agent does not receive its config path from anywhere — the collector is
// started with one or more `--config <uri>` flags, so the manager recovers them
// from os.Args. Only `file:` sources (and bare paths) can be read or written;
// other schemes (env:, yaml:, leansignal:, http:) are reported so the UI can
// show the full picture, but carry no content.
//
// Writes are deliberately paranoid, because a config the collector cannot load
// takes the agent down and it can only be recovered over SSH:
//
//	yaml parse -> `<self> validate` dry-run of the MERGED config -> atomic
//	rename into place, previous contents kept as <path>.bak
//
// Only after a successful write does the agent SIGHUP itself, which the
// collector handles as an in-process config reload (otelcol.Collector.Run).
package leansignaledgecontroller

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"go.uber.org/zap"
	"gopkg.in/yaml.v3"

	agentv1 "github.com/leansignal/leansignal-agent/proto/gen/leansignal/agent/v1"
)

const (
	// fileScheme is the confmap provider scheme for on-disk config files. It is
	// the only scheme this manager can read or write.
	fileScheme = "file:"

	// maxConfigFileBytes bounds what the agent will read from or write to a
	// config file. Collector configs are a few KiB; anything near this is a
	// mistake, and the cap keeps a stray file off the control stream.
	maxConfigFileBytes = 1 << 20 // 1 MiB

	// validateTimeout bounds the `validate` dry-run subprocess. It resolves the
	// whole config, which for the ${leansignal:...} provider means a
	// control-center lookup, so allow for a slow network.
	validateTimeout = 30 * time.Second

	// reloadDelay is how long to wait after answering an UpdateConfig before
	// restarting. It covers three things, and the longest one sets it:
	//   - the CommandResult reaching lean-api before the control stream goes;
	//   - the batch processors (1s timeout) handing off their current batch;
	//   - the "restarting to reload the config" line reaching the local log
	//     store, which is two batched hops: the telemetry log processor
	//     (~1s) and then the logs/all batch processor (1s). 2s was exactly on
	//     that boundary and the line usually lost the race, so give it margin.
	reloadDelay = 4 * time.Second

	// exitCodeConfigApplied is the status the process exits with once a new
	// config is on disk, to be restarted by whatever supervises it (systemd,
	// Kubernetes, docker). It is deliberately NON-ZERO so the shipped systemd
	// unit's `Restart=on-failure` restarts it, and deliberately not 1 so an
	// intentional config restart is distinguishable from a genuine crash in
	// `systemctl status` / `kubectl describe`.
	exitCodeConfigApplied = 75

	// backupSuffix is appended to a config file's path to store the contents it
	// had before the most recent remote write.
	backupSuffix = ".bak"
)

// errValidationUnavailable means the candidate config could not be validated at
// all (the validator would not run), as opposed to being found invalid. Writes
// are refused in both cases — an unvalidated config is exactly what kills the
// agent — but the operator-facing message differs.
var errValidationUnavailable = errors.New("config validation unavailable")

// configManager reads and writes the collector config files this process was
// started with.
type configManager struct {
	logger *zap.Logger

	// uris are the `--config` values in command-line order (later ones override
	// earlier ones when the collector merges them).
	uris []string

	// primary is the path UpdateConfig writes when it names no file: the
	// configured override, else the first file: source.
	primary string

	// writeEnabled mirrors Config.RemoteConfigWrite — the host-side kill switch.
	writeEnabled bool

	// executable is the collector binary used for the `validate` dry-run.
	// Overridden in tests.
	executable string

	// reloadFn puts the newly written config into effect. In production it ends
	// the process (see restartForConfig) and never returns; tests override it so
	// they never take the test binary down with them.
	reloadFn func() error
}

// restartForConfig ends the process so its supervisor restarts it with the
// config that was just written.
//
// This deliberately does NOT ask the collector to reload in place. otelcol
// answers SIGHUP by shutting the old service down and rebuilding it, which
// sounds ideal — but the agent's own logs are exported over OTLP to its own
// loopback receiver (service.telemetry, on by default since 0.6.2), and that
// receiver is torn down early in the shutdown. Flushing the logger then retries
// against a dead endpoint for ~20s, fails, and otelcol treats a failed reload as
// fatal and exits anyway. So the in-place reload could never complete on a
// self-monitoring agent: it just made the restart slow and made it look like a
// crash. Exiting on purpose is honest, immediate, and behaves identically on
// systemd, Kubernetes and docker — including Windows, which has no SIGHUP and
// therefore could not reload at all before.
//
// Anything the pipelines still hold is lost, which is why the caller waits
// reloadDelay first: the batch processors run on a 1s timeout, so a beat is
// enough for them to hand off what they have.
//
// The caller also announces the restart BEFORE that wait — see
// handleUpdateConfig. Logging it here instead would put the line one instruction
// ahead of os.Exit, and the agent's own logs travel service.telemetry -> the
// loopback OTLP receiver -> logs/all -> batch -> the local store, so it would
// never survive the trip and the restart would be invisible in the product's own
// logs (it was, until 0.8.6 — only stderr had it).
func restartForConfig(logger *zap.Logger) error {
	// Best-effort flush of anything zap still buffers; the OTLP hop above is
	// what the caller's delay covers.
	_ = logger.Sync()

	os.Exit(exitCodeConfigApplied)

	return nil // unreachable; keeps the func signature honest for the test seam
}

// newConfigManager builds a manager from the process command line. args is
// os.Args[1:] in production; tests pass their own.
func newConfigManager(logger *zap.Logger, cfg *Config, args []string) *configManager {
	uris := parseConfigFlags(args)

	primary := cfg.ConfigFile
	if primary == "" {
		// First file: source wins. The systemd install passes the main config
		// first and overlays after it, so this is the file an operator means.
		for _, uri := range uris {
			if path, ok := uriPath(uri); ok {
				primary = path

				break
			}
		}
	}

	exe, err := os.Executable()
	if err != nil {
		logger.Warn("cannot determine collector executable; remote config writes will be refused",
			zap.Error(err))

		exe = ""
	}

	return &configManager{
		logger:       logger,
		uris:         uris,
		primary:      primary,
		writeEnabled: cfg.RemoteConfigWrite,
		executable:   exe,
		reloadFn:     func() error { return restartForConfig(logger) },
	}
}

// parseConfigFlags extracts the values of every `--config` / `-config` flag, in
// order. Both `--config X` and `--config=X` forms are accepted, matching the
// collector's own flag parsing.
func parseConfigFlags(args []string) []string {
	var uris []string

	for i := 0; i < len(args); i++ {
		arg := args[i]

		name, value, hasValue := strings.Cut(arg, "=")
		if name != "--config" && name != "-config" {
			continue
		}

		if hasValue {
			uris = append(uris, value)

			continue
		}

		// Separate value: `--config <uri>`.
		if i+1 < len(args) {
			uris = append(uris, args[i+1])
			i++
		}
	}

	return uris
}

// uriPath maps a `--config` URI to a filesystem path, reporting whether it is a
// readable/writable file source at all. A bare path (no scheme) is treated as a
// file, which is how the collector's default scheme behaves for absolute paths.
func uriPath(uri string) (string, bool) {
	if rest, ok := strings.CutPrefix(uri, fileScheme); ok {
		return rest, rest != ""
	}

	// Any other `scheme:` prefix belongs to a different confmap provider
	// (env:, yaml:, leansignal:, http:, …) and has no file behind it.
	if scheme, _, ok := strings.Cut(uri, ":"); ok && scheme != "" && !filepath.IsAbs(uri) {
		return "", false
	}

	return uri, uri != ""
}

// Snapshot reads every config source and reports it to lean-api.
func (m *configManager) Snapshot() *agentv1.ConfigSnapshot {
	snap := &agentv1.ConfigSnapshot{
		PrimaryPath:  m.primary,
		WriteEnabled: m.writeEnabled,
	}

	if len(m.uris) == 0 {
		snap.Error = "no --config sources found on the agent command line"

		return snap
	}

	for _, uri := range m.uris {
		path, isFile := uriPath(uri)
		if !isFile {
			snap.Files = append(snap.Files, &agentv1.ConfigFile{
				Path:  uri,
				Error: "not a file source; cannot be read or edited",
			})

			continue
		}

		file := &agentv1.ConfigFile{Path: path}

		content, err := readConfigFile(path)
		if err != nil {
			file.Error = err.Error()
			snap.Files = append(snap.Files, file)

			continue
		}

		file.Content = content
		file.Writable = m.writeEnabled && isWritable(path)

		snap.Files = append(snap.Files, file)
	}

	return snap
}

// readConfigFile reads a config file, refusing anything implausibly large.
func readConfigFile(path string) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}

	if info.IsDir() {
		return nil, fmt.Errorf("%s is a directory", path)
	}

	if info.Size() > maxConfigFileBytes {
		return nil, fmt.Errorf("config file is %d bytes, over the %d byte limit", info.Size(), maxConfigFileBytes)
	}

	return os.ReadFile(path) //nolint:gosec // path comes from this process's own --config flags
}

// isWritable reports whether the agent process could replace this file.
//
// What matters is the DIRECTORY, not the file's own mode: a write stages a temp
// file alongside the target and renames it into place, and rename needs write
// permission on the directory — it never opens the target for writing. Probing
// the file with O_WRONLY was too strict and produced false negatives wherever
// the config is owned by someone else but sits in a directory the agent owns:
// the Helm chart's writable mode seeds the volume with a root-run init
// container, leaving a root-owned 0644 config in a group-writable directory,
// which the agent can replace perfectly well.
//
// A read-only ConfigMap mount still reports false — the probe fails with EROFS.
func isWritable(path string) bool {
	probe, err := os.CreateTemp(filepath.Dir(path), ".leansignal-write-probe-*")
	if err != nil {
		return false
	}

	name := probe.Name()
	_ = probe.Close()
	_ = os.Remove(name)

	return true
}

// Apply validates a candidate config and, if it is sound, writes it into place.
// It returns a message describing the outcome; a non-nil error means nothing
// was written. reload reports whether the caller should now signal a reload.
func (m *configManager) Apply(ctx context.Context, path string, content []byte, skipReload bool) (msg string, reload bool, err error) {
	if !m.writeEnabled {
		return "", false, errors.New("remote config write is disabled on this agent (set remote_config_write: true on the leansignal_edge_controller extension)")
	}

	if path == "" {
		path = m.primary
	}

	if path == "" {
		return "", false, errors.New("no config file to write: the agent resolved no file: --config source")
	}

	// Only files this collector was actually started with may be written —
	// never an arbitrary path from the network.
	if !m.isKnownPath(path) {
		return "", false, fmt.Errorf("%s is not one of this agent's config files", path)
	}

	if len(content) == 0 {
		return "", false, errors.New("refusing to write an empty config")
	}

	if len(content) > maxConfigFileBytes {
		return "", false, fmt.Errorf("config is %d bytes, over the %d byte limit", len(content), maxConfigFileBytes)
	}

	var probe map[string]any
	if err := yaml.Unmarshal(content, &probe); err != nil {
		return "", false, fmt.Errorf("config is not valid YAML: %w", err)
	}

	// Stage the candidate beside the real file so the later rename is atomic
	// (same filesystem) and so `validate` sees it exactly as it will land.
	tmpPath, err := m.stage(path, content)
	if err != nil {
		return "", false, err
	}

	defer func() {
		// Only removes the staged file if the rename below never happened.
		if _, statErr := os.Stat(tmpPath); statErr == nil {
			_ = os.Remove(tmpPath)
		}
	}()

	if err := m.validate(ctx, path, tmpPath); err != nil {
		return "", false, err
	}

	if err := m.commit(path, tmpPath); err != nil {
		return "", false, err
	}

	m.logger.Info("collector config updated from the control plane",
		zap.String("path", path),
		zap.Int("bytes", len(content)),
		zap.String("backup", path+backupSuffix),
		zap.Bool("reload", !skipReload),
	)

	if skipReload {
		return fmt.Sprintf("config validated and written to %s (previous kept as %s); restart the agent to apply it",
			path, path+backupSuffix), false, nil
	}

	return fmt.Sprintf("config validated and written to %s (previous kept as %s); restarting to reload the config",
		path, path+backupSuffix), true, nil
}

// isKnownPath reports whether path is one of the file sources this collector
// was started with.
func (m *configManager) isKnownPath(path string) bool {
	for _, uri := range m.uris {
		if known, ok := uriPath(uri); ok && known == path {
			return true
		}
	}

	return false
}

// stage writes the candidate config to a temp file in the target's directory,
// inheriting the target's permissions.
func (m *configManager) stage(path string, content []byte) (string, error) {
	perm := os.FileMode(0o600)
	if info, err := os.Stat(path); err == nil {
		perm = info.Mode().Perm()
	}

	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return "", fmt.Errorf("cannot stage the new config next to %s: %w", path, err)
	}

	tmpPath := tmp.Name()

	cleanup := func(cause error) (string, error) {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)

		return "", cause
	}

	if _, err := tmp.Write(content); err != nil {
		return cleanup(fmt.Errorf("cannot write the staged config: %w", err))
	}

	if err := tmp.Sync(); err != nil {
		return cleanup(fmt.Errorf("cannot flush the staged config: %w", err))
	}

	if err := tmp.Chmod(perm); err != nil {
		return cleanup(fmt.Errorf("cannot set permissions on the staged config: %w", err))
	}

	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)

		return "", fmt.Errorf("cannot close the staged config: %w", err)
	}

	return tmpPath, nil
}

// validate runs the collector's own `validate` subcommand over the full config
// set with the candidate substituted for the file being replaced, so overlays
// and cross-file references are checked exactly as the collector will resolve
// them.
func (m *configManager) validate(ctx context.Context, path, candidate string) error {
	if m.executable == "" {
		return fmt.Errorf("%w: the collector executable could not be located, so the new config cannot be checked before it is applied", errValidationUnavailable)
	}

	args := make([]string, 0, 1+2*len(m.uris))
	args = append(args, "validate")

	for _, uri := range m.uris {
		known, ok := uriPath(uri)
		if ok && known == path {
			uri = fileScheme + candidate
		}

		args = append(args, "--config", uri)
	}

	ctx, cancel := context.WithTimeout(ctx, validateTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, m.executable, args...) //nolint:gosec // executable is this process's own binary
	out, err := cmd.CombinedOutput()

	if err == nil {
		return nil
	}

	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return fmt.Errorf("%w: validating the new config timed out after %s", errValidationUnavailable, validateTimeout)
	}

	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		// The validator never ran (binary missing, no exec permission, …). The
		// config may be perfect, but applying an unchecked config is what takes
		// the agent offline, so refuse.
		return fmt.Errorf("%w: %v", errValidationUnavailable, err)
	}

	return fmt.Errorf("the new config is not valid, so it was not applied:\n%s", strings.TrimSpace(sanitizeValidatorOutput(string(out), candidate, path)))
}

// sanitizeValidatorOutput rewrites the staging path back to the real one so the
// operator sees errors against the file they edited, and bounds the output.
func sanitizeValidatorOutput(out, candidate, path string) string {
	out = strings.ReplaceAll(out, candidate, path)

	const maxValidatorOutput = 8 << 10
	if len(out) > maxValidatorOutput {
		out = out[:maxValidatorOutput] + "\n… (truncated)"
	}

	return out
}

// commit backs up the current config and moves the validated candidate into
// place. The rename is atomic, so the collector never observes a partial file.
func (m *configManager) commit(path, candidate string) error {
	if current, err := os.ReadFile(path); err == nil { //nolint:gosec // path is one of this process's own --config files
		if err := os.WriteFile(path+backupSuffix, current, 0o600); err != nil {
			m.logger.Warn("could not back up the previous config; continuing",
				zap.String("path", path), zap.Error(err))
		}
	}

	if err := os.Rename(candidate, path); err != nil {
		return fmt.Errorf("cannot replace %s: %w", path, err)
	}

	// fsync the directory so the rename survives a crash right after it.
	if dir, err := os.Open(filepath.Dir(path)); err == nil {
		_ = dir.Sync()
		_ = dir.Close()
	}

	return nil
}

// Reload signals the running collector to re-read its config files.
func (m *configManager) Reload() error {
	return m.reloadFn()
}
