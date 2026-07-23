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
     The collector's own self-metrics arrive via OTLP (see service.telemetry),
     so the otlp receiver covers them — no dedicated self-scrape receiver. */}}
{{- define "leansignal-agent.metricsReceivers" -}}
{{- $r := list -}}
{{- if .Values.receivers.otlp.enabled -}}{{- $r = append $r "otlp" -}}{{- end -}}
{{- if .Values.receivers.k8sCluster.enabled -}}{{- $r = append $r "k8s_cluster" -}}{{- end -}}
{{- if .Values.receivers.kubeletStats.enabled -}}{{- $r = append $r "kubeletstats" -}}{{- end -}}
{{- if and (eq (include "leansignal-agent.mode" .) "central") .Values.localStores.scrape.enabled -}}{{- $r = append $r "prometheus/localstores" -}}{{- end -}}
{{- join ", " $r -}}
{{- end -}}

{{/* host:port scrape targets for the co-located stores' own /metrics pages —
     the query-endpoint URLs with the scheme stripped. */}}
{{- define "leansignal-agent.localVMScrapeTarget" -}}
{{- include "leansignal-agent.localVMQueryEndpoint" . | trimPrefix "https://" | trimPrefix "http://" -}}
{{- end -}}
{{- define "leansignal-agent.localLokiScrapeTarget" -}}
{{- include "leansignal-agent.localLokiQueryEndpoint" . | trimPrefix "https://" | trimPrefix "http://" -}}
{{- end -}}
{{- define "leansignal-agent.localTempoScrapeTarget" -}}
{{- include "leansignal-agent.localTempoQueryEndpoint" . | trimPrefix "https://" | trimPrefix "http://" -}}
{{- end -}}

{{/* Comma-separated list of enabled receivers for the logs/all pipeline. */}}
{{- define "leansignal-agent.logsReceivers" -}}
{{- $r := list -}}
{{- if .Values.receivers.otlp.enabled -}}{{- $r = append $r "otlp" -}}{{- end -}}
{{- if .Values.receivers.loki.enabled -}}{{- $r = append $r "loki" -}}{{- end -}}
{{- join ", " $r -}}
{{- end -}}

{{/* NOTE: the backend host endpoints (control/dataplane/loki/tempo) are no longer
     derived by the chart — the ${leansignal:...} confmap provider derives them in
     the agent binary from leansignal.tenant (+ resolved/pinned region). The chart
     only sets LEANSIGNAL_TENANT/DOMAIN/CC_URL/RESOLVE_AAT and optional per-host
     pin env vars; see templates/deployment.yaml. */}}

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

{{/* Local Loki OTLP logs write endpoint: explicit value, else the bundled
     aloki Deployment's service, else localhost. */}}
{{- define "leansignal-agent.localLokiEndpoint" -}}
{{- if .Values.localLoki.writeEndpoint -}}
{{- .Values.localLoki.writeEndpoint -}}
{{- else if .Values.localLoki.deploy -}}
{{- printf "http://%s-loki.%s.svc:%v/otlp/v1/logs" (include "leansignal-agent.fullname" .) .Release.Namespace .Values.localLoki.service.port -}}
{{- else -}}
{{- "http://127.0.0.1:3100/otlp/v1/logs" -}}
{{- end -}}
{{- end -}}

{{/* Local Loki query base (no /otlp/v1/logs): explicit value, else the write
     endpoint with its OTLP push path trimmed, else the bundled aloki service,
     else localhost. */}}
{{- define "leansignal-agent.localLokiQueryEndpoint" -}}
{{- if .Values.localLoki.queryEndpoint -}}
{{- .Values.localLoki.queryEndpoint -}}
{{- else if .Values.localLoki.writeEndpoint -}}
{{- trimSuffix "/otlp/v1/logs" .Values.localLoki.writeEndpoint -}}
{{- else if .Values.localLoki.deploy -}}
{{- printf "http://%s-loki.%s.svc:%v" (include "leansignal-agent.fullname" .) .Release.Namespace .Values.localLoki.service.port -}}
{{- else -}}
{{- "http://127.0.0.1:3100" -}}
{{- end -}}
{{- end -}}

{{/* Local Tempo OTLP traces write endpoint: explicit value, else the bundled
     atempo Deployment's service (OTLP HTTP port), else localhost on 4328. */}}
{{- define "leansignal-agent.localTempoEndpoint" -}}
{{- if .Values.localTempo.writeEndpoint -}}
{{- .Values.localTempo.writeEndpoint -}}
{{- else if .Values.localTempo.deploy -}}
{{- printf "http://%s-tempo.%s.svc:%v/v1/traces" (include "leansignal-agent.fullname" .) .Release.Namespace .Values.localTempo.service.otlpPort -}}
{{- else -}}
{{- "http://127.0.0.1:4328/v1/traces" -}}
{{- end -}}
{{- end -}}

{{/* Local Tempo query base (no /v1/traces): explicit value, else the bundled
     atempo service (query port), else localhost. The query API is a different
     port from OTLP ingest, so it cannot be derived from writeEndpoint. */}}
{{- define "leansignal-agent.localTempoQueryEndpoint" -}}
{{- if .Values.localTempo.queryEndpoint -}}
{{- .Values.localTempo.queryEndpoint -}}
{{- else if .Values.localTempo.deploy -}}
{{- printf "http://%s-tempo.%s.svc:%v" (include "leansignal-agent.fullname" .) .Release.Namespace .Values.localTempo.service.queryPort -}}
{{- else -}}
{{- "http://127.0.0.1:3200" -}}
{{- end -}}
{{- end -}}

