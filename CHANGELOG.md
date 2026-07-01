# Changelog

All notable changes to this project are documented here. The format is based on
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and this project follows
[Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

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

[Unreleased]: https://github.com/LeanSignal/leansignal-agent/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/LeanSignal/leansignal-agent/releases/tag/v0.1.0
