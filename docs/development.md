# Developer guide

How the LeanSignal team works on this repository. For external contributors, the
essentials are in [CONTRIBUTING.md](../CONTRIBUTING.md); this page documents the
internal workflow in more detail.

## Repository at a glance

| Path | What it is |
|------|-----------|
| `components/` | the only first-party Go code (5 components + shared lib) |
| `manifest.yaml` | OCB manifest; assembles the full collector distribution |
| `_build/` | generated distribution (gitignored) — never edit by hand |
| `deploy/helm`, `deploy/docker` | Kubernetes chart + compose trial |
| `scripts/install`, `scripts/release` | installers + release tooling |
| `docs/` | user + developer documentation |

The agent is a client of the proprietary LeanSignal API. The control-plane protocol is defined in the `proto/` module (leansignal.agent.v1); keep it in sync with the
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
make install-hooks          # enable the pre-commit lint hook (one-time)
make test                   # go test -race ./components/...
make lint                   # golangci-lint, pinned to the CI version
make lint-fix               # auto-fix gofmt/goimports + fixable issues
make license                # refresh SPDX headers if you added files
make generate               # OCB -> _build/ (source only)
make build                  # compile the full distribution
make snapshot               # full goreleaser build for all platforms (no publish)
make helm-lint helm-template
make shellcheck
```

Fast iteration on component code doesn't need OCB: `go test -race ./components/...`
— which now exercises the log and trace demand filters alongside the metrics
tracker and filter.

### Pre-commit hook (lint locally, not in CI)

`make install-hooks` sets `core.hooksPath=.githooks`, so [`.githooks/pre-commit`](../.githooks/pre-commit)
runs **the same golangci-lint as CI** (pinned via `GOLANGCI_VERSION` in the
Makefile, installed on first use) on staged Go changes before every commit — so
lint failures are caught locally, not after a push. If it blocks a commit, run
`make lint-fix` (auto-formats), re-stage, and commit again; use
`git commit --no-verify` to skip in a pinch. `make lint` is the same check you can
run anytime.

### Adding or changing a component

1. Add code under `components/<name>/` with a `factory.go` exposing `NewFactory()`.
2. Wire it into `manifest.yaml` (`gomod` = this module, `import` = the package
   path, `name` = the import alias used in generated code).
3. `make generate` to confirm OCB resolves it; `make build` to compile.
4. If you touch OTLP→Prometheus naming, change **both** `metricstracker` and
   `demandfilter` — they intentionally mirror each other (see
   [components.md](components.md)).

## Running the agent locally

`make local-build` compiles the distribution into `_build/leansignal-agent`; the
run targets (`local-run`, `cloud-run`) execute that **prebuilt** binary without
recompiling. So the inner loop is:

```
edit components/** → make local-build → make local-run
```

### Build once: `make local-build`

Compiles `_build/` (runs OCB first if the generated sources don't exist yet).
Re-run it after any change under `components/**` — edits are picked up through a
local `replace` directive, so it's incremental (~seconds) and doesn't re-run OCB.
Changing `manifest.yaml` (added/removed a component or dependency) is handled
automatically: it re-runs OCB. For pure component work you don't need to build at
all — `go test -race ./components/...`.

### Against a local lean-api: `make local-run`

Runs the prebuilt binary with `config/agent-config.local.yaml`, wired to a local
lean-api (gRPC `:9090`, h2c). Defaults (override any on the command line):

| Make var | Default | Meaning |
|----------|---------|---------|
| `LOCAL_ENDPOINT` | `localhost:9090` | lean-api gRPC target (h2c, no TLS) |
| `AGENT_KEY` | dev key `deadbeef-…` | agent key; empty falls back to the dev key (seed it via `make local-seed`) |
| `LOCAL_VM` | `http://localhost:8482` | local vm-ag **base URL** (write + query) |
| `LOCAL_DATAPLANE` | `http://localhost:8483` | dataplane VM **base URL** (demanded subset) |

Endpoints are **base URLs** — the config appends `/api/v1/write` for the exporter
and the agent appends `/api/v1/query…` for the tunnel, so one value drives both.

Start two local VictoriaMetrics (everything + demanded subset; no vmauth locally),
build, then run:

```bash
docker run --rm -d -p 8482:8428 victoriametrics/victoria-metrics:v1.111.0   # vm-ag
docker run --rm -d -p 8483:8428 victoriametrics/victoria-metrics:v1.111.0   # dataplane
make local-build
make local-run
```

Because this connects to a real local lean-api, the index syncs and a demand list
arrives — so `metrics/filtered` populates (the dev config logs drops), and the
UI's **edit-mode query tunnel** (lean-api → this agent → vm-ag) works end to end.
Verify it:

```bash
# direct from the local store
curl -s 'http://localhost:8482/api/v1/query?query=system_cpu_load_average_1m'
# the SAME query tunneled through lean-api (dev bypasses the session guard)
curl -s 'http://localhost:8080/api/v1/metrics/avm_proxy/api/v1/query?query=system_cpu_load_average_1m'
```

### Against a cloud tenant: `make cloud-run`

Point the same local agent at a deployed tenant over **TLS (443)**. You give it
the tenant name and its agent key; the gRPC and ingest hosts are derived:

```bash
make cloud-run TENANT=mb1 AGENT_KEY=<the tenant's agent key>
```

| Make var | Default | Meaning |
|----------|---------|---------|
| `AGENT_KEY` | — (**required**) | tenant agent key (from its `agents` table) |
| `TENANT` | `mb1` | tenant name; derives the hosts below |
| `CLOUD_DOMAIN` | `eu11.leansignal.io` | cluster domain |
| `CLOUD_ENDPOINT` | `$(TENANT)-grpc.$(CLOUD_DOMAIN):443` | gRPC control (TLS) |
| `CLOUD_DATAPLANE` | `https://$(TENANT)-ingest.$(CLOUD_DOMAIN)` | vmauth ingest base |
| `LOCAL_VM` | `http://localhost:8482` | local VM base — the agent's own store (same as local-run) |

It uses `config/agent-config.cloud.yaml` (TLS on, `insecure: false`). The local VM
stays local — the agent always keeps full fidelity next to itself; the cloud reads
it back over the gRPC tunnel. The `…-api` host is REST/UI only and is not used by
the agent. Note: the ingest/dataplane path only works once the tenant's vmauth is
deployed; the control channel, local store, and edit-mode tunnel work regardless.

### Standalone (no local lean-api)

If you don't have lean-api running, the edge controller just retries the
connection (harmless) and — with no demand list — the `dataplane` pipeline stays
empty while the local pipeline still captures everything. The fastest path is the
all-in-one compose (collector image + local VM):

```bash
export LEANSIGNAL_ENDPOINT=localhost:9090                          # dummy gRPC; ok if unreachable
export LEANSIGNAL_AGENT_KEY=dev                                    # any non-empty value
export LEANSIGNAL_DATAPLANE_ENDPOINT=http://victoriametrics:8428/api/v1/write
docker compose -f deploy/docker/docker-compose.yaml up
```

To run **your locally built binary** instead (the normal inner loop when editing
components):

```bash
# 1. Build the distribution
make local-build                 # -> _build/leansignal-agent

# 2. Start a local VictoriaMetrics
docker run --rm -p 8428:8428 victoriametrics/victoria-metrics:v1.111.0

# 3. Set the connection details the example config reads from the environment
export LEANSIGNAL_ENDPOINT=localhost:9090
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
- The upstream OpenTelemetry Collector base is pinned in `manifest.yaml`; the
  co-located stores in `VM_VERSION`, `LOKI_VERSION`, and `TEMPO_VERSION`. Bump
  those deliberately (their own PRs).
