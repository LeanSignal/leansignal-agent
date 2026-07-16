# Using the agent

Once installed and connected, the agent collects **telemetry** — metrics, logs,
and traces — stores **everything** locally, and forwards the **demanded** subset
to the central dataplane.

## What you provide at install time

Two things are required (the installer prompts for them if you don't pass flags):

1. **Tenant name** — the gRPC control host (`<tenant>-grpc.<domain>`) and the
   ingest host (`<tenant>-ingest.<domain>`) are derived from it (domain defaults
   to `eu11.leansignal.io`).
2. **Agent key / secret token** — authenticates this agent (as gRPC metadata on
   the control channel and as the bearer token on the dataplane).

Advanced: override the derived hosts with the endpoint / dataplane-endpoint flags
(or `--domain`). See the per-platform install guides for the exact flags.

## Sending your own telemetry

The agent exposes OTLP receivers that carry **all three signals** — metrics, logs,
and traces:

- gRPC on `4317`
- HTTP on `4318`

Point any OpenTelemetry SDK or collector at `http://<agent-host>:4318` (or
`:4317` for gRPC). On Kubernetes, use the in-cluster service
`leansignal-agent.<namespace>.svc:4317`. Host and Kubernetes node metrics are
collected automatically (hostmetrics / kubeletstats / k8s_cluster). Already
running a Loki-style log shipper (promtail, Grafana Alloy)? Point it at the `loki`
push receiver on `:3500` (HTTP) / `:3600` (gRPC) instead of re-instrumenting. See
[Connecting telemetry sources](data-sources.md) for the full list.

## Querying the local stores

Everything lands in the co-located store for its signal — **VictoriaMetrics** for
metrics, **Loki** for logs, **Tempo** for traces — each on loopback:

| Signal | Local store | Query API | Default port |
|--------|-------------|-----------|--------------|
| Metrics | VictoriaMetrics | Prometheus query API | `:8428` |
| Logs | Loki | Loki query API (LogQL) | `:3100` |
| Traces | Tempo | Tempo query API | `:3200` |

```bash
# metrics — list metric names
curl -s http://localhost:8428/api/v1/label/__name__/values | jq .

# metrics — instant query
curl -s 'http://localhost:8428/api/v1/query?query=up' | jq .

# logs — list stream labels in the local Loki
curl -s http://localhost:3100/loki/api/v1/labels | jq .

# traces — search the local Tempo
curl -s 'http://localhost:3200/api/search?limit=5' | jq .
```

Point Grafana at the stores for dashboards: a Prometheus datasource at
`http://<host>:8428`, a Loki datasource at `http://<host>:3100`, and a Tempo
datasource at `http://<host>:3200`.

### From the LeanSignal UI (query tunnel)

You don't need to expose the local stores to use them from LeanSignal. When you
edit a dashboard, the UI's **edit-mode** queries are tunneled down the agent's
gRPC control stream, run against the matching local store (VictoriaMetrics, Loki,
or Tempo, per the query's target), and returned — so you get full-fidelity data in
the UI while the stores stay private. Only read-only query paths are allowed; the
agent must be connected (if it's offline the UI returns a `503` for edit-mode).
View-mode dashboards read the central stores instead.

## How "demand" works

The central stores only receive what's on the **demand set** sent by the
LeanSignal control plane — metric names for metrics, LogQL stream selectors for
logs, and trace resource selectors for traces. This keeps central cardinality and
cost under control while the local stores keep full fidelity.

- For **metrics**, newly seen series are reported to the control plane (the
  **metric index**); logs and traces have no such index — their demand comes
  entirely from the selectors the control plane pushes down.
- The control plane decides what to demand; the agent applies it on the next
  batch automatically — no restart needed.
- **If the agent can't reach the control plane**, no demand set is applied and the
  central stores receive nothing (**fail-closed**, each signal independently). The
  local stores are unaffected.

## Verify it's healthy

- Health endpoint: `http://<host>:13133/` (HTTP 200 when serving).
- Logs:
  - Linux: `journalctl -u leansignal-agent -f`
  - macOS: `tail -f /usr/local/var/log/leansignal-agent/agent.log`
  - Windows: Event Log / service status (`Get-Service LeanSignalAgent`)
  - Kubernetes: `kubectl logs deploy/leansignal-agent -f`
- A connected agent logs periodic pings and index sync counts.

## Troubleshooting

| Symptom | Likely cause |
|---------|--------------|
| Nothing in the central stores | no demand set yet, or control-plane unreachable (check the gRPC URL + agent key) |
| Nothing in a local store | check the agent is running and the local store for that signal is up (VM `:8428`, Loki `:3100`, Tempo `:3200`) |
| `connection refused` to the API | wrong `endpoint` / firewall / TLS — confirm the host:port endpoint |
| `401`/auth errors on connect | wrong or expired agent key |
| UI edit-mode returns `503` / no data | agent not connected, or the target's `local_*_query_url` is unset / points at the wrong local store |
| UI edit-mode query returns `403` | the path isn't on that target's read-only allow-list |

Increase log detail with `--set logLevel=debug` (Helm) or `telemetry.logs.level:
debug` in the config file.
