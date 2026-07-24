# Changelog

All notable changes to this project are documented here. The format is based on
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and this project follows
[Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.6.6] - 2026-07-24
### Added
- **One Tempo org per trace ingestion rule**, so deleting a rule actually purges
  its spans. Tempo has no selective delete — a whole org is the smallest thing
  that can be expired — so until now a deleted trace rule stopped collection but
  left everything it had already stored until the tenant-wide retention aged it
  out. Spans are now routed into `<tenant>__<filter-id>` orgs:
  - `DemandSet.trace_demands` pairs each trace selector with the id of the
    filter demanding it. `trace_selectors` (field 4) stays populated, so an
    agent that predates this filters identically and keeps the tenant-wide org.
  - `leansignal_trace_demand_filter` gains a routed path: instead of keep/drop
    it emits one copy of each demanded `ResourceSpans` **per matching rule**,
    stamped with that rule's id. A resource matched by three rules ships three
    times — deliberate duplication, and the price of per-rule deletion, since
    each org must hold its own copy. Fail-closed is preserved, and with no
    routes the legacy path runs unchanged.
  - `leansignal_trace_router` (new exporter) groups a batch by that stamp,
    strips it, and POSTs each group to `<endpoint>/v1/traces/r/<filter-id>`;
    lean-api's forward-auth turns the path into the org. The stock `otlphttp`
    exporter cannot do this — its endpoint is fixed at config time — so only the
    push is custom; queueing, retry and timeout stay with `exporterhelper`.
    Unstamped spans go to `/v1/traces`, so an agent upgrade alone never moves
    anyone's data.
  The agent never names the org: it chooses the push path, and lean-api derives
  the org after validating the rule belongs to that agent's tenant. The agent
  runs in the customer's network, so letting it name the org would let it write
  anywhere.

### Changed
- The `traces/filtered` pipeline exports through `leansignal_trace_router`
  instead of `otlphttp/tempo_tenant` (Helm chart, docker-compose, cloud and
  example configs). `agent-config.local.yaml` keeps `otlphttp/tempo_tenant`:
  local dev has no ingress to forward-auth the per-rule path.

### Requires
- lean-api with per-rule trace orgs (`agent-auth` minting
  `<tenant>__<filter-id>`, the purge worker expiring the org, and the dptempo
  proxy querying the union of live orgs). Against an older lean-api the per-rule
  paths forward-auth into the tenant org — i.e. today's behaviour.

## [0.6.5] - 2026-07-24
### Added
- **Pause-on-limit backoff for the tenant ingest exporters.** A new
  `leansignal_ingest_backoff` extension (`components/ingestbackoff`) plugs into
  the `auth` slot of `prometheusremotewrite/dataplane`, `otlphttp/loki_tenant`
  and `otlphttp/tempo_tenant` (one instance per signal). When the ingest edge
  rejects a push with **403** — LeanSignal's "ingest limit exceeded" answer
  (storage ceiling or monthly ingest budget, enforced by lean-api's
  forward-auth) — that signal's pushes are suppressed **locally** (batches
  dropped as permanent errors, zero network traffic, no retry-queue growth) and
  exactly ONE probe goes out per `retry_interval` (default `1m`); a probe
  success resumes pushing immediately. Local-store fidelity is unaffected.
  State transitions are logged once (`pausing pushes` / `pushes resumed`).
- **Local-store self-monitoring.** The agent scrapes its co-located stores' own
  `/metrics` (avm `vm_*`, aloki `loki_*`, atempo `tempo_*`) into the metrics
  pipeline — job names `leansignal-avm` / `leansignal-aloki` /
  `leansignal-atempo` — so agent-stack health (local disk usage, window
  pressure, ingest errors) is demandable like any other metric. Central mode
  only; Helm toggle `localStores.scrape.enabled` (default on), host config gains
  the equivalent `prometheus/localstores` receiver.

## [0.6.4] - 2026-07-22
### Added
- **Startup region resolve — the agent derives every backend host from its tenant
  slug.** A new `leansignal:` confmap provider (`components/resolveprovider`),
  compiled into the collector binary so it works under every install method
  (systemd/docker/k8s/manual), resolves `${leansignal:...}` config references. On
  the first lookup it calls control-center `GET /resolve_tenant?tenant=<slug>`
  **once** (memoized), recovers the region from the returned `api_url`
  (`<slug>-api.<region>`), and derives `grpc` / `dataplane` / `loki` / `tempo` —
  the per-signal ingest hosts `<slug>-{metrics,logs,traces}-ingest.<region>`.
  `LEANSIGNAL_DOMAIN` pins the region and skips the lookup; each
  `LEANSIGNAL_*_ENDPOINT` pins one host verbatim (skips resolution for it).

### Changed
- **Backend hosts are now derived, not configured.** The cloud/example configs,
  the Helm chart (`configmap`/`deployment`/`values`/`NOTES`/`_helpers`),
  `install.sh`, `install.ps1`, and the macOS plist reference the backend hosts via
  `${leansignal:...}`; `--tenant` / `leansignal.tenant` (the slug) is the only
  required host input, with endpoint flags/values kept as optional per-host pins.
- **Ingest hosts are per-signal.** Moved from a single `<slug>-ingest` origin to
  `<slug>-metrics-ingest` / `<slug>-logs-ingest` / `<slug>-traces-ingest`
  (matching the control-center `SetAllocated` + lean-infra tenant-template rename).

## [0.6.3] - 2026-07-21
### Changed
- **Helm chart bundles all three local stores.** The k8s chart now deploys the
  co-located Loki (`aloki`) and Tempo (`atempo`) as their **own** single-replica
  Deployments + ClusterIP Services (the same topology as the bundled
  VictoriaMetrics `avm`), reached in-cluster over service DNS — so a plain
  `helm install` brings up a working three-signal agent with no extra wiring.
  New `localLoki.deploy` / `localTempo.deploy` toggles (default on; set a
  `writeEndpoint` to point at your own store instead), each with `image`,
  `service`, `persistence` (emptyDir by default), and `resources` blocks. The
  bundled VictoriaMetrics subchart is now **enabled by default**. The local
  Loki/Tempo query endpoints are derived from their services automatically.
- `otlphttp/loki_local` now retries on failure, matching `tempo_local` (avoids
  dropping records during a local-store startup race).

## [0.6.0] - 2026-07-16
### Added
- **Demand-driven logs (Loki).** A new `logs/all` → `logs/filtered` pipeline pair
  mirrors the metrics fan-out: the agent writes every log record to a co-located
  Loki (~1h window) and forwards **only** the demanded LogQL streams to the tenant
  Loki. New `leansignal_log_demand_filter` processor (`components/logdemandfilter`)
  drops any `ResourceLogs` group whose computed Loki stream labels match no demanded
  selector — fail-closed (an empty / not-yet-received demand list forwards zero log
  records). The `loki` push receiver is enabled so promtail/Alloy-style shippers can
  push natively; tenant delivery uses `otlphttp` with the agent key as a bearer,
  authenticated at the ingest ingress. A co-located Loki is installed alongside the
  agent in every deploy form (host installer `--no-loki`/`--loki-version`, docker
  compose, Helm `localLoki`/`logs` values), pinned via `LOKI_VERSION`.
- **Demand-driven traces (Tempo).** A `traces/all` → `traces/filtered` pipeline pair,
  the traces twin of the logs path: everything to a co-located Tempo (~1h window),
  only the demanded resources' spans forwarded to the tenant Tempo. New
  `leansignal_trace_demand_filter` processor (`components/tracedemandfilter`) drops
  any `ResourceSpans` group whose resource attributes match no demanded selector —
  resource-granular (whole services, never individual spans), fail-closed. The local
  Tempo's OTLP receiver binds `127.0.0.1:4328` (the collector owns 4317/4318); query
  API on `127.0.0.1:3200`. Installed in all deploy forms (host installer
  `--no-tempo`/`--tempo-version`, docker compose, Helm `localTempo`/`traces` values),
  pinned via `TEMPO_VERSION`.
- **Edit-mode query tunnel for logs and traces.** `QueryRequest.target`
  (`QUERY_TARGET_VM` | `QUERY_TARGET_LOKI` | `QUERY_TARGET_TEMPO`) selects which
  co-located store the edge controller runs a lean-api-proxied, read-only,
  allow-listed query against; the demand set (and the `DemandSet.hash` agents echo
  back) now covers metric names, LogQL stream selectors, and trace resource selectors.

### Notes
- **Proto is additive and wire-compatible.** Old servers/agents ignore the new
  `DemandSet.log_selectors` / `trace_selectors` fields and the new `QueryTarget`
  values, so mixed fleets keep working (metrics-only for older peers).

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
