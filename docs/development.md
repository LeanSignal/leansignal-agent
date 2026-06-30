# Developer guide

How the LeanSignal team works on this repository. For external contributors, the
essentials are in [CONTRIBUTING.md](../CONTRIBUTING.md); this page documents the
internal workflow in more detail.

## Repository at a glance

| Path | What it is |
|------|-----------|
| `components/` | the only first-party Go code (3 components + shared lib) |
| `manifest.yaml` | OCB manifest; assembles the full collector distribution |
| `_build/` | generated distribution (gitignored) — never edit by hand |
| `deploy/helm`, `deploy/docker` | Kubernetes chart + compose trial |
| `scripts/install`, `scripts/release` | installers + release tooling |
| `docs/` | user + developer documentation |

The agent is a client of the proprietary LeanSignal API. The WebSocket protocol
lives in `components/edgecontroller/messages.go`; keep it in sync with the
backend when either side changes.

## Branching model

Trunk-based development on `main`:

- `main` is always releasable and protected (PRs only, green CI required).
- Branch off `main` per change: `feat/<short>`, `fix/<short>`, `docs/<short>`,
  `chore/<short>`.
- Keep branches short-lived; rebase on `main` rather than long-running forks.

## Commits & PRs

- **Sign off every commit** (`git commit -s`) — DCO is enforced.
- Prefer [Conventional Commits](https://www.conventionalcommits.org/)
  (`feat:`, `fix:`, `docs:`, `chore:`, `refactor:`, `test:`) — the release
  changelog is generated from commit/PR titles.
- One logical change per PR; include tests for behavior changes.
- PRs require: green CI, at least one review (see `CODEOWNERS`), and up-to-date
  with `main`. Squash-merge is the default.

## Local development loop

```bash
make install-tools          # ocb, addlicense, goreleaser (one-time)
make test                   # go test -race ./components/...
make vet lint               # static checks
make license                # refresh SPDX headers if you added files
make generate               # OCB -> _build/ (source only)
make build                  # compile the full distribution
make snapshot               # full goreleaser build for all platforms (no publish)
make helm-lint helm-template
make shellcheck
```

Fast iteration on component code doesn't need OCB: `go test -race ./components/...`.

### Adding or changing a component

1. Add code under `components/<name>/` with a `factory.go` exposing `NewFactory()`.
2. Wire it into `manifest.yaml` (`gomod` = this module, `import` = the package
   path, `name` = the import alias used in generated code).
3. `make generate` to confirm OCB resolves it; `make build` to compile.
4. If you touch OTLP→Prometheus naming, change **both** `metricstracker` and
   `demandfilter` — they intentionally mirror each other (see
   [components.md](components.md)).

## Running the agent locally

You can run the whole pipeline on your machine. You do **not** need a real
LeanSignal backend: the edge controller will just retry the connection
(harmless), and since no demand list arrives, the `dataplane` (filtered) pipeline
stays empty while the local pipeline captures everything.

The fastest path is the all-in-one compose (collector image + local VM):

```bash
export LEANSIGNAL_ENDPOINT=ws://127.0.0.1:9/                       # dummy; ok if unreachable
export LEANSIGNAL_AGENT_KEY=dev                                    # any non-empty value
export LEANSIGNAL_DATAPLANE_ENDPOINT=http://127.0.0.1:8428/api/v1/write
docker compose -f deploy/docker/docker-compose.yaml up
```

To run **your locally built binary** instead (the normal inner loop when editing
components):

```bash
# 1. Build the distribution
make build                       # -> _build/leansignal-agent

# 2. Start a local VictoriaMetrics
docker run --rm -p 8428:8428 victoriametrics/victoria-metrics:v1.111.0

# 3. Set the connection details the example config reads from the environment
export LEANSIGNAL_ENDPOINT=ws://127.0.0.1:9/
export LEANSIGNAL_AGENT_KEY=dev
export LEANSIGNAL_DATAPLANE_ENDPOINT=http://127.0.0.1:8428/api/v1/write

# 4. Validate, then run
./_build/leansignal-agent validate --config config/agent-config.example.yaml
./_build/leansignal-agent --config config/agent-config.example.yaml
```

`hostmetrics` starts producing data immediately. To push your own OTLP metrics:

```bash
go run github.com/open-telemetry/opentelemetry-collector-contrib/cmd/telemetrygen@latest \
  metrics --otlp-insecure --otlp-endpoint localhost:4317 --duration 30s
```

Verify it's flowing:

```bash
curl -s http://localhost:8428/api/v1/label/__name__/values | jq .   # metric names in the local store
curl -sf http://localhost:13133/ && echo " agent healthy"           # health check
```

Notes:

- All three env vars must be set — the example config references them, so the
  collector won't start if any are missing.
- Want to exercise the demand filter locally? It only forwards metrics named in a
  demand list, which comes from the backend. Without one, the filtered pipeline is
  empty by design; point it at a real control plane (set `LEANSIGNAL_ENDPOINT` +
  a valid `LEANSIGNAL_AGENT_KEY`) to see it populate.
- For pure component work you don't need to build or run the collector at all:

  ```bash
  go test -race ./components/...
  ```

## CI

Every PR/push runs [`ci.yml`](../.github/workflows/ci.yml): `go vet`, race tests,
license-header check, `golangci-lint`, an OCB generate + compile, `helm lint`,
and `shellcheck`. [`codeql.yml`](../.github/workflows/codeql.yml) runs security
analysis. All must pass before merge.

## Releasing

Tag-driven, fully automated — see [releasing.md](releasing.md). In short:

```bash
# update CHANGELOG.md, then:
git tag v0.2.0 && git push origin v0.2.0
```

This builds cross-platform binaries, multi-arch images, the VM-mirrored bundles,
and the Helm chart, and publishes the GitHub Release.

## Versioning

- Agent: SemVer tags `vMAJOR.MINOR.PATCH`.
- The upstream OpenTelemetry Collector base is pinned in `manifest.yaml`;
  VictoriaMetrics in `VM_VERSION`. Bump those deliberately (their own PRs).
