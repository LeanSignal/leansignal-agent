# Changelog

All notable changes to this project are documented here. The format is based on
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and this project follows
[Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- Initial public, Apache-2.0 release of the LeanSignal Agent.
- Custom OpenTelemetry Collector distribution (OCB) with three first-party
  components: `leansignalmetrics_tracker`, `leansignal_demand_filter`, and the
  `leansignal_edge_controller` extension.
- Co-located VictoriaMetrics for full local fidelity; demand-filtered forwarding
  to a central dataplane.
- Helm chart, host installers (Linux/macOS/Windows), and docker-compose trial.
- GitHub Actions CI + goreleaser release pipeline (cross-platform binaries,
  multi-arch images, VictoriaMetrics mirroring + combined bundles).

[Unreleased]: https://github.com/LeanSignal/leansignal-agent/commits/main
