# Install on Kubernetes

The agent ships as a Helm chart that deploys the collector and collects
**telemetry** — metrics, logs, and traces. The chart bundles a co-located
**metrics** store (the upstream `victoria-metrics-single` subchart, optional);
there is **no bundled Loki or Tempo** — for logs and traces you point the agent
at a Loki/Tempo you run (see [Logs & traces](#logs--traces) below).

## Install

You only need your tenant + agent key; the gRPC control host and the ingest host
are derived (`<tenant>-grpc.<domain>` / `<tenant>-ingest.<domain>`, domain
defaults to `eu11.leansignal.io` — override with `--set leansignal.domain=…`).

```bash
helm upgrade --install leansignal-agent \
  oci://ghcr.io/leansignal/charts/leansignal-agent \
  --namespace leansignal --create-namespace \
  --set leansignal.tenant="YOUR_TENANT" \
  --set leansignal.agentKey.value="YOUR_KEY" \
  --set victoria-metrics-single.enabled=true
```

`leansignal.agentName` sets the `leansignal_agent_name` label; leave it unset to default to
the Kubernetes node name (`spec.nodeName`). The `k8s-install.sh` wrapper requires
`--agent-name` explicitly.

To override the derived hosts, set `leansignal.endpoint` / `dataplane.endpoint`
explicitly instead of (or alongside) `leansignal.tenant`.

Or with a values file (see [`values-example.yaml`](../deploy/helm/leansignal-agent/values-example.yaml)):

```bash
helm upgrade --install leansignal-agent \
  oci://ghcr.io/leansignal/charts/leansignal-agent \
  -n leansignal --create-namespace -f my-values.yaml
```

There is also a convenience wrapper: [`scripts/install/k8s-install.sh`](../scripts/install/k8s-install.sh).

## Edge mode (forward to a central agent)

An **edge** agent forwards OTLP to a central agent instead of running the full
pipeline — no local VM, tracker, demand filter, or control channel, so no tenant
or dataplane is needed. Set the central agent's OTLP endpoint:

```bash
helm upgrade --install leansignal-agent-edge \
  oci://ghcr.io/leansignal/charts/leansignal-agent \
  -n leansignal --create-namespace \
  --set leansignal.agentKey.value="YOUR_KEY" \
  --set leansignal.agentName="edge-cluster-1" \
  --set leansignal.centralAgentGrpcUrl="central-agent.central-ns.svc:4317"
```

Setting `leansignal.centralAgentGrpcUrl` (or `leansignal.mode=edge`) switches the
rendered pipeline to the edge forwarder and leaves the bundled VM off. The central
agent's OTLP Service must be reachable and is unauthenticated by design (keep it
in-cluster / on a trusted network). The wrapper equivalent is
`k8s-install.sh --agent-key KEY --agent-name NAME --central-url HOST:PORT`.

## Config persistence & owning the config

The chart renders the collector config into a **ConfigMap** (a standalone object),
so the config already **survives pod restarts and image/agent upgrades** — it is
not baked into the pod. A `helm upgrade` only rewrites it if your values change.

To own the config out-of-band so even `helm upgrade` never overwrites it — e.g.
you hand-edit it in the cluster — point the chart at a ConfigMap you manage
(mirrors `agentKey.existingSecret`):

```yaml
config:
  existingConfigMap: my-agent-config   # must contain a config.yaml key
```

The chart then renders no ConfigMap and mounts yours instead. (With a managed
ConfigMap the Deployment's `checksum/config` annotation rolls the pod on config
changes; with an external one you trigger the rollout yourself.)

## Using an existing Secret for the agent key

```yaml
leansignal:
  tenant: mb1
  agentKey:
    existingSecret: my-agent-secret
    existingSecretKey: agent-key
```

## Bring your own VictoriaMetrics

Disable the bundled subchart and point at your own store:

```yaml
victoria-metrics-single:
  enabled: false
localVM:
  writeEndpoint: http://my-vm.monitoring.svc:8428/api/v1/write
  # queryEndpoint is derived from writeEndpoint (with /api/v1/write trimmed) for
  # the edit-mode query tunnel; set it explicitly only if your query API is elsewhere:
  # queryEndpoint: http://my-vm.monitoring.svc:8428
```

The chart passes `queryEndpoint` to the agent as `local_vm_query_url` so LeanSignal
can read this store over the gRPC tunnel — it does not need to be exposed.

## Logs & traces

The chart's logs and traces pipelines are **enabled by default** (`logs.enabled`
/ `traces.enabled`), but — unlike metrics — the chart bundles **no** Loki or
Tempo. The agent writes every log stream / span to a *local* store and forwards
only the demanded subset to the tenant store, so on Kubernetes you point the
local endpoints at a Loki/Tempo **you run** (or the tenant provides):

```yaml
logs:
  enabled: true
localLoki:
  # OTLP logs push endpoint (…/otlp/v1/logs) of a Loki you run.
  writeEndpoint: http://loki.monitoring.svc:3100/otlp/v1/logs
  # queryEndpoint is derived from writeEndpoint (…/otlp/v1/logs trimmed); set it
  # explicitly only if your query API is elsewhere.

traces:
  enabled: true
localTempo:
  # OTLP traces push endpoint (…/v1/traces) of a Tempo you run.
  writeEndpoint: http://tempo.monitoring.svc:4318/v1/traces
  # queryEndpoint has NO derivation (the query API is a different port), so set it:
  queryEndpoint: http://tempo.monitoring.svc:3200
```

The **tenant** logs/traces ingest hosts are derived from the tenant just like the
metrics dataplane — `https://<tenant>-ingest.<domain>` (override with
`logs.tenantEndpoint` / `traces.tenantEndpoint`); the exporters append
`/otlp/v1/logs` and `/v1/traces`. As with metrics, LeanSignal can read the local
stores over the gRPC tunnel via their `queryEndpoint`.

If you don't run a local Loki/Tempo, disable those pipelines with
`--set logs.enabled=false --set traces.enabled=false` rather than leaving them
pointed at the default in-pod `localhost` endpoints, where nothing is listening.

The chart also exposes the agent's **Loki push receiver** (promtail/alloy-style
shippers) on the OTLP Service — ports **3500** (HTTP) / **3600** (gRPC) — whenever
`logs.enabled` and the `loki` receiver are on (central mode).

## What gets created

A Deployment (collector), ConfigMap (rendered config), ServiceAccount, a
ClusterRole/Binding for the `k8s_cluster` + `kubeletstats` receivers, a Secret
(unless you supply one), an OTLP Service (with the Loki push receiver ports
**3500**/**3600** added when logs are enabled), and — when enabled — the bundled
VictoriaMetrics StatefulSet/Service. The rendered config carries the metrics,
logs, and traces pipelines (logs/traces in central mode when enabled). **No Loki
or Tempo is created** — those are stores you run and point the agent at.

## It's already collecting

Once the pod is running, **Kubernetes cluster + node metrics (and OTLP) are
collected automatically** and written to the co-located VictoriaMetrics — nothing
else to configure. Verify:

```bash
kubectl -n leansignal rollout status deploy/leansignal-agent
kubectl -n leansignal logs deploy/leansignal-agent -f     # connection + index sync counts

# query the local store via a port-forward
kubectl -n leansignal port-forward svc/leansignal-agent-victoria-metrics-single-server 8428:8428 &
curl -s http://127.0.0.1:8428/api/v1/label/__name__/values
```

Send your own app telemetry (metrics, and — once you've wired up a local
Loki/Tempo above — logs and traces) to the in-cluster OTLP service
`leansignal-agent.leansignal.svc:4317` (gRPC) / `:4318` (HTTP); log shippers can
push to the Loki receiver on `:3500` (HTTP) / `:3600` (gRPC).

## Change the agent key or tenant

Key and tenant are Helm values — `helm upgrade` to change them (the VictoriaMetrics
PVC + data are retained):
```bash
helm upgrade leansignal-agent oci://ghcr.io/leansignal/charts/leansignal-agent \
  --namespace leansignal --reuse-values \
  --set leansignal.tenant=NEW_TENANT \
  --set leansignal.agentKey.value=NEW_KEY
```
If you supply the key via an existing Secret (see "Using an existing Secret" above),
rotate that Secret instead and restart:
`kubectl -n leansignal rollout restart deploy/leansignal-agent`.

## Upgrading

```bash
helm upgrade leansignal-agent oci://ghcr.io/leansignal/charts/leansignal-agent \
  --version <chart-version> --reuse-values
```
Bumping the chart `appVersion` upgrades the agent image; the VictoriaMetrics
StatefulSet + PVC are retained. See [Upgrading](upgrading.md) for the agent-only vs
VM distinction.

## Uninstall

```bash
helm uninstall leansignal-agent -n leansignal
```
