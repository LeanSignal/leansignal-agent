{{/* Common name helpers */}}
{{- define "leansignal-agent.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "leansignal-agent.fullname" -}}
{{- if .Values.fullnameOverride -}}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name (include "leansignal-agent.name" .) | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}

{{- define "leansignal-agent.labels" -}}
app.kubernetes.io/name: {{ include "leansignal-agent.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
{{- end -}}

{{- define "leansignal-agent.selectorLabels" -}}
app.kubernetes.io/name: {{ include "leansignal-agent.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{- define "leansignal-agent.serviceAccountName" -}}
{{- if .Values.serviceAccount.create -}}
{{- default (include "leansignal-agent.fullname" .) .Values.serviceAccount.name -}}
{{- else -}}
{{- default "default" .Values.serviceAccount.name -}}
{{- end -}}
{{- end -}}

{{- define "leansignal-agent.image" -}}
{{- $tag := .Values.image.tag | default .Chart.AppVersion -}}
{{- printf "%s:%s" .Values.image.repository $tag -}}
{{- end -}}

{{/* Secret name holding the agent key */}}
{{- define "leansignal-agent.agentKeySecretName" -}}
{{- if .Values.leansignal.agentKey.existingSecret -}}
{{- .Values.leansignal.agentKey.existingSecret -}}
{{- else -}}
{{- printf "%s-agent-key" (include "leansignal-agent.fullname" .) -}}
{{- end -}}
{{- end -}}

{{/* Deployment mode: "edge" when leansignal.mode=edge or a central URL is set,
     else "central". Edge is a lightweight OTLP forwarder to a central agent. */}}
{{- define "leansignal-agent.mode" -}}
{{- if or (eq .Values.leansignal.mode "edge") .Values.leansignal.centralAgentGrpcUrl -}}
edge
{{- else -}}
central
{{- end -}}
{{- end -}}

{{/* ConfigMap the agent mounts: an operator-managed one (survives helm upgrades)
     if config.existingConfigMap is set, else the chart-rendered ConfigMap. */}}
{{- define "leansignal-agent.configMapName" -}}
{{- if .Values.config.existingConfigMap -}}
{{- .Values.config.existingConfigMap -}}
{{- else -}}
{{- include "leansignal-agent.fullname" . -}}
{{- end -}}
{{- end -}}

{{/* Comma-separated list of enabled receivers for the metrics/all pipeline.
     prometheus/internal (the collector's own self-metrics) is always included. */}}
{{- define "leansignal-agent.metricsReceivers" -}}
{{- $r := list -}}
{{- if .Values.receivers.otlp.enabled -}}{{- $r = append $r "otlp" -}}{{- end -}}
{{- if .Values.receivers.k8sCluster.enabled -}}{{- $r = append $r "k8s_cluster" -}}{{- end -}}
{{- if .Values.receivers.kubeletStats.enabled -}}{{- $r = append $r "kubeletstats" -}}{{- end -}}
{{- $r = append $r "prometheus/internal" -}}
{{- join ", " $r -}}
{{- end -}}

{{/* Comma-separated list of enabled receivers for the logs/all pipeline. */}}
{{- define "leansignal-agent.logsReceivers" -}}
{{- $r := list -}}
{{- if .Values.receivers.otlp.enabled -}}{{- $r = append $r "otlp" -}}{{- end -}}
{{- if .Values.receivers.loki.enabled -}}{{- $r = append $r "loki" -}}{{- end -}}
{{- join ", " $r -}}
{{- end -}}

{{/* Control-plane gRPC endpoint: explicit value, else derived <tenant>-grpc.<domain>:443 */}}
{{- define "leansignal-agent.controlEndpoint" -}}
{{- if .Values.leansignal.endpoint -}}
{{- .Values.leansignal.endpoint -}}
{{- else if .Values.leansignal.tenant -}}
{{- printf "%s-grpc.%s:443" .Values.leansignal.tenant .Values.leansignal.domain -}}
{{- end -}}
{{- end -}}

{{/* Dataplane remote-write URL: explicit value, else derived <tenant>-ingest.<domain> */}}
{{- define "leansignal-agent.dataplaneEndpoint" -}}
{{- if .Values.dataplane.endpoint -}}
{{- .Values.dataplane.endpoint -}}
{{- else if .Values.leansignal.tenant -}}
{{- printf "https://%s-ingest.%s/api/v1/write" .Values.leansignal.tenant .Values.leansignal.domain -}}
{{- end -}}
{{- end -}}

{{/* Local VM write endpoint: explicit value, else the bundled subchart's service, else localhost */}}
{{- define "leansignal-agent.localVMEndpoint" -}}
{{- if .Values.localVM.writeEndpoint -}}
{{- .Values.localVM.writeEndpoint -}}
{{- else if (index .Values "victoria-metrics-single" "enabled") -}}
{{- printf "http://%s-victoria-metrics-single-server.%s.svc:8428/api/v1/write" .Release.Name .Release.Namespace -}}
{{- else -}}
{{- "http://127.0.0.1:8428/api/v1/write" -}}
{{- end -}}
{{- end -}}

{{/* Local VM query base (no /api/v1/write): explicit value, else the write endpoint
     with its remote-write path trimmed, else the bundled subchart's service. */}}
{{- define "leansignal-agent.localVMQueryEndpoint" -}}
{{- if .Values.localVM.queryEndpoint -}}
{{- .Values.localVM.queryEndpoint -}}
{{- else if .Values.localVM.writeEndpoint -}}
{{- trimSuffix "/api/v1/write" .Values.localVM.writeEndpoint -}}
{{- else if (index .Values "victoria-metrics-single" "enabled") -}}
{{- printf "http://%s-victoria-metrics-single-server.%s.svc:8428" .Release.Name .Release.Namespace -}}
{{- else -}}
{{- "http://127.0.0.1:8428" -}}
{{- end -}}
{{- end -}}

{{/* Local Loki OTLP logs write endpoint: explicit value, else localhost. */}}
{{- define "leansignal-agent.localLokiEndpoint" -}}
{{- if .Values.localLoki.writeEndpoint -}}
{{- .Values.localLoki.writeEndpoint -}}
{{- else -}}
{{- "http://127.0.0.1:3100/otlp/v1/logs" -}}
{{- end -}}
{{- end -}}

{{/* Local Loki query base (no /otlp/v1/logs): explicit value, else the write
     endpoint with its OTLP push path trimmed, else localhost. */}}
{{- define "leansignal-agent.localLokiQueryEndpoint" -}}
{{- if .Values.localLoki.queryEndpoint -}}
{{- .Values.localLoki.queryEndpoint -}}
{{- else if .Values.localLoki.writeEndpoint -}}
{{- trimSuffix "/otlp/v1/logs" .Values.localLoki.writeEndpoint -}}
{{- else -}}
{{- "http://127.0.0.1:3100" -}}
{{- end -}}
{{- end -}}

{{/* Tenant logs-ingest base URL: explicit value, else derived from the tenant —
     the same ingest host as the dataplane (path-routed to the tenant Loki).
     The exporter appends /otlp/v1/logs. */}}
{{- define "leansignal-agent.lokiEndpoint" -}}
{{- if .Values.logs.tenantEndpoint -}}
{{- .Values.logs.tenantEndpoint -}}
{{- else if .Values.leansignal.tenant -}}
{{- printf "https://%s-ingest.%s" .Values.leansignal.tenant .Values.leansignal.domain -}}
{{- end -}}
{{- end -}}

{{/* Local Tempo OTLP traces write endpoint: explicit value, else localhost on
     4328 (the agent collector owns 4317/4318 when co-located). */}}
{{- define "leansignal-agent.localTempoEndpoint" -}}
{{- if .Values.localTempo.writeEndpoint -}}
{{- .Values.localTempo.writeEndpoint -}}
{{- else -}}
{{- "http://127.0.0.1:4328/v1/traces" -}}
{{- end -}}
{{- end -}}

{{/* Local Tempo query base (no /v1/traces): explicit value, else localhost —
     the query API (3200) is a different port from OTLP ingest, so it cannot be
     derived from writeEndpoint the way the Loki one is. */}}
{{- define "leansignal-agent.localTempoQueryEndpoint" -}}
{{- if .Values.localTempo.queryEndpoint -}}
{{- .Values.localTempo.queryEndpoint -}}
{{- else -}}
{{- "http://127.0.0.1:3200" -}}
{{- end -}}
{{- end -}}

{{/* Tenant traces-ingest base URL: explicit value, else derived from the tenant —
     the same ingest host as the dataplane (path-routed to the tenant Tempo).
     The exporter appends /v1/traces. */}}
{{- define "leansignal-agent.tempoEndpoint" -}}
{{- if .Values.traces.tenantEndpoint -}}
{{- .Values.traces.tenantEndpoint -}}
{{- else if .Values.leansignal.tenant -}}
{{- printf "https://%s-ingest.%s" .Values.leansignal.tenant .Values.leansignal.domain -}}
{{- end -}}
{{- end -}}
