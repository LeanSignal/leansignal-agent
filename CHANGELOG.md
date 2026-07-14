# Changelog

All notable changes to this project are documented here. The format is based on
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and this project follows
[Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.5.1] - 2026-07-14
### Fixed
- **Identity-label collision on the `mode` / `agent_name` labels.** The `resource`
  processor stamped the source-identity resource attributes as bare `mode` and
  `agent.name`, which `resource_to_telemetry_conversion` promoted to the generic
  labels `mode` / `agent_name`. `mode` collides with metrics that carry a native
  `mode` label — most importantly `node_cpu_seconds_total{mode="idle|user|system|…"}`
  — and because the agent stamped every series, the collision **overwrote** those
  native values (e.g. all `node_cpu_seconds_total` series collapsed to
  `mode="central"`, destroying the per-mode CPU breakdown). The attributes are now
  namespaced as `leansignal.mode` and `leansignal.agent.name`, promoted to the
  `leansignal_mode` / `leansignal_agent_name` labels, so they no longer clash with
  any collected metric.

  **Breaking (labels):** dashboards/queries that referenced the `mode` or
  `agent_name` labels must migrate to `leansignal_mode` / `leansignal_agent_name`.

## [0.5.0] - 2026-07-12
### Added
Report the agent version to the LeanAPI backend

## [0.4.0] - 2026-07-08
### Added
- **Agent self-telemetry, on by default.** Every config now exposes the
  collector's internal metrics on `127.0.0.1:8888` (`service.telemetry.metrics`,
  level `detailed`), scrapes them with a `prometheus/internal` receiver, and
  routes them through the `metrics/all` pipeline — so `otelcol_*` health metrics
  (throughput, exporter queue depth, send failures, remote-write latency, memory)
  land in the local VM, are indexed, and are demandable like any other metric. The
  `leansignal_edge_controller` extension also emits its **own** instruments:
  `leansignal_edgecontroller_{known,discovered,demand}_timeseries_cache_size`,
  `_pending_backend_updates`, a `_connection_up` gauge, and
  `_connection_attempts_total` / `_connection_established_total` counters. New
  reference: [`docs/own-telemetry.md`](docs/own-telemetry.md).
- **Per-agent identity labels.** Every metric now carries `agent_name`,
  `host_name`, and `os_type` labels (via the `resourcedetection` + `resource`
  processors, promoted with `resource_to_telemetry_conversion`), so series from
  different hosts stay distinct in the shared central store. The name comes from a
  new **required** `--agent-name` install flag (`LEANSIGNAL_AGENT_NAME`; Helm
  `leansignal.agentName`, defaulting to the Kubernetes node name).
- **Edge / central agent modes.** A new **edge** mode installs a lightweight OTLP
  **forwarder** — host metrics + OTLP from local apps + self-telemetry, shipped as
  OTLP to a central agent — with no local VM, tracker, demand filter, or control
  channel. Selected with `--central-url HOST:PORT` (or `CENTRAL_AGENT_GRPC_URL`;
  Helm `leansignal.centralAgentGrpcUrl` / `leansignal.mode=edge`). Metrics carry a
  `mode` = `central`|`edge` label, and central agents **preserve** the identity
  that edge agents stamp on forwarded data.
- **Helm: bring-your-own config** via `config.existingConfigMap` — point the chart
  at a ConfigMap you manage and it renders none of its own, so the config survives
  `helm upgrade` and can be edited in-cluster.

### Changed
- **`--agent-name` is now required** for all host and Helm installs.
- A **central** agent's OTLP receiver now binds `0.0.0.0` (all interfaces) and is
  unauthenticated, so edge agents can forward to it — keep central agents on a
  trusted/internal network (or firewall `:4317`/`:4318`).

## [0.3.0] - 2026-07-07
### Added
- **Agent diagnosis command** (`get_diagnosis`), triggered by an admin through
  lean-api's `GET /api/v1/agent/diagnosis/{id}`. The agent logs a summary of the
  current demand set — which demanded metric names were **matched** against the
  series it actually collects and which were **not found** — and writes the full
  contents of its three timeseries caches as human-readable YAML files:
  `KnownTimeseriesCache.yaml`, `DiscoveredTimeseriesCache.yaml`, and
  `DemandTimeseriesCache.yaml`. Output goes to the new `diagnostics_dir`
  edge-controller setting (default `/tmp/leansignal-agent`); the absolute path is
  logged on each run.

### Fixed
- **Metric name → timeseries name conversion.** The metrics tracker and the
  demand filter built Prometheus series names without the OpenTelemetry **unit**
  suffix (e.g. `system_cpu_time_total` instead of `system_cpu_time_seconds_total`,
  `system_memory_usage` instead of `system_memory_usage_bytes`). Because those
  names didn't match the ones written to VictoriaMetrics — and therefore the
  demand set derived from dashboards/alerts — every unit-bearing metric was
  dropped by the filter and never reached the dataplane (only unitless series
  such as load averages got through). Both now build names through the same
  `github.com/prometheus/otlptranslator` module the Prometheus remote-write
  exporter uses, so agent-side names match exactly what is stored, by
  construction.
- demand filter resync with backend

## [0.2.0] - 2026-07-01

### Added
- **In-place upgrade tooling** for host installs: `scripts/install/upgrade.sh`
  (Linux/macOS) and `scripts/install/upgrade.ps1` (Windows). By default it upgrades
  only the agent binary — VictoriaMetrics keeps running and its on-disk data is
  never touched. `--with-vm` also upgrades VictoriaMetrics, taking an **enforced
  pre-upgrade snapshot** (aborts if it can't be confirmed; `--skip-snapshot` to
  override) and swapping against the same data path. Both paths verify the release
  checksum, health-check the service, and **roll back automatically** on failure.
- Releases now publish a **`VERSIONS.txt`** manifest (agent + bundled
  VictoriaMetrics versions) so the upgrader can resolve which VM version to install.
- **Upgrade documentation**: [`docs/upgrading.md`](docs/upgrading.md) plus
  per-platform upgrade sections in the README and each install guide.

### Fixed
- Windows installer (`install.ps1`) checksum verification parsed the wrong token
  and silently skipped the integrity check; it now reads the matched line correctly.

## [0.1.0] - 2026-07-01

### Added
- Initial public, Apache-2.0 release of the LeanSignal Agent.
- Custom OpenTelemetry Collector distribution (OCB) with three first-party
  components: `leansignalmetrics_tracker`, `leansignal_demand_filter`, and the
  `leansignal_edge_controller` extension.
- Persistent, outbound **gRPC control channel** (`AgentControl.Connect`): the
  agent dials out and one stream carries the metric index up, the demand list
  down, and edit-mode queries both ways — no inbound access to the agent needed.
- **Edit-mode query tunnel**: the LeanSignal UI reads the agent's private local
  store over the control stream (read-only, allow-listed).
- Co-located VictoriaMetrics for full local fidelity; demand-filtered forwarding
  to a central dataplane (via `vmauth` in production, authenticated by the agent key).
- Tenant-based install — provide only your **agent key + tenant**; the gRPC and
  ingest hosts are derived.
- Helm chart, host installers (Linux/macOS/Windows), and docker-compose trial.
- GitHub Actions CI + goreleaser release pipeline (cross-platform binaries,
  multi-arch images, VictoriaMetrics mirroring + combined bundles).

[Unreleased]: https://github.com/LeanSignal/leansignal-agent/compare/v0.4.0...HEAD
[0.4.0]: https://github.com/LeanSignal/leansignal-agent/compare/v0.3.0...v0.4.0
[0.3.0]: https://github.com/LeanSignal/leansignal-agent/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/LeanSignal/leansignal-agent/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/LeanSignal/leansignal-agent/releases/tag/v0.1.0
