# Agent own telemetry (self-monitoring)

The LeanSignal Agent monitors **itself**. The underlying OpenTelemetry Collector
emits its own internal telemetry — metrics (throughput, queue depth, export
failures, memory), its own logs, and (a few) traces — and the edge controller
adds a handful of LeanSignal-specific metrics (index cache sizes, control-stream
connectivity). All of it is collected **by default** — no extra configuration —
and stored alongside your application telemetry in the local stores
(VictoriaMetrics / Loki / Tempo).

## How it's wired (on by default)

Every shipped config (`config/agent-config.*.yaml`, the Docker config, and the
Helm chart) routes the collector's own telemetry — **metrics, logs, and traces** —
through OTLP to the agent's **own loopback OTLP receiver** (`127.0.0.1:4317`) via
`service.telemetry`. From there it flows through the same `*/all` pipelines as any
other telemetry.

```yaml
service:
  telemetry:
    metrics:
      level: detailed
      readers:
        - periodic:
            interval: 15000        # milliseconds
            exporter: { otlp: { protocol: grpc, endpoint: http://127.0.0.1:4317 } }
    logs:
      level: info
      processors:
        - batch: { exporter: { otlp: { protocol: grpc, endpoint: http://127.0.0.1:4317 } } }
    traces:
      processors:
        - batch: { exporter: { otlp: { protocol: grpc, endpoint: http://127.0.0.1:4317 } } }
  pipelines:
    # self-metrics arrive on the otlp receiver — no dedicated self-scrape receiver
    metrics/all: { receivers: [otlp, hostmetrics], ... }
```

Because self-telemetry rides the same pipelines as everything else, it is:

- **stored in full** locally — metrics in VictoriaMetrics, logs in Loki, traces in
  Tempo (all short-retention, queryable via **"Available"**),
- **indexed** by the tracker (metrics show up in the metric index), and
- **demandable** — reference any of it from a dashboard/alert and it flows to the
  central dataplane / tenant store (**"Stored"**), exactly like application telemetry.

The agent's own `service.name` is **`leansignal-agent`**, so
`{service_name="leansignal-agent"}` (logs) or `service.name = "leansignal-agent"`
(traces) selects the agent itself.

> Logs still **also** go to stderr/journald (`journalctl -u leansignal-agent`,
> `kubectl logs`) — adding the OTLP path does not silence the console.

## Querying it

Against the local VM (default `127.0.0.1:8428`, dev `:8482`), or through the
LeanSignal UI query tunnel:

```bash
# Is the control stream up right now?
curl -s 'http://127.0.0.1:8428/api/v1/query?query=leansignal_edgecontroller_connection_up' | jq .

# List every self-telemetry name currently stored
curl -s http://127.0.0.1:8428/api/v1/label/__name__/values \
  | jq -r '.data[]' | grep -E '^(leansignal_|otelcol_|http_client_)'
```

## Top signals to watch

If you alert on nothing else, watch these:

| Signal | Why it matters |
|---|---|
| `leansignal_edgecontroller_connection_up == 0` (sustained) | Agent has lost its control stream to LeanSignal — no demand updates, no index sync, no UI queries. |
| `rate(otelcol_exporter_send_failed_metric_points_total[5m]) > 0` | Writes to a store are failing — data loss risk (check per `exporter` label: `local` vs `dataplane`). |
| `otelcol_exporter_queue_size / otelcol_exporter_queue_capacity` near 1 | Export queue is backing up (slow/unreachable backend); the next step is dropped data. |
| `rate(otelcol_receiver_refused_metric_points_total[5m]) > 0` | The agent is rejecting incoming data (backpressure / limits). |
| `leansignal_edgecontroller_pending_backend_updates` high & rising | The metric index isn't draining to the backend (sync lag or a slow/disconnected control plane). |
| `otelcol_process_memory_rss_bytes` trending up | Collector memory growth — capacity/leak watch. |
| `rate(leansignal_edgecontroller_connection_attempts_total[5m])` spiking | Control stream is flapping (repeated reconnects). |

---

## Reference

Metric names below are as they appear in VictoriaMetrics. Histograms are exported
as three series — `<name>_bucket`, `<name>_sum`, `<name>_count`; only the base
name is listed. Counters carry a `_total` suffix and appear once they first
increment. Everything is present at the shipped `detailed` level.

### LeanSignal edge controller (custom)

These are unique to this distribution — the health of the LeanSignal control
plane and the metric index the agent maintains.

| Metric | Type | Meaning |
|---|---|---|
| `leansignal_edgecontroller_connection_up` | gauge | `1` while the gRPC control stream to LeanSignal is connected, else `0`. Always present. |
| `leansignal_edgecontroller_connection_attempts_total` | counter | Control-stream dial attempts. `rate()` rising = the stream is flapping/reconnecting. |
| `leansignal_edgecontroller_connection_established_total` | counter | Successful (re)connects of the control stream. Appears after the first connect; each increment is one reconnect. |
| `leansignal_edgecontroller_known_timeseries_cache_size` | gauge | Distinct timeseries the agent tracks in its full known index. Roughly your local active-series count. |
| `leansignal_edgecontroller_discovered_timeseries_cache_size` | gauge | Newly discovered series queued to announce to the backend (drains as `IndexCreate` is acked). Growing = backend not draining the index. |
| `leansignal_edgecontroller_demand_timeseries_cache_size` | gauge | Number of series names the backend currently demands. `0` = nothing is being forwarded to the central dataplane (fail-closed). |
| `leansignal_edgecontroller_pending_backend_updates` | gauge | Known-index entries with changes not yet acknowledged by the backend (`IndexUpdate` backlog). Sustained high = index-sync lag. |

### Exporters — getting data out

Per-exporter via the `exporter` label — these are the metrics pipeline's
exporters: `prometheusremotewrite/local` (everything → local VM) and
`prometheusremotewrite/dataplane` (demanded subset → central). The logs and
traces pipelines add their own Loki/Tempo exporter labels under the same
`otelcol_exporter_*` families (log-record and span counters instead of
metric-point counters).

| Metric | Type | Meaning |
|---|---|---|
| `otelcol_exporter_sent_metric_points_total` | counter | Points successfully written to the store. |
| `otelcol_exporter_send_failed_metric_points_total` | counter | Points that failed to write. **Any sustained rate here is data-loss risk.** |
| `otelcol_exporter_queue_size` | gauge | Points/batches currently queued for send. |
| `otelcol_exporter_queue_capacity` | gauge | Max queue size. Watch `queue_size / queue_capacity`. |
| `otelcol_exporter_queue_batch_send_size` | histogram | Size distribution of batches leaving the sending queue. |
| `otelcol_exporter_prometheusremotewrite_sent_batches_total` | counter | Remote-write batches sent (remote-write exporter specific). |
| `otelcol_exporter_prometheusremotewrite_translated_time_series_total` | counter | OTLP→Prometheus series translated for remote-write. |
| `otelcol_exporter_prometheusremotewrite_consumers` | gauge | Active remote-write consumer goroutines. |

### Receivers — data coming in

Per-receiver via the `receiver` label — the metrics pipeline's receivers:
`otlp` (which now also carries the agent's own OTLP self-metrics) and
`hostmetrics`. The logs and traces pipelines add their own receiver labels under
the same `otelcol_receiver_*` families.

| Metric | Type | Meaning |
|---|---|---|
| `otelcol_receiver_accepted_metric_points_total` | counter | Points accepted into the pipeline. Your ingest throughput. |
| `otelcol_receiver_refused_metric_points_total` | counter | Points refused (backpressure, limits). |
| `otelcol_receiver_failed_metric_points_total` | counter | Points that failed inside the receiver. |

### Scrapers (hostmetrics)

| Metric | Type | Meaning |
|---|---|---|
| `otelcol_scraper_scraped_metric_points_total` | counter | Points pulled by the host-metrics scrapers. |
| `otelcol_scraper_errored_metric_points_total` | counter | Points a scraper failed to collect. |

### Batch processor

| Metric | Type | Meaning |
|---|---|---|
| `otelcol_processor_batch_batch_send_size` | histogram | Distribution of batch sizes flushed downstream. |
| `otelcol_processor_batch_metadata_cardinality` | gauge | Distinct metadata (context) combinations the batcher is tracking. |
| `otelcol_processor_batch_timeout_trigger_send_total` | counter | Batches flushed because the timeout fired (vs. reaching size). |

### Process / Go runtime

| Metric | Type | Meaning |
|---|---|---|
| `otelcol_process_uptime_seconds_total` | counter | Seconds since start; a reset means the agent restarted. |
| `otelcol_process_cpu_seconds_total` | counter | Total CPU time consumed. |
| `otelcol_process_memory_rss_bytes` | gauge | Resident memory. Primary memory-health signal. |
| `otelcol_process_runtime_heap_alloc_bytes` | gauge | Go heap currently allocated. |
| `otelcol_process_runtime_total_sys_memory_bytes` | gauge | Total memory obtained from the OS by the Go runtime. |
| `otelcol_process_runtime_alloc_bytes_total` | counter | Cumulative bytes allocated (allocation pressure). |

### Remote-write HTTP client (detailed level)

Latency and payload size of the actual HTTP writes to the stores — the clearest
view of how the local VM and the central dataplane are responding.

| Metric | Type | Meaning |
|---|---|---|
| `http_client_request_duration_seconds` | histogram | Remote-write request latency. Watch p95/p99 for a slow store. |
| `http_client_request_body_size_bytes` | histogram | Remote-write request payload sizes. |

### gRPC (detailed level, traffic-dependent)

The OTLP gRPC **receiver** emits `rpc_server_*` histograms (call latency, messages
per RPC) when applications push over gRPC. These are **absent until there is gRPC
traffic** — an idle agent, or one receiving only host metrics and OTLP/HTTP, shows
none — so confirm the exact names once a gRPC producer is connected. (The edge
controller's own outbound control-stream client is not instrumented, so it
contributes no `rpc_*` metrics.)

> **No more `up` / `scrape_*` self-scrape meta.** Self-metrics are now pushed over
> OTLP rather than scraped, so the Prometheus scrape-meta series (`up`,
> `scrape_duration_seconds`, `scrape_samples_scraped`, …) that a `prometheus`
> receiver used to synthesize are no longer produced. Use
> `leansignal_edgecontroller_connection_up` and the `otelcol_exporter_*` failure
> counters as the health canaries instead.

## Notes

- **All three signals self-report.** The collector's own metrics, logs *and*
  traces are pushed via OTLP (see [How it's wired](#how-its-wired-on-by-default)):
  metrics land in the local VictoriaMetrics, the agent's own logs in the local
  Loki (`{service_name="leansignal-agent"}`), and its (sparse) internal spans in
  the local Tempo. The `otelcol_*` metric families additionally carry `receiver` /
  `exporter` label values for the Loki/Tempo exporters and the log/trace receivers.
- **Identity labels:** because self-telemetry flows through `metrics/all`, every
  series here also carries the `leansignal_agent_name` / `host_name` / `os_type` labels (see
  [Configuration](configuration.md#identity-labels)) — so the central store can
  attribute each agent's own health.
- **Naming:** custom edge-controller metrics keep their own name
  (`leansignal_edgecontroller_*`); the `otelcol_` prefix belongs only to the
  collector's built-in core metrics.
- **Counters are born on first increment.** A counter that has never fired (e.g.
  `..._connection_established_total` on an agent that has never reached the
  backend) simply has no series yet — use the always-present
  `leansignal_edgecontroller_connection_up` gauge to observe current state.
- **`detailed` level** is what enables the `http_client_*` / `rpc_*` histograms.
  Dropping to `normal` in `service.telemetry.metrics` removes those but keeps all
  the counters and gauges above.
