# LeanSignal Agent documentation

The LeanSignal Agent is an Apache-2.0 OpenTelemetry Collector distribution that
writes everything to a co-located VictoriaMetrics and forwards a demanded subset
to a central dataplane, keeping a live metric index in sync with the LeanSignal
control plane.

## Contents

### Users
- [Usage](usage.md) — sending metrics, querying the local store, how demand works, troubleshooting
- [Configuration](configuration.md) — settings, env vars, pipelines
- Install:
  - [Kubernetes](install-kubernetes.md)
  - [Linux](install-linux.md)
  - [macOS](install-macos.md)
  - [Windows](install-windows.md)

### Understanding it
- [Architecture](architecture.md) — data flow, storage tiers, the demand/index model
- [Components](components.md) — the custom tracker, demand filter, and edge controller

### Developers & maintainers
- [Development guide](development.md) — branching, PRs, local loop, conventions
- [Releasing](releasing.md) — the tag-driven release process

See the repository [README](../README.md) for a quick start.
