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

The OpenTelemetry Collector emits its own internal telemetry (throughput, queue
sizes, dropped data, memory). Expose it as Prometheus metrics via
`service.telemetry`:

```yaml
service:
  telemetry:
    metrics:
      level: detailed
      readers:
        - pull:
            exporter:
              prometheus:
                host: 127.0.0.1
                port: 8888
```

Now the collector's own metrics are at `http://127.0.0.1:8888/metrics` — e.g.
`otelcol_receiver_accepted_metric_points`, `otelcol_exporter_sent_metric_points`,
`otelcol_exporter_send_failed_metric_points`, `otelcol_processor_batch_batch_send_size`.

### Persisting collector self-metrics

To keep them, scrape that endpoint with a Prometheus receiver and route it to a
store. Add to the agent config:

```yaml
receivers:
  prometheus/collector:
    config:
      scrape_configs:
        - job_name: otelcol
          scrape_interval: 15s
          static_configs:
            - targets: ["127.0.0.1:8888"]
```

Then add `prometheus/collector` to the `metrics/all` pipeline's `receivers`. In
the full LeanSignal agent that means the collector's own health flows to the
local VictoriaMetrics (and is indexed / demand-filtered like any other metric):

```yaml
service:
  telemetry:
    metrics:
      level: detailed
      readers:
        - pull: { exporter: { prometheus: { host: 127.0.0.1, port: 8888 } } }
  pipelines:
    metrics/all:
      receivers: [otlp, hostmetrics, prometheus/collector]
      processors: [leansignalmetrics_tracker, batch]
      exporters: [prometheusremotewrite/local, forward/demand_filter]
```

Useful signals to alert on: `otelcol_exporter_send_failed_metric_points` (export
failures), `otelcol_exporter_queue_size` vs `otelcol_exporter_queue_capacity`
(backpressure), and `otelcol_process_memory_rss` (collector memory).
