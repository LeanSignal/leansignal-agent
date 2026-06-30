# Install on Kubernetes

The agent ships as a Helm chart that deploys the collector and (optionally) a
co-located VictoriaMetrics via the upstream `victoria-metrics-single` subchart.

## Install

```bash
helm upgrade --install leansignal-agent \
  oci://ghcr.io/leansignal/charts/leansignal-agent \
  --namespace leansignal --create-namespace \
  --set leansignal.endpoint="wss://api.leansignal.com/api/v1/agents/ws/" \
  --set leansignal.agentKey.value="YOUR_KEY" \
  --set dataplane.endpoint="https://dataplane.example.com/api/v1/write" \
  --set victoria-metrics-single.enabled=true
```

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
  endpoint: wss://api.leansignal.com/api/v1/agents/ws/
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
```

## What gets created

A Deployment (collector), ConfigMap (rendered config), ServiceAccount, a
ClusterRole/Binding for the `k8s_cluster` + `kubeletstats` receivers, a Secret
(unless you supply one), an OTLP Service, and — when enabled — the
VictoriaMetrics StatefulSet/Service.

## Verify

```bash
kubectl -n leansignal rollout status deploy/leansignal-agent
kubectl -n leansignal logs deploy/leansignal-agent -f
```

## Uninstall

```bash
helm uninstall leansignal-agent -n leansignal
```
