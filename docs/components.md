# Custom components

All first-party code lives under [`components/`](../components). The collector is
otherwise standard OpenTelemetry Collector Contrib.

## `metricsindex` (library)

Shared types and an in-process pub/sub bus that decouples the tracker (publisher)
from the edge controller (subscriber):

- `HashKey` — an xxh3-128 fingerprint of a timeseries.
- `TimeseriesEntry` / `TimeseriesBatch` — metric name + sorted labels + sample count.
- `RegisterTimeseriesReceiver` / `BroadcastTimeseriesBatch` — the process-global bus.

> One tracker and one edge controller are expected per process. The bus is a
> process singleton, not keyed per pipeline.

## `leansignalmetrics_tracker` (processor)

Pass-through processor (`MutatesData: false`). For every batch it:

1. Expands each OTLP metric into the Prometheus series name(s) it would produce
   (counters get `_total`, histograms explode into `_bucket`/`_sum`/`_count`,
   summaries into base + `quantile`/`_sum`/`_count`, etc.).
2. Fingerprints each series and builds a per-call batch.
3. Broadcasts the batch over the in-process bus to the edge controller.

`ConsumeMetrics` is invoked **concurrently** by the receivers feeding the
pipeline; the per-call batch keeps it free of shared mutable state (regression
guard: `TestConsumeMetricsConcurrent`, run with `-race`).

Config:

```yaml
leansignalmetrics_tracker:
  log_metrics: false   # log first-seen metric names
  log_series: false    # log first-seen series fingerprints
```

## `leansignal_demand_filter` (processor)

Drops every metric whose Prometheus name is **not** on the current demand list,
which it reads live from the edge controller on each batch. An empty / not-yet-
received list blocks everything (fail-closed). The OTLP→Prometheus naming logic
is intentionally mirrored from the tracker so demand matching uses identical
names — **change both together**.

```yaml
leansignal_demand_filter:
  log_filtered: false   # debug-log each dropped metric
```

## `leansignal_log_demand_filter` (processor)

Drops every log record whose computed **Loki stream labels** match **none** of
the demanded LogQL stream selectors, which it reads live from the edge
controller on each batch. It works at whole-resource granularity — a
`ResourceLogs` group is kept or dropped as a unit. An empty / not-yet-received
demand list blocks everything (fail-closed). It replicates Loki's OTLP
resource-attribute promotion so the stream labels it matches on are identical to
the ones the local and tenant Loki build — if that promotion logic changes,
change it here too.

```yaml
leansignal_log_demand_filter:
  log_filtered: false   # debug-log each dropped ResourceLogs group
```

## `leansignal_trace_demand_filter` (processor)

Drops every span whose **resource attributes** match **none** of the demanded
trace resource selectors, read live from the edge controller on each batch. It
is **resource-granular by design** — a whole service is demanded or not; there
is no per-span selection. An empty / not-yet-received demand list blocks
everything (fail-closed). The selector grammar mirrors the log filter's, widened
to dotted `resource.*` attribute keys.

```yaml
leansignal_trace_demand_filter:
  log_filtered: false   # debug-log each dropped ResourceSpans group
```

## `leansignal_edge_controller` (extension)

Maintains the persistent gRPC stream to the LeanSignal API and three thread-safe
caches:

- **known** — every series seen, with an 8-hour ring buffer of per-hour sample
  counts; drives "active" (needs index update) vs "inactive" (needs delete).
- **discovered** — newly seen series awaiting their first "create".
- **demand** — the current demand set: metric names, LogQL stream selectors, and
  trace resource selectors, each read by its respective filter.

A background loop flushes to the control plane (discovered first, then known
deletes, then known updates), with retry/backoff. Heartbeats carry cache sizes
and sync stats.

It also serves the **edit-mode query tunnel**: when the control plane pushes a
`QueryRequest` over the stream, the extension runs it (in a bounded worker pool,
off the receive loop) against the local store named by the request's `target`
and streams a `QueryResponse` back, correlated by id. Each target has its own
base URL and its own read-only path allow-list:

- **VM** (`local_vm_query_url`) — `query`, `query_range`, `series`, `labels`,
  `label/<n>/values`, `metadata`, `status/*`.
- **Loki** (`local_loki_query_url`) — the read-only `/loki/api/v1/*` query paths,
  **excluding** `/loki/api/v1/tail` (a WebSocket live-tail stream — the tunnel is
  strictly one request → one response).
- **Tempo** (`local_tempo_query_url`) — the read-only search / trace-lookup
  paths, **excluding** the metrics-generator endpoints (`/api/metrics/*`).

`GET`/`POST` only; anything else is refused with `403`, and path traversal is
cleaned before matching. A target whose base URL is empty answers `503`.

```yaml
leansignal_edge_controller:
  endpoint: "mb1-grpc.eu11.leansignal.io:443"   # <tenant>-grpc.<domain>:443 in prod
  agent_key: "${env:LEANSIGNAL_AGENT_KEY}"
  insecure: false                                # true = plaintext h2c (local dev only)
  local_vm_query_url: "http://127.0.0.1:8428"    # local VictoriaMetrics query API (metrics)
  local_loki_query_url: "http://127.0.0.1:3100"  # local Loki query API (logs)
  local_tempo_query_url: "http://127.0.0.1:3200" # local Tempo query API (traces)
  reconnect_interval: 5s
  ping_interval: 30s
```
