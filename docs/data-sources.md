# Connecting telemetry sources

The agent is a full OpenTelemetry Collector, so it can take telemetry — metrics,
logs, and traces — from many producers. Some receivers are **on by default**;
others you enable by editing the config. Everything you connect is written to the
local store for its signal in full; only the demanded subset is forwarded
centrally.

## What's collected out of the box

| Install | On automatically |
|---------|------------------|
| Linux / macOS / Windows host | `hostmetrics` — CPU, memory, disk, filesystem, network, load |
| Kubernetes | `kubeletstats` (node/pod/container) + `k8s_cluster` (object states) |
| Everywhere | `otlp` receiver on `:4317` (gRPC) and `:4318` (HTTP) — carries metrics, logs, **and** traces from your apps |
| Everywhere | `loki` push receiver on `:3500` (HTTP) and `:3600` (gRPC) — accepts logs from promtail / Grafana Alloy-style shippers |

## What to connect — recommendations

| You want to collect… | Use | What to install |
|----------------------|-----|-----------------|
| Host CPU / memory / disk / network | `hostmetrics` | nothing — built in |
| **Your own application metrics** | `otlp` | instrument the app with an OpenTelemetry SDK |
| A service exposing a Prometheus `/metrics` page (node_exporter, cAdvisor, app exporters, kube-state-metrics) | `prometheus` (scrape) | run the exporter, add a scrape job |
| A producer already doing Prometheus remote-write | `prometheusremotewrite` receiver | point it at the agent |
| **Your own application logs** | `otlp`, or `loki` push | instrument with an OTel SDK/collector, or point promtail / Alloy at the agent |
| **Your own application traces** | `otlp` | instrument the app with an OpenTelemetry SDK |
| Kubernetes nodes / pods | `kubeletstats` + `k8s_cluster` | nothing — built in |

**Rule of thumb:** on a host you usually **don't need node_exporter** — the
built-in `hostmetrics` receiver already covers host metrics. Add node_exporter (or
Windows' `windows_exporter`) only if you specifically want its metric set. For
anything that already exposes a Prometheus `/metrics` endpoint, use the
`prometheus` receiver to scrape it.

## 1. Your applications (OTLP) — recommended

Point any OpenTelemetry SDK/exporter at the agent's OTLP endpoint. **One endpoint
carries all three signals** — metrics, logs, and traces flow over the same
`:4317`/`:4318` receiver:

- Host (macOS / Linux / Windows): `http://localhost:4318` (HTTP) or `localhost:4317` (gRPC)
- Kubernetes: `http://leansignal-agent.<namespace>.svc:4318` (or `:4317`)

```bash
# typical OTel SDK environment — one endpoint, all three signals
export OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318
export OTEL_METRICS_EXPORTER=otlp
export OTEL_LOGS_EXPORTER=otlp
export OTEL_TRACES_EXPORTER=otlp
```

## 2. Prometheus exporters (node_exporter, cAdvisor, app `/metrics`)

Add a `prometheus` receiver that scrapes the exporter, and add it to the
`metrics/all` pipeline.

### Install node_exporter (Linux)

```bash
VER=1.8.2
curl -fsSL "https://github.com/prometheus/node_exporter/releases/download/v${VER}/node_exporter-${VER}.linux-amd64.tar.gz" | tar -xz -C /tmp
sudo install -m0755 "/tmp/node_exporter-${VER}.linux-amd64/node_exporter" /usr/local/bin/
sudo tee /etc/systemd/system/node_exporter.service >/dev/null <<'EOF'
[Unit]
Description=Prometheus Node Exporter
[Service]
ExecStart=/usr/local/bin/node_exporter
Restart=always
[Install]
WantedBy=multi-user.target
EOF
sudo systemctl enable --now node_exporter        # serves http://127.0.0.1:9100/metrics
```

On Windows, install [`windows_exporter`](https://github.com/prometheus-community/windows_exporter)
(serves `:9182`); on macOS, node_exporter runs the same way (`:9100`).

### Wire it into the agent config

Add the receiver and include it in the `metrics/all` pipeline:

```yaml
receivers:
  prometheus:
    config:
      scrape_configs:
        - job_name: node_exporter
          scrape_interval: 15s
          static_configs:
            - targets: ['127.0.0.1:9100']    # windows_exporter: 127.0.0.1:9182

service:
  pipelines:
    metrics/all:
      receivers: [otlp, hostmetrics, prometheus]   # <- add prometheus
```

Then apply the change (see [Applying config changes](configuration.md#applying-config-changes)).
The same pattern scrapes cAdvisor, blackbox_exporter, or any app that exposes
Prometheus metrics — just add more `scrape_configs` targets.

## 3. Log shippers (promtail, Grafana Alloy)

Already running a Loki-style log shipper? The agent has a `loki` push receiver on
`:3500` (HTTP) and `:3600` (gRPC), so promtail, Grafana Alloy, or anything that
speaks Loki's push API can send to it unchanged — no re-instrumentation. Point the
shipper's Loki client at the agent's push path:

- Host: `http://localhost:3500/loki/api/v1/push`
- Kubernetes: `http://leansignal-agent.<namespace>.svc:3500/loki/api/v1/push`

Logs arriving this way join the same `logs/all` pipeline as OTLP logs: written to
the local Loki in full, with only the demanded stream selectors forwarded on to
the tenant Loki. Apps that can emit OTLP logs directly should prefer the OTLP
endpoint in section 1.

## 4. Kubernetes cluster & nodes

`kubeletstats` + `k8s_cluster` are on when you install via the chart (toggle with
`receivers.kubeletStats` / `receivers.k8sCluster`). To also pull
**kube-state-metrics** or a **node-exporter DaemonSet**, add a `prometheus` scrape
job (targeting their in-cluster Services) to the config via a values override.
Apps in the cluster send OTLP (metrics, logs, and traces) to
`leansignal-agent.<namespace>.svc:4318`; log shippers push to
`leansignal-agent.<namespace>.svc:3500` (the `loki` receiver, on by default —
toggle with `receivers.loki`).

## Verify a source is flowing

Each signal lands in its own local store; query the matching one (host ports
shown; on K8s port-forward the store):

```bash
# METRICS — names now present in the local VictoriaMetrics (:8428)
curl -s http://localhost:8428/api/v1/label/__name__/values
# e.g. after adding node_exporter you'll see node_* series:
curl -s 'http://localhost:8428/api/v1/query?query=node_cpu_seconds_total' | head -c 300

# LOGS — stream labels now present in the local Loki (:3100)
curl -s http://localhost:3100/loki/api/v1/labels

# TRACES — recent traces in the local Tempo (query API :3200)
curl -s 'http://localhost:3200/api/search?limit=5'
```
