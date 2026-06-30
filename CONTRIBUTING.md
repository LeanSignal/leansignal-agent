# Contributing to LeanSignal Agent

Thanks for your interest in contributing! This document explains how to build,
test, and submit changes. For the team's detailed workflow (branching model,
release process, adding components) see [docs/development.md](docs/development.md).

## Developer Certificate of Origin (DCO)

We use the [Developer Certificate of Origin](https://developercertificate.org/).
Every commit must be signed off, certifying you wrote the code or have the right
to submit it under the project's Apache-2.0 license:

```bash
git commit -s -m "your message"
```

This appends a `Signed-off-by: Your Name <you@example.com>` trailer. PRs without
sign-off will be asked to amend.

## Prerequisites

- Go (see the version in [`go.mod`](go.mod))
- [OpenTelemetry Collector Builder (OCB)](https://github.com/open-telemetry/opentelemetry-collector/tree/main/cmd/builder) — `make install-tools` installs the pinned version
- `helm` (for the chart), `shellcheck` (for install scripts) — optional but recommended

## Project layout

```
components/            custom OTel components (the only first-party Go code)
  metricsindex/        shared in-process pub/sub + fingerprint types
  metricstracker/      processor: builds & broadcasts the timeseries index
  edgecontroller/      extension: WebSocket control plane + caches
  demandfilter/        processor: drops non-demanded metrics
manifest.yaml          OCB manifest assembling the full distribution
config/                example host configuration
deploy/                Helm chart + docker-compose
scripts/install/       Linux/macOS/Windows installers
docs/                  documentation
```

The full collector binary is **generated** by OCB into `_build/` (gitignored)
from `manifest.yaml`; you normally only edit the packages under `components/`.

## Build & test

```bash
make test            # go test -race ./components/...
make vet             # go vet ./...
make lint            # golangci-lint (if installed)
make generate        # run OCB to generate _build/ (source only)
make build           # generate + compile the full distribution binary
```

Quick component iteration without OCB:

```bash
go test -race ./components/...
```

## Pull requests

1. Fork and create a topic branch.
2. Keep changes focused; add tests for behavior changes.
3. Run `make test vet` (and `make lint` if available) before pushing.
4. Sign off your commits (`-s`).
5. Open a PR describing the change and motivation.

## Adding or changing collector components

- Component code lives under `components/<name>/`.
- If you add a new component, also wire it into `manifest.yaml` and run
  `make generate` to confirm OCB resolves it.
- The OTLP→Prometheus metric-name logic is intentionally mirrored between
  `metricstracker` and `demandfilter`; if you change naming in one, change both
  (see [`docs/components.md`](docs/components.md)).

## License headers

First-party source files carry an SPDX Apache-2.0 header. Run `make license`
(uses [`addlicense`](https://github.com/google/addlicense)) to add/verify them.
