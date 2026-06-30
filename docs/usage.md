# Using the agent

Once installed and connected, the agent collects metrics, stores **everything**
locally, and forwards the **demanded** subset to the central dataplane.

## What you provide at install time

Two things are always required (the installer prompts for them if you don't pass
flags):

1. **LeanSignal API URL** — the WebSocket control-plane endpoint
   (`wss://api.leansignal.com/api/v1/agents/ws/`).
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
| Nothing in the central dataplane | no demand list yet, or control-plane unreachable (check the WebSocket URL + agent key) |
| Nothing in the local store | check the agent is running and the local VM is up on `:8428` |
| `connection refused` to the API | wrong `endpoint` / firewall / TLS — confirm the `wss://` URL |
| `401`/auth errors on connect | wrong or expired agent key |

Increase log detail with `--set logLevel=debug` (Helm) or `telemetry.logs.level:
debug` in the config file.
