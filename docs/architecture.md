# Architecture

The LeanSignal Agent is an OpenTelemetry Collector distribution (built with the
OpenTelemetry Collector Builder, OCB) plus first-party components and a shared
library. It collects **telemetry** — metrics, logs, and traces — maintains a
metric **index** in the LeanSignal control plane, and writes to two tiers of
storage per signal.

## Data flow

```
  receivers (OTLP, host/Kubernetes metrics)
        │
        ▼
  ┌──────────────────────── pipeline: metrics/all ─────────────────────────┐
  │  leansignalmetrics_tracker (pass-through)                               │
  │    • expands each metric to its Prometheus series names                 │
  │    • fingerprints every series and broadcasts the batch in-process      │
  │  exporters:                                                            │
  │    • local VictoriaMetrics  ← everything (full fidelity)               │
  │    • forward/demand_filter  ─┐ (fan-out copy)                          │
  └──────────────────────────────┼────────────────────────────────────────┘
                                  ▼
  ┌──────────────────────── pipeline: metrics/filtered ────────────────────┐
  │  leansignal_demand_filter                                              │
  │    • reads the current demand list from the edge controller            │
  │    • drops every metric not on the list (empty list ⇒ drop all)        │
  │  exporter:                                                            │
  │    • central dataplane ← demanded subset only                          │
  └────────────────────────────────────────────────────────────────────────┘

  leansignal_edge_controller (extension, runs alongside the pipelines)
    • ONE persistent, outbound gRPC stream to the LeanSignal API (agent dials out)
    • receives the in-process index batches → maintains 3 caches
    • pushes index create/update/delete to the control plane
    • receives the demand list → exposed to the demand filter
    • answers edit-mode QUERY requests pushed by the control plane by running them
      (read-only, allow-listed) against the local VictoriaMetrics and streaming
      the response back — see "Query tunnel" below
```

**Logs and traces follow the same shape** through their own pipelines: a
`logs/all` → `logs/filtered` pair (co-located Loki → tenant Loki) and a
`traces/all` → `traces/filtered` pair (co-located Tempo → tenant Tempo), each
gated by its own demand filter. Only the metric pipeline has a tracker/index —
logs and traces need none, because their demand comes from selectors pushed down
the stream, not from a discovered catalogue. All three filters are **fail-closed**:
nothing is forwarded until the demand set asks for it.

## Control plane vs data plane

Two independent paths connect the agent to LeanSignal:

- **Control plane — one gRPC bidirectional stream** (`AgentControl.Connect`). The
  agent dials out and keeps it open; over it flow the metric index (agent → API),
  the demand set — metric names, LogQL stream selectors, and trace resource
  selectors (API → agent), heartbeats, and edit-mode query requests/responses
  (both ways). Authenticated by the agent key in gRPC metadata. In production this
  is TLS on 443 via the tenant's `…-grpc` ingress; locally it is plaintext h2c.
- **Data plane — one path per signal**, all authenticated by the same agent key:
  - **Metrics** — Prometheus remote-write of the demanded series to the central
    VictoriaMetrics, through **vmauth** (bearer token) in production, or straight to
    a VM locally. The agent is vmauth-agnostic — it just remote-writes to a
    configurable URL with an optional bearer header.
  - **Logs** — OTLP push of the demanded log streams to the tenant **Loki**,
    through the ingest ingress (which forward-authenticates the agent key and
    stamps the tenant org id).
  - **Traces** — OTLP push of the demanded spans to the tenant **Tempo**, through
    the same ingest ingress.

Because the agent only ever dials **out**, it needs no inbound connectivity and
the local stores are never exposed to the internet.

## Query tunnel (reading the local stores from the UI)

The local stores hold full fidelity but sit in a private network, so the
LeanSignal UI cannot reach them directly. Instead, when a user explores telemetry
in **edit / "Available" mode**, lean-api pushes the query **down the existing gRPC
stream** with a `target` (VM, Loki, or Tempo); the edge controller runs it against
that store's query API and streams the response back, correlated by request id. To
lean-api's HTTP caller it looks like a synchronous request; on the wire it is one
request/response pair multiplexed onto the control stream (the same pattern
Kubernetes' Konnectivity uses to reach workloads behind a firewall).

- Only **read-only** paths are allowed, with a per-target allow-list:
  - **VictoriaMetrics** — `/api/v1/query`, `/query_range`, `/series`, `/labels`,
    `/label/<name>/values`, `/metadata`, `/status/*`.
  - **Loki** — `/loki/api/v1/query`, `/query_range`, `/labels`,
    `/label/<name>/values`, `/series`, `/index/stats`, `/index/volume`,
    `/patterns`. `/loki/api/v1/tail` (a WebSocket stream) is **not** allowed — the
    tunnel is strictly one request → one response.
  - **Tempo** — `/api/echo`, `/api/search`, `/api/search/tags`,
    `/api/(v2/)search/tag/<tag>/values`, `/api/(v2/)traces/<id>`. The
    metrics-generator endpoints (`/api/metrics/*`) are not enabled.

  `GET`/`POST` only; admin, ingest, delete and write paths are refused — defense in
  depth, since lean-api is already authenticated.
- The query bases are `local_vm_query_url` / `local_loki_query_url` /
  `local_tempo_query_url` (see [configuration.md](configuration.md)); the agent
  appends the API path itself.
- If the agent is offline, lean-api answers the UI with `503` immediately; a slow
  query maps to `504`. View-mode / "Stored" reads hit the **central** stores
  directly and don't use the tunnel.

## Storage tiers

Everything is kept locally at full fidelity, right next to the workload; only the
demanded subset reaches the central, long-retention stores — controlling long-term
cardinality and cost.

| Signal | Local store (everything) | Local retention | Central store (demanded only) |
|--------|--------------------------|-----------------|-------------------------------|
| Metrics | co-located **VictoriaMetrics** | short (e.g. 1d) | central dataplane VM |
| Logs | co-located **Loki** | short (~1h window) | tenant Loki |
| Traces | co-located **Tempo** | short (~1h window) | tenant Tempo |

## The metric index & demand model

- As metrics flow, the tracker discovers every unique timeseries (metric name +
  labels) and fingerprints it. New series are reported to the control plane
  ("index create"); ongoing series are periodically refreshed ("update"); series
  that go silent are removed ("delete"). Logs and traces have **no** such index.
- The control plane decides what is worth storing centrally and sends back a
  **demand set**: metric names (from dashboard/alert queries), LogQL stream
  selectors, and trace resource selectors (from dashboard panels). The three demand
  filters enforce their respective parts on the `*/filtered` pipelines. The demand
  set carries a content **hash** (covering all three lists) that the agent echoes
  back on its heartbeat so the control plane can detect and re-push a missed update.
- **Fail-closed:** if no demand set has been received (e.g. the agent can't reach
  the control plane), the central stores receive nothing — the local stores still
  capture everything. Logs and traces are demand-driven the same way: nothing is
  forwarded until a selector demands it.

## Components

| Component | Type | Role |
|-----------|------|------|
| `leansignalmetrics_tracker` | processor (metrics) | builds & broadcasts the timeseries index (pass-through) |
| `leansignal_demand_filter` | processor (metrics) | drops metrics not on the demand set |
| `leansignal_log_demand_filter` | processor (logs) | drops log records whose Loki stream labels match no demanded selector |
| `leansignal_trace_demand_filter` | processor (traces) | drops spans whose resource attributes match no demanded selector (resource-granular) |
| `leansignal_edge_controller` | extension | gRPC control plane + index/demand caches + edit-mode query tunnel (VM/Loki/Tempo) |
| `metricsindex` | library | shared fingerprint types + in-process pub/sub |

See [components.md](components.md) for details.

## Build model

`manifest.yaml` declares the full set of upstream receivers/processors/exporters
plus the first-party components, and is assembled by OCB into a single collector
binary. First-party code lives under `components/`; the generated distribution
lands in `_build/` (gitignored). Releases are produced by goreleaser (see
[releasing.md](releasing.md)).
