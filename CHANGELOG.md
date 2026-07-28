# Changelog

All notable changes to this project are documented here. The format is based on
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and this project follows
[Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.8.6] - 2026-07-28
### Fixed
- **The restart that applies a config is now visible in the agent's own logs.**
  0.8.5 logged `restarting to reload the config` one instruction before
  `os.Exit`, so the line never survived the trip the agent's logs actually take —
  `service.telemetry` → the loopback OTLP receiver → `logs/all` → batch → the
  local store. It reached stderr (`kubectl logs`, journald) and nothing else, so
  an operator who saved a config in the app and then looked at that agent's logs
  *in the app* saw no sign of the restart at all.

  The announcement now happens **before** the wait rather than after it, giving
  it the whole delay to make that trip, and `reloadDelay` goes 2s → 4s because
  the path is two batched hops (the telemetry log processor, then the `logs/all`
  batch processor, ~1s each) and 2s sat exactly on the boundary. Applying a
  config therefore takes ~4s instead of ~2s — still a fraction of the ~20s the
  pre-0.8.5 in-place reload cost.

  The line carries `in` (how long until the exit) and `exit_code` (75), and if
  the agent happens to be shutting down for another reason during that window it
  now says so instead of leaving the announcement as the last word.

## [0.8.5] - 2026-07-28
### Fixed
- **Applying a config no longer hangs for ~20 seconds and then looks like a
  crash.** Saving a config asked the collector to reload in place via `SIGHUP`.
  That can never succeed on a self-monitoring agent: `service.telemetry` exports
  the agent's own logs over OTLP to its **own loopback receiver**, and the reload
  tears that receiver down early. Flushing the logger then retried against a dead
  endpoint for ~20s, failed, and the collector treats a failed reload as fatal —
  so it exited anyway, with status **1**, indistinguishable from a genuine crash
  in `systemctl status` / `kubectl describe`. The config did apply, but only via
  that ugly path, and an operator running `Restart=no` was left with a dead agent.

  Applying a config is now an explicit restart. The agent logs
  **`restarting to reload the config`** and exits **75** — non-zero so the shipped
  systemd unit's `Restart=on-failure` picks it up, and deliberately not 1 so an
  intentional config restart is greppable and distinct from a crash. It is back
  in a couple of seconds.

  Consequences worth knowing:
  - **Windows can apply a config for the first time.** It has no `SIGHUP`, so
    until now the config was written and only took effect on the next manual
    service restart. Every platform now behaves identically.
  - Whatever the pipelines still hold at that moment is lost — at most a second,
    since the batch processors flush on a 1s timer and the agent waits 2s before
    exiting (the same pause that gets the result back to the UI first). The
    co-located stores keep everything they already received.
  - Docker users need a restart policy (`--restart unless-stopped`); systemd and
    Kubernetes already restart it.

  The docs said "reloads in place, no restart" throughout. They were wrong, and
  are corrected.

## [0.8.4] - 2026-07-28
### Fixed
- **The chart now installs on a cluster enforcing the `restricted` Pod Security
  Standard.** It previously could not: the collector container declared no
  security context at all, so the API server refused to create the pod —
  `allowPrivilegeEscalation != false`, `unrestricted capabilities`,
  `runAsNonRoot != true`, and no `seccompProfile`. **Zero pods were admitted**,
  and `helm install --wait` simply timed out with the reason buried in a
  ReplicaSet event. The bundled Loki and Tempo (which set only
  `runAsUser`/`runAsGroup`/`fsGroup`) and the `victoria-metrics-single` subchart
  had the same gap.

  All four workloads now ship a `restricted`-compliant baseline, and the
  collector's is exposed as `podSecurityContext` / `securityContext` values so it
  can be overridden or emptied. Nothing changes on a permissive cluster — the
  agent image is distroless `:nonroot` and binds no privileged port, so it was
  already running this way; it just never said so.

- **`Chart.yaml`'s `version`/`appVersion` are no longer stale placeholders.**
  They sat at `0.6.3`/`0.6.2` because the release workflow stamps both from the
  git tag, so nobody noticed. Installing from a git checkout therefore pulled the
  **0.6.2** image, which predates the `${leansignal:...}` resolve provider
  (0.6.4+) — the collector crash-looped on `scheme "leansignal" is not supported`.
  Installs of the published chart were never affected.

### Changed
- **The config-seed init container runs as the agent's own uid, not root.** With
  `config.writable`, `fsGroup` already makes the volume group-writable, so the
  seed never needed to be root. It now declares `runAsNonRoot` + `runAsUser`
  (and drops all capabilities), which keeps it admissible under `restricted` and
  leaves the seeded config **owned by the agent** rather than by root.

  That also removes the failure class behind 0.8.3: a root-owned config in a
  group-writable volume was what made the old writability probe answer wrongly.
  The `chmod g+w` workaround added in 0.8.2 is gone with it.

- `config.seedImage` and `config.fsGroup` documentation now calls out the two
  environment-specific things worth checking before enabling `config.writable`:
  the seed image is the chart's **only** image outside `ghcr.io` (override it for
  mirrored/air-gapped installs), and the storage class must honour `fsGroup` —
  a few NFS/CIFS and CSI drivers do not, and there the config is correctly
  reported as non-writable.

## [0.8.3] - 2026-07-28
### Fixed
- **A config the agent does not own is no longer reported as read-only.** The
  writability probe opened the config file with `O_WRONLY`, which is stricter
  than what writing it actually requires: a config is replaced by staging a temp
  file alongside it and renaming over the target, and `rename` needs write
  permission on the **directory** — it never opens the target for writing.

  This bit the chart's new `config.writable` mode immediately. The seed init
  container runs as root, so it leaves a root-owned `0644` config in a volume
  the `fsGroup` makes group-writable — a file the agent can replace perfectly
  well, but cannot open for writing. The app therefore showed the editor
  read-only on a deployment that was explicitly configured to be editable.

  The probe now tests the directory, which is what the write path uses. A
  read-only ConfigMap mount still reports non-writable (the probe fails with
  `EROFS`, same as before).

### Changed
- The chart's seed init container also sets the group write bit on what it
  copies. Not required after the fix above — the rename path never needed it —
  but it keeps the volume's permissions unsurprising to anyone reading them.

## [0.8.2] - 2026-07-28
### Added
- **`config.writable` — edit the collector config from the LeanSignal app on
  Kubernetes.** Kubernetes mounts a ConfigMap volume **read-only** and no flag
  changes that, so a chart-installed agent reported its config as non-writable
  and the app's Configuration tab opened read-only (reading always worked). Set

  ```yaml
  config:
    writable: true
  ```

  and the chart provisions a small PVC instead, seeded **once** from the
  ConfigMap by an init container on first start. From then on the config is
  editable from the app, from `leanctl`, or through the `agent_config_update`
  MCP tool — validated before it is written, kept as `config.yaml.bak`, and
  applied by the same in-place reload as a host install. The agent needed no
  change: it probes the filesystem, so it reports the file as writable by
  itself.

  Three details the mode carries with it:
  - the Deployment switches to the **`Recreate`** strategy, because a
    ReadWriteOnce claim cannot be attached to the outgoing and incoming pod at
    once — a rolling update would deadlock;
  - a **`fsGroup`** (65532, matching the distroless `:nonroot` image) is applied
    so the agent owns the provisioned volume and can write to it;
  - the `checksum/config` pod annotation is **dropped**, since the ConfigMap is
    then only a first-boot seed and re-rolling would imply an update the running
    agent does not actually pick up.

  **The trade-off, stated plainly:** after the first boot the volume is the
  source of truth, so config changes made through `helm upgrade` no longer reach
  the agent. Delete `config.yaml` from the volume (or the PVC) and restart to
  hand control back to the chart; `helm uninstall` deletes the PVC and any edits
  with it.

  Default is `false` — unchanged behaviour, and the right setting under GitOps
  (ArgoCD, Flux), where the repository should stay the source of truth and an
  edit made in the app would be reverted by the next sync anyway.

## [0.8.1] - 2026-07-27
### Added
- **The agent's collector config can be read and edited from the LeanSignal
  app.** Open **Agents**, click the agent's name, and pick the **Configuration**
  tab: it shows the `--config` files this collector was started with, read live
  off the agent host over the existing gRPC control stream, and lets a tenant
  admin edit them. No SSH, and no server-side copy of the config — what is shown
  is what is on disk. Values are returned verbatim, so `${env:...}` and
  `${leansignal:...}` references stay unexpanded and secrets are never resolved
  onto the wire.

  Saving is guarded, because a config the collector cannot load takes the agent
  down and only SSH gets it back:
  1. the YAML must parse;
  2. the agent runs its own `validate` subcommand over the **merged** config —
     every `--config` source, with the candidate substituted for the file being
     replaced — so overlays and cross-file references are checked exactly as the
     collector will resolve them. A config that fails is **never written**, and
     the validator's output comes back to the UI verbatim. A candidate that
     cannot be validated at all is refused too: applying an unchecked config is
     the failure mode being defended against.
  3. the file is replaced by an atomic rename, with the previous contents kept
     as `<path>.bak`;
  4. the agent sends itself `SIGHUP`, which the collector answers by reloading
     in place. The agent disconnects and reconnects within a few seconds — no
     process restart, no supervisor, and identical behaviour under systemd,
     docker and Kubernetes. The local stores are untouched, so no data is lost.

  New control-plane messages (wire-compatible additions): `GetConfig` →
  `ConfigSnapshot` / `ConfigFile`, and `UpdateConfig` gains `path` and
  `skip_reload`. The commands are driven by lean-api's
  `GET`/`PUT /api/v1/agent/config/{id}`, so a backend without those endpoints
  simply never sends them — this release is safe to roll out ahead of it.

- **`remote_config_write` on the `leansignal_edge_controller` extension**
  (default `true`) is the host-side kill switch. Reading the config in the UI
  stays available when it is off; only writes are refused. Worth setting to
  `false` where the LeanSignal tenant admins and the host's operators are not
  the same people — an OTEL config can be pointed at arbitrary files on the host
  (a `filelog` receiver) and export them elsewhere, so a remote config write is
  effectively file-read on that host. A config delivered read-only (a Kubernetes
  ConfigMap mount) cannot be written whatever this says, and is reported to the
  UI as non-writable so the editor opens read-only with the reason.

- **`config_file`** on the same extension pins which file a write targets when
  the request names none. It defaults to the first `--config file:...` on the
  command line, which is the main config in every layout LeanSignal ships.

### Changed
- `UpdateConfig` is no longer a stub. It previously logged the command and
  answered `success: true` without applying anything.

### Notes
- Windows has no `SIGHUP`, so there the config is validated and written but
  takes effect on the next service restart; the UI says so in the result.
- Only `file:` config sources can be read or edited. Other confmap schemes
  (`env:`, `yaml:`, `leansignal:`, `http:`) are reported to the UI as
  non-editable rather than being silently omitted, so the picture is never
  quietly partial.

## [0.8.0] - 2026-07-26
### Added
- **Logging is now enabled by default for the agent's own components.** The
  co-located VictoriaMetrics, Loki and Tempo feed the agent's logs pipeline and
  land in the local log store as `leansignal-victoria-metrics`,
  `leansignal-loki` and `leansignal-tempo` — the agent itself already arrived as
  `leansignal-agent`. As with all telemetry, they stay local until demanded.
  Linux reads journald, macOS tails the daemons' log files; Windows and
  Kubernetes are unchanged. The installer wires this through a
  `localstore-logs.yaml` overlay beside `config.yaml`, with offsets persisted so
  restarts lose nothing; remove the overlay and its `--config` argument to opt
  out.

### Changed
- The agent's `ExecStart` / `ProgramArguments` pass `--config` with an explicit
  `file:` scheme and load the store-log overlay alongside the main config.

### Fixed
- **The docker-compose and cloud dev configs now use `leansignal_trace_router`.**
  The shipped `agent-config.example.yaml` moved to the per-rule trace router in
  0.6.6, but two configs were left behind:
  - `deploy/docker/agent-config.yaml` still exported tenant spans through
    `otlphttp/tempo_tenant`, so every trace landed in the one tenant-wide Tempo
    org. Deleting a trace ingestion rule could not purge its spans, because
    per-org retention is the only granularity Tempo can delete at.
  - `config/agent-config.cloud.yaml` already named the router but configured it
    with `auth`, `retry_on_failure` and `sending_queue` — exporterhelper keys the
    router's own config does not declare — and carried literal escaped quotes in
    its `endpoint`. The collector rejects unknown keys, so `make cloud-run` could
    not start.
  Both now match the example config: `endpoint` + `headers` + `timeout: 30s`.
- The docker config's `prometheusremotewrite/dataplane` exporter was missing the
  agent-key `Authorization` header that the metrics ingress forward-auths.

## [0.7.0] - 2026-07-25
### Added
- **The co-located log and trace stores now install on macOS.** Loki and Tempo
  were Linux-only — `install.sh` detected darwin and skipped them, because there
  was no launchd plist for either — so a Mac kept metrics locally but had nowhere
  to put logs or spans. Demanded logs and traces still reached the tenant (the
  collector config is identical on every platform, tenant exporters included);
  what was missing was the **local** full-fidelity window, and with it the ability
  to explore a signal before demanding it. All three stores now install on both
  platforms:
  - New launchd daemons `com.leansignal.loki` and `com.leansignal.tempo` beside
    the existing `com.leansignal.victoria-metrics`, each independent of the agent
    and of each other. Binaries in `/usr/local/bin/`, configs in
    `/usr/local/etc/leansignal-agent/{loki,tempo}.yaml`, data under
    `/usr/local/var/leansignal-agent/{loki,tempo}`, logs at
    `/usr/local/var/log/leansignal-agent/{loki,tempo}.log`.
  - Grafana publishes darwin builds of both, so the pinned `LOKI_VERSION` /
    `TEMPO_VERSION` are used unchanged; the download URL, archive member and
    service-template names are now derived from the detected platform instead of
    being hardcoded to `linux`.
  - `--no-loki` / `--no-tempo` now do something on macOS, and `uninstall.sh`
    removes the two new daemons.
  The local windows match Linux: Loki ~1h (exact, `max_query_lookback`), Tempo
  ~1h (approximate, compaction-driven). Nothing about the agent or its config
  changes — it has always been writing to `127.0.0.1:3100` and `127.0.0.1:4328`;
  on macOS there was simply nothing listening. **Windows is unchanged** — still
  metrics-only locally, with logs and traces received and forwarded but not
  stored.

### Changed
- `loki.yaml` and `tempo.yaml` now carry a data-directory placeholder that the
  installer substitutes with the platform path, so one template serves both
  platforms. **Every non-comment line of the rendered Linux config is identical
  to 0.6.7's** — no behaviour change on Linux.
- `docs/install-macos.md` documents macOS as a full-feature platform and gains a
  verification section covering all three signals (including why `allowed=0` in
  the demand-filter log lines is correct on a fresh agent, and why Tempo's
  `/api/search/tags` is empty until its first blocks flush).
- Every install guide now documents, per service, how to **read its logs**,
  **edit its config** and **restart just it**. Windows gains the log story it
  never had: `sc.exe` does not capture the collector's stderr, so there is no log
  file — the guide now covers the Event Log, running the binary in the foreground
  with the service's environment loaded, and reading the agent's own logs in
  LeanSignal once `{service_name="leansignal-agent"}` is demanded.

### Fixed
- **`docs/install-kubernetes.md` claimed the chart bundles no Loki or Tempo.** It
  has for some time: `localLoki.deploy` and `localTempo.deploy` both default to
  `true`, so central-mode installs get a Loki and a Tempo Deployment alongside the
  collector. The guide told users to go run their own and, in the "what gets
  created" list, to expect neither.
- **Every `kubectl` command in that guide named the wrong resource.** The chart's
  `fullname` helper always prefixes the release name rather than deduping it, so
  the documented install produces `leansignal-agent-leansignal-agent…`, not
  `leansignal-agent…`. Each `kubectl logs` / `rollout restart` / `get cm` as
  written failed with `NotFound`, and the advertised in-cluster OTLP address
  (`leansignal-agent.leansignal.svc:4317`) resolved to nothing.
- **`make lint` was red on `main`.** Two `tracedemand` imports had been placed in
  the stdlib group (goimports), and once that was fixed staticcheck surfaced four
  SA5011 findings behind it: two tests nil-checked a constructor that returns
  `&T{}` and can never be nil, making the following field access look like a
  possible nil dereference. Imports regrouped, the dead nil-checks dropped.

## [0.6.8] - 2026-07-24
### Fixed
- **`leansignal_trace_router` drops 4xx-rejected pushes instead of retrying
  them.** lean-api answers 403 to a push naming a deleted rule (deliberately —
  routing it to the tenant org would store undemanded spans in the one org that
  can never be expired). The router treated every non-2xx as retryable, so until
  the next demand set reached the agent every batch carrying the deleted rule
  became a retry storm (~13 rejected pushes/min per stale rule observed live).
  4xx (except 408/429) now wraps in `consumererror.NewPermanent`, so
  exporterhelper drops the batch; 408/429/5xx stay retryable. Pairs with
  lean-api's miss-refresh in agent-auth (a just-created rule forces one fresh
  rule-set lookup before a 403), which closes the creation race that permanent
  drops would otherwise expose.

## [0.6.7] - 2026-07-24
### Fixed
- **The trace ingestion rule now travels in a header, not the push path.** 0.6.6
  posted each rule's spans to `/v1/traces/r/<filter-id>`, which needs its own
  Ingress with a rewrite — and control-center renames only the ingresses it knows
  about when a pool tenant is allocated. On every allocated tenant that Ingress
  kept the pool hostname while the agent pushed to the allocated one, so the
  per-rule path fell through to the plain `/v1/traces` Prefix rule un-rewritten
  and Tempo 404'd every batch: traces would have stopped reaching the tenant
  store as soon as an allocated tenant upgraded. `leansignal_trace_router` now
  posts to the plain `/v1/traces` with `X-Lean-Trace-Rule: <filter-id>`, which
  rides the existing ingress — nothing new to route, nothing to rename. Requires
  lean-api reading that header (it also still accepts the 0.6.6 path, so agents
  can roll in any order).

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
