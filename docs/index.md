# LeanSignal Agent documentation

The LeanSignal Agent is an Apache-2.0 OpenTelemetry Collector distribution that
collects **telemetry** — metrics, logs, and traces — writes everything to
co-located stores (VictoriaMetrics, Loki, Tempo) and forwards a demanded subset
to a central dataplane, keeping a live metric index in sync with the LeanSignal
control plane. It dials **out** over a single gRPC stream that carries the index
up, the demand set down, and edit-mode queries both ways — so the LeanSignal UI
can read your private full-fidelity stores without them ever being exposed.

## Contents

### Users
- [Usage](usage.md) — sending telemetry, querying the local stores, how demand works, troubleshooting
- [Connecting telemetry sources](data-sources.md) — apps (OTLP), Prometheus exporters (node_exporter, …), log shippers, host & Kubernetes
- [Configuration](configuration.md) — settings, env vars, pipelines, applying config changes
- [Host metrics (macOS) & self-monitoring](host-and-self-monitoring.md) — collect CPU/memory/disk, monitor the collector itself
- [Agent own telemetry](own-telemetry.md) — the self-monitoring metrics the agent exposes (collector + LeanSignal control plane) and what to alert on
- Install:
  - [Kubernetes](install-kubernetes.md)
  - [Linux](install-linux.md)
  - [macOS](install-macos.md)
  - [Windows](install-windows.md)
- [Upgrading](upgrading.md) — agent-only vs store upgrades, data safety, snapshots, rollback

### Understanding it
- [Architecture](architecture.md) — data flow, storage tiers, the demand/index model, control plane vs data plane, the query tunnel
- [Components](components.md) — the custom tracker, the per-signal demand filters, and edge controller

### Developers & maintainers
- [Development guide](development.md) — branching, PRs, local loop, conventions
- [Releasing](releasing.md) — the tag-driven release process

See the repository [README](../README.md) for a quick start.
