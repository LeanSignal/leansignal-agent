# Install on Kubernetes

The agent ships as a Helm chart that deploys the collector and (optionally) a
co-located VictoriaMetrics via the upstream `victoria-metrics-single` subchart.

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

To override the derived hosts, set `leansignal.endpoint` / `dataplane.endpoint`
explicitly instead of (or alongside) `leansignal.tenant`.

Or with a values file (see [`values-example.yaml`](../deploy/helm/leansignal-agent/values-example.yaml)):

```bash
helm upgrade --install leansignal-agent \
  oci://ghcr.io/leansignal/charts/leansignal-agent \
  -n leansignal --create-namespace -f my-values.yaml
```

There is also a convenience wrapper: [`scripts/install/k8s-install.sh`](../scripts/install/k8s-install.sh).

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

## What gets created

A Deployment (collector), ConfigMap (rendered config), ServiceAccount, a
ClusterRole/Binding for the `k8s_cluster` + `kubeletstats` receivers, a Secret
(unless you supply one), an OTLP Service, and — when enabled — the
VictoriaMetrics StatefulSet/Service.

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

Send your own app metrics to the in-cluster OTLP service
`leansignal-agent.leansignal.svc:4317` (gRPC) / `:4318` (HTTP).

## Uninstall

```bash
helm uninstall leansignal-agent -n leansignal
```
