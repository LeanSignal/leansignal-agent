# Install on Kubernetes

The agent ships as a Helm chart that deploys the collector and collects
**telemetry** — metrics, logs, and traces. It bundles a co-located store for each
signal: **VictoriaMetrics** for metrics (the upstream `victoria-metrics-single`
subchart, enable with `--set victoria-metrics-single.enabled=true`), plus
**Loki** and **Tempo** Deployments for logs and traces, both on by default in
central mode. Any of them can be swapped for a store you already run — see
[Logs & traces](#logs--traces) and
[Bring your own VictoriaMetrics](#bring-your-own-victoriametrics).

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

### Editing the config from the LeanSignal app

By default the config **cannot** be edited from the app (Agents → the agent →
**Configuration**): Kubernetes mounts a ConfigMap volume **read-only**, and no
flag changes that, so the agent reports the file as non-writable and the editor
opens read-only. Reading always works.

To make it editable, give the config its own volume instead:

```yaml
config:
  writable: true
  # size: 16Mi          # the PVC is tiny; most provisioners round up anyway
  # storageClass: ""    # empty = cluster default
```

The chart then provisions a small **PVC** and an init container seeds it **once**
from the ConfigMap on first start. From then on the config is editable from the
app, from `leanctl`, or through the `agent_config_update` MCP tool — and a saved
config is validated, backed up as `config.yaml.bak`, and applied by the agent
**restarting itself** — it logs `restarting to reload the config`, exits with
status 75, and the kubelet brings the container straight back with the new
config. Expect a couple of seconds of collection gap; the co-located stores keep
everything they already received.

**The trade-off, stated plainly:** after that first boot the volume is the source
of truth, so config changes made through `helm upgrade` **no longer reach the
agent**. That is the point — you cannot have both the chart and the UI owning the
same file. Consequences worth knowing:

- To hand control back to the chart, delete `config.yaml` from the volume (or
  delete the PVC) and restart the pod; it re-seeds from the ConfigMap.
- The Deployment switches to the `Recreate` strategy, because a ReadWriteOnce
  claim cannot be attached to the old and new pod at once. Expect a few seconds
  of downtime on upgrades — the co-located stores keep running, so nothing that
  already reached them is lost.
- `helm uninstall` deletes the PVC and any edits with it.

Two things to check for your cluster before enabling it:

- **The seed image.** The volume is populated by a one-shot init container, which
  needs a shell — the agent image is distroless and has none. It defaults to
  `busybox:1.37.0`, the chart's **only** image outside `ghcr.io`. On a mirrored
  or air-gapped cluster, point it at your own registry: `config.seedImage:
  my-registry.internal/busybox:1.37.0` (anything with `/bin/sh` and `cp`). It
  runs as the agent's own uid, not root, so it is admissible in a namespace
  enforcing the `restricted` Pod Security Standard.
- **Your storage class must honour `fsGroup`.** That is what makes the volume
  writable by the nonroot agent. A few NFS/CIFS and CSI drivers ignore it; there
  the volume stays root-owned and the agent correctly reports the config as
  non-writable. Local-path, EBS, Longhorn and the common CSI drivers are fine.

**Leave this off when the config is managed by GitOps** (ArgoCD, Flux). There the
repository should stay the source of truth: an edit made in the app would be
reverted by the next sync, so the read-only default is the honest behaviour —
change the config where it is defined and let the sync roll it out.

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
/ `traces.enabled`), and so are the local stores that back them: `localLoki.deploy`
and `localTempo.deploy` both default to **true**, so a bundled Loki and Tempo
(ClusterIP, ~1h window each) are deployed alongside the collector in central mode.
The agent writes every log stream / span to the local store and forwards only the
demanded subset to the tenant store — the same pattern as metrics.

**To bring your own** Loki/Tempo, set its `writeEndpoint`; that alone disables the
bundled store (you are pointing the agent elsewhere):

```yaml
localLoki:
  # OTLP logs push endpoint (…/otlp/v1/logs) of a Loki you run.
  writeEndpoint: http://loki.monitoring.svc:3100/otlp/v1/logs
  # queryEndpoint is derived from writeEndpoint (…/otlp/v1/logs trimmed); set it
  # explicitly only if your query API is elsewhere.

localTempo:
  # OTLP traces push endpoint (…/v1/traces) of a Tempo you run.
  writeEndpoint: http://tempo.monitoring.svc:4318/v1/traces
  # queryEndpoint has NO derivation (the query API is a different port), so set it:
  queryEndpoint: http://tempo.monitoring.svc:3200
```

**To store nothing locally**, set `localLoki.deploy=false` / `localTempo.deploy=false`
without a `writeEndpoint`. Telemetry still reaches your tenant, but there is no
local buffer and nothing to explore before demanding — and the agent will log
connection-refused errors against the unused in-pod endpoints. To switch a signal
off entirely instead, use `--set logs.enabled=false` / `--set traces.enabled=false`.

**Storage.** Both bundled stores use an `emptyDir` by default, so a pod restart
drops the local window — acceptable for a ~1h buffer, and it never affects what
LeanSignal already holds. Give either one a PVC if you'd rather it survive:

```yaml
localLoki:
  persistence: { enabled: true, size: 5Gi, storageClassName: "" }
localTempo:
  persistence: { enabled: true, size: 5Gi, storageClassName: "" }
```

The windows match the host installs: Loki `max_query_lookback: 1h` (exact), Tempo
`block_retention: 1h` (approximate). In-cluster Tempo takes OTLP on **4318** — the
host installs use 4328 only because the collector owns 4317/4318 there, which is
not a conflict in a separate pod.

The **tenant** logs/traces ingest hosts are derived from the tenant slug just like
the metrics dataplane (override with `logs.tenantEndpoint` /
`traces.tenantEndpoint`); the exporters append `/otlp/v1/logs` and `/v1/traces`.
As with metrics, LeanSignal reads the local stores over the gRPC tunnel via their
`queryEndpoint`.

The chart also exposes the agent's **Loki push receiver** (promtail/alloy-style
shippers) on the OTLP Service — ports **3500** (HTTP) / **3600** (gRPC) — whenever
`logs.enabled` and the `loki` receiver are on (central mode).

## What gets created

Every name below is `<release>-leansignal-agent…` — this chart's fullname helper
always prefixes the release name, so the documented install (release
`leansignal-agent`) yields the doubled `leansignal-agent-leansignal-agent`.

| Resource | Name (release `leansignal-agent`) |
|---|---|
| Deployment + ConfigMap + Service (collector) | `leansignal-agent-leansignal-agent` |
| Deployment + ConfigMap + Service (local Loki) | `leansignal-agent-leansignal-agent-loki` |
| Deployment + ConfigMap + Service (local Tempo) | `leansignal-agent-leansignal-agent-tempo` |
| StatefulSet + Service (local VictoriaMetrics) | `leansignal-agent-victoria-metrics-single-server` |

Plus a ServiceAccount, a ClusterRole/Binding for the `k8s_cluster` +
`kubeletstats` receivers, and a Secret (unless you supply one). With
`config.writable: true` there is also a PersistentVolumeClaim
`leansignal-agent-leansignal-agent-config` holding the editable config. The OTLP Service
carries the Loki push receiver ports **3500**/**3600** when logs are enabled. The
rendered config carries the metrics, logs and traces pipelines (logs/traces in
central mode). The Loki, Tempo and VictoriaMetrics resources appear only while
their store is enabled — see [Logs & traces](#logs--traces) and
[Bring your own VictoriaMetrics](#bring-your-own-victoriametrics).

## It's already collecting

Once the pod is running, **Kubernetes cluster + node metrics (and OTLP) are
collected automatically** and written to the co-located VictoriaMetrics — nothing
else to configure. Verify:

```bash
kubectl -n leansignal rollout status deploy/leansignal-agent-leansignal-agent
kubectl -n leansignal logs deploy/leansignal-agent-leansignal-agent -f     # connection + index sync counts

# query the local store via a port-forward
kubectl -n leansignal port-forward svc/leansignal-agent-victoria-metrics-single-server 8428:8428 &
curl -s http://127.0.0.1:8428/api/v1/label/__name__/values
```

Send your own app telemetry (metrics, and — once you've wired up a local
Loki/Tempo above — logs and traces) to the in-cluster OTLP service
`leansignal-agent-leansignal-agent.leansignal.svc:4317` (gRPC) / `:4318` (HTTP); log shippers can
push to the Loki receiver on `:3500` (HTTP) / `:3600` (gRPC).

## Manage

Names below assume the documented release name `leansignal-agent` — see
[What gets created](#what-gets-created) for why they are doubled.

```bash
# STATUS — everything the chart owns
kubectl -n leansignal get pods,deploy,sts,svc,cm

# LIVE LOGS (per component)
kubectl -n leansignal logs deploy/leansignal-agent-leansignal-agent -f        # collector
kubectl -n leansignal logs deploy/leansignal-agent-leansignal-agent-loki -f   # local log store
kubectl -n leansignal logs deploy/leansignal-agent-leansignal-agent-tempo -f  # local trace store
kubectl -n leansignal logs sts/leansignal-agent-victoria-metrics-single-server -f
kubectl -n leansignal logs deploy/leansignal-agent-leansignal-agent -f --previous   # after a crash

# RESTART (rolling; PVCs and store data are retained)
kubectl -n leansignal rollout restart deploy/leansignal-agent-leansignal-agent
kubectl -n leansignal rollout status  deploy/leansignal-agent-leansignal-agent
kubectl -n leansignal rollout restart deploy/leansignal-agent-leansignal-agent-loki
kubectl -n leansignal rollout restart deploy/leansignal-agent-leansignal-agent-tempo
kubectl -n leansignal rollout restart sts/leansignal-agent-victoria-metrics-single-server

# REACH A LOCAL STORE from your machine
kubectl -n leansignal port-forward svc/leansignal-agent-victoria-metrics-single-server 8428:8428
kubectl -n leansignal port-forward svc/leansignal-agent-leansignal-agent-loki 3100:3100
kubectl -n leansignal port-forward svc/leansignal-agent-leansignal-agent-tempo 3200:3200
kubectl -n leansignal port-forward deploy/leansignal-agent-leansignal-agent 13133:13133   # agent health
```

> **Keep `replicaCount: 1`.** The edge controller and metrics tracker assume one
> process per agent — scaling the Deployment breaks index sync.

### Configuration

Configuration is **Helm values**, not files edited in place — the collector config
is a rendered ConfigMap, so hand-editing it is undone by the next `helm upgrade`
unless you own it out-of-band via `config.existingConfigMap`, or move it onto its
own volume with `config.writable: true` and edit it from the LeanSignal app
(see [Config persistence](#config-persistence--owning-the-config)).

```bash
# see the rendered collector config
kubectl -n leansignal get cm leansignal-agent-leansignal-agent -o jsonpath='{.data.config\.yaml}'

# change a value and roll it out
helm upgrade leansignal-agent oci://ghcr.io/leansignal/charts/leansignal-agent \
  --namespace leansignal --reuse-values --set <key>=<value>
```

A `helm upgrade` that changes the ConfigMap restarts the pod automatically; if you
rotate an external Secret instead, restart it yourself with `rollout restart`.

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
`kubectl -n leansignal rollout restart deploy/leansignal-agent-leansignal-agent`.

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
