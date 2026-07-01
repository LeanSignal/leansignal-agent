# Architecture

The LeanSignal Agent is an OpenTelemetry Collector distribution (built with the
OpenTelemetry Collector Builder, OCB) plus three first-party components and a
shared library. It collects metrics, maintains a metric **index** in the
LeanSignal control plane, and writes to two tiers of storage.

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

## Control plane vs data plane

Two independent paths connect the agent to LeanSignal:

- **Control plane — one gRPC bidirectional stream** (`AgentControl.Connect`). The
  agent dials out and keeps it open; over it flow the metric index (agent → API),
  the demand list (API → agent), heartbeats, and edit-mode query requests/responses
  (both ways). Authenticated by the agent key in gRPC metadata. In production this
  is TLS on 443 via the tenant's `…-grpc` ingress; locally it is plaintext h2c.
- **Data plane — Prometheus remote-write** of the demanded subset to the central
  VictoriaMetrics. In production this goes through **vmauth**, which authenticates
  the agent key (bearer token) and forwards to the tenant's central store; locally
  it writes straight to a VM. The agent itself is vmauth-agnostic — it just does
  remote-write to a configurable URL with an optional bearer header.

Because the agent only ever dials **out**, it needs no inbound connectivity and
the local store is never exposed to the internet.

## Query tunnel (reading the local store from the UI)

The local VictoriaMetrics holds full fidelity but sits in a private network, so
the LeanSignal UI cannot reach it directly. Instead, when a user opens a dashboard
in **edit mode**, lean-api pushes the VictoriaMetrics query **down the existing
gRPC stream**; the edge controller runs it against the local store's query API and
streams the response back, correlated by request id. To lean-api's HTTP caller it
looks like a synchronous request; on the wire it is one request/response pair
multiplexed onto the control stream (the same pattern Kubernetes' Konnectivity
uses to reach workloads behind a firewall).

- Only **read-only** VictoriaMetrics paths are allowed (`/api/v1/query`,
  `/query_range`, `/series`, `/labels`, `/label/<name>/values`, `/metadata`,
  `/status/*`; `GET`/`POST` only). Admin, import, delete and write paths are
  refused — defense in depth, since lean-api is already authenticated.
- The query base is `local_vm_query_url` (see [configuration.md](configuration.md));
  the agent appends the API path itself.
- If the agent is offline, lean-api answers the UI with `503` immediately; a slow
  query maps to `504`. View-mode dashboards read the **central** store directly and
  don't use the tunnel.

## Storage tiers

| Tier | Location | Contents | Typical retention |
|------|----------|----------|-------------------|
| **Local VictoriaMetrics** | co-located with the agent | **everything** | short (e.g. 1d) |
| **Central dataplane** | central / managed | only **demanded** series | long (e.g. weeks) |

The local store gives you full-fidelity recent data right next to the workload;
the central store keeps only what's explicitly demanded, controlling long-term
cardinality and cost.

## The metric index & demand model

- As metrics flow, the tracker discovers every unique timeseries (metric name +
  labels) and fingerprints it. New series are reported to the control plane
  ("index create"); ongoing series are periodically refreshed ("update"); series
  that go silent are removed ("delete").
- The control plane decides which metrics are worth storing centrally and sends
  back a **demand list**. The demand filter enforces it on the `metrics/filtered`
  pipeline.
- **Fail-closed:** if no demand list has been received (e.g. the agent can't
  reach the control plane), the central dataplane receives nothing — the local
  store still captures everything.

## Components

| Component | Type | Role |
|-----------|------|------|
| `leansignalmetrics_tracker` | processor | builds & broadcasts the timeseries index (pass-through) |
| `leansignal_demand_filter` | processor | drops metrics not on the demand list |
| `leansignal_edge_controller` | extension | gRPC control plane + index/demand caches + edit-mode query tunnel |
| `metricsindex` | library | shared fingerprint types + in-process pub/sub |

See [components.md](components.md) for details.

## Build model

`manifest.yaml` declares the full set of upstream receivers/processors/exporters
plus the three first-party components, and is assembled by OCB into a single
collector binary. First-party code lives under `components/`; the generated
distribution lands in `_build/` (gitignored). Releases are produced by goreleaser
(see [releasing.md](releasing.md)).
