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

{{/* Comma-separated list of enabled receivers for the metrics/all pipeline */}}
{{- define "leansignal-agent.metricsReceivers" -}}
{{- $r := list -}}
{{- if .Values.receivers.otlp.enabled -}}{{- $r = append $r "otlp" -}}{{- end -}}
{{- if .Values.receivers.k8sCluster.enabled -}}{{- $r = append $r "k8s_cluster" -}}{{- end -}}
{{- if .Values.receivers.kubeletStats.enabled -}}{{- $r = append $r "kubeletstats" -}}{{- end -}}
{{- join ", " $r -}}
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
