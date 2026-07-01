# Using the agent

Once installed and connected, the agent collects metrics, stores **everything**
locally, and forwards the **demanded** subset to the central dataplane.

## What you provide at install time

Two things are always required (the installer prompts for them if you don't pass
flags):

1. **LeanSignal API URL** — the gRPC control-plane target (host:port)
   (`api.leansignal.com:443`).
2. **Agent key / secret token** — authenticates this agent to the API.

Plus the **central dataplane URL** (Prometheus remote-write) for the demanded
subset. See the per-platform install guides for the exact flags.

## Sending your own metrics

The agent exposes OTLP receivers:

- gRPC on `4317`
- HTTP on `4318`

Point any OpenTelemetry SDK or collector at `http://<agent-host>:4318` (or
`:4317` for gRPC). On Kubernetes, use the in-cluster service
`leansignal-agent.<namespace>.svc:4317`. Host and Kubernetes node metrics are
collected automatically (hostmetrics / kubeletstats / k8s_cluster).

## Querying the local store

Everything lands in the co-located VictoriaMetrics (default `:8428`), which
speaks the Prometheus query API:

```bash
# list metric names
curl -s http://localhost:8428/api/v1/label/__name__/values | jq .

# instant query
curl -s 'http://localhost:8428/api/v1/query?query=up' | jq .
```

Point Grafana (Prometheus datasource) at `http://<host>:8428` for dashboards.

### From the LeanSignal UI (query tunnel)

You don't need to expose the local store to use it from LeanSignal. When you edit
a dashboard, the UI's **edit-mode** queries are tunneled down the agent's gRPC
control stream, run against the local store, and returned — so you get
full-fidelity data in the UI while the store stays private. Only read-only query
paths are allowed; the agent must be connected (if it's offline the UI returns a
`503` for edit-mode). View-mode dashboards read the central dataplane instead.

## How "demand" works

The central dataplane only receives metrics on the **demand list** sent by the
LeanSignal control plane. This keeps central cardinality and cost under control
while the local store keeps full fidelity.

- Newly seen series are reported to the control plane (the **metric index**).
- The control plane decides what to demand; the agent applies it on the next
  batch automatically — no restart needed.
- **If the agent can't reach the control plane**, no demand list is applied and
  the dataplane receives nothing (fail-closed). The local store is unaffected.

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
| Nothing in the central dataplane | no demand list yet, or control-plane unreachable (check the gRPC URL + agent key) |
| Nothing in the local store | check the agent is running and the local VM is up on `:8428` |
| `connection refused` to the API | wrong `endpoint` / firewall / TLS — confirm the host:port endpoint |
| `401`/auth errors on connect | wrong or expired agent key |
| UI edit-mode returns `503` / no data | agent not connected, or `local_vm_query_url` points at the wrong local VM |
| UI edit-mode query returns `403` | the path isn't on the read-only allow-list (only VM query APIs are permitted) |

Increase log detail with `--set logLevel=debug` (Helm) or `telemetry.logs.level:
debug` in the config file.
