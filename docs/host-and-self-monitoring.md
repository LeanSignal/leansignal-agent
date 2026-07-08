# Host metrics (macOS) & monitoring the collector

This page shows a minimal way to confirm the agent collects host metrics on
**macOS** (tested on macOS Tahoe / Apple silicon and Intel), and how to monitor
the collector itself.

## 1. Quick check: macOS host metrics to your terminal

The fastest "does it work" test needs no backend — it prints metrics to the
console using the `debug` exporter. Save as `macos-demo.yaml`:

```yaml
receivers:
  hostmetrics:
    collection_interval: 10s
    scrapers:
      cpu: {}
      memory: {}
      disk: {}
      filesystem: {}
      load: {}
      network: {}
      paging: {}

processors:
  batch: {}

exporters:
  debug:
    verbosity: detailed

service:
  pipelines:
    metrics:
      receivers: [hostmetrics]
      processors: [batch]
      exporters: [debug]
```

Run it with a locally built agent (see [development.md](development.md)):

```bash
make build
./_build/leansignal-agent --config macos-demo.yaml
```

Within ~10s you'll see metrics like `system.cpu.time`, `system.memory.usage`,
`system.filesystem.usage`, `system.disk.io`, `system.network.io`,
`system.cpu.load_average.1m` scroll past. That confirms collection works.

> **macOS scraper support:** `cpu`, `load`, `memory`, `disk`, `filesystem`,
> `network`, and `paging` work on macOS. The `processes` and `process` scrapers
> are **Linux/Windows-only** and will fail to start on macOS — don't add them in a
> macOS config. (The shared `config/agent-config.example.yaml` intentionally omits
> `processes` so it's portable.)

## 2. Store macOS metrics in VictoriaMetrics

To query/graph them, write to a local VictoriaMetrics instead of the console.
Start a VM and swap the exporter:

```bash
docker run --rm -p 8428:8428 victoriametrics/victoria-metrics:latest
```

```yaml
exporters:
  prometheusremotewrite/local:
    endpoint: http://127.0.0.1:8428/api/v1/write
    tls:
      insecure: true

service:
  pipelines:
    metrics:
      receivers: [hostmetrics]
      processors: [batch]
      exporters: [prometheusremotewrite/local]
```

Then query:

```bash
curl -s 'http://localhost:8428/api/v1/query?query=system_memory_usage_bytes' | jq .
curl -s http://localhost:8428/api/v1/label/__name__/values | jq . | grep system_
```

The full agent (`config/agent-config.example.yaml`) already includes the
`hostmetrics` receiver, so a normal install collects these automatically — this
standalone config is just for a quick isolated check.

## 3. Monitoring the collector itself

The agent monitors itself **out of the box** — you don't need to add anything.
Every shipped config exposes the collector's internal telemetry on
`127.0.0.1:8888` (level `detailed`), scrapes it with a `prometheus/internal`
receiver, and feeds it into the `metrics/all` pipeline. So the collector's own
health (throughput, queue depth, export failures, memory) plus a set of
LeanSignal-specific metrics (index cache sizes, control-stream connectivity) flow
to the local VictoriaMetrics automatically — indexed and demand-filtered like any
other metric.

Query it the same way as host metrics, e.g.:

```bash
curl -s 'http://127.0.0.1:8428/api/v1/query?query=leansignal_edgecontroller_connection_up' | jq .
curl -s 'http://127.0.0.1:8428/api/v1/query?query=otelcol_exporter_send_failed_metric_points_total' | jq .
```

For the full list of what's exposed, what each metric means, and the top signals
to alert on, see **[Agent own telemetry](own-telemetry.md)**.
