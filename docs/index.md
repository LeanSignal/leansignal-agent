# LeanSignal Agent documentation

The LeanSignal Agent is an Apache-2.0 OpenTelemetry Collector distribution that
writes everything to a co-located VictoriaMetrics and forwards a demanded subset
to a central dataplane, keeping a live metric index in sync with the LeanSignal
control plane. It dials **out** over a single gRPC stream that carries the index
up, the demand list down, and edit-mode queries both ways — so the LeanSignal UI
can read your private full-fidelity store without it ever being exposed.

## Contents

### Users
- [Usage](usage.md) — sending metrics, querying the local store, how demand works, troubleshooting
- [Connecting metrics sources](data-sources.md) — apps (OTLP), Prometheus exporters (node_exporter, …), host & Kubernetes
- [Configuration](configuration.md) — settings, env vars, pipelines, applying config changes
- [Host metrics (macOS) & self-monitoring](host-and-self-monitoring.md) — collect CPU/memory/disk, monitor the collector itself
- Install:
  - [Kubernetes](install-kubernetes.md)
  - [Linux](install-linux.md)
  - [macOS](install-macos.md)
  - [Windows](install-windows.md)
- [Upgrading](upgrading.md) — agent-only vs VM upgrades, data safety, snapshots, rollback

### Understanding it
- [Architecture](architecture.md) — data flow, storage tiers, the demand/index model, control plane vs data plane, the query tunnel
- [Components](components.md) — the custom tracker, demand filter, and edge controller

### Developers & maintainers
- [Development guide](development.md) — branching, PRs, local loop, conventions
- [Releasing](releasing.md) — the tag-driven release process

See the repository [README](../README.md) for a quick start.
