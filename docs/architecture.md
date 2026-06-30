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
    • persistent gRPC stream to the LeanSignal API
    • receives the in-process index batches → maintains 3 caches
    • pushes index create/update/delete to the control plane
    • receives the demand list → exposed to the demand filter
```

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
| `leansignal_edge_controller` | extension | gRPC control plane + index/demand caches |
| `metricsindex` | library | shared fingerprint types + in-process pub/sub |

See [components.md](components.md) for details.

## Build model

`manifest.yaml` declares the full set of upstream receivers/processors/exporters
plus the three first-party components, and is assembled by OCB into a single
collector binary. First-party code lives under `components/`; the generated
distribution lands in `_build/` (gitignored). Releases are produced by goreleaser
(see [releasing.md](releasing.md)).
