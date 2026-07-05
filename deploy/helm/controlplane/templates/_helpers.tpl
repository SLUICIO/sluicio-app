{{/*
SPDX-License-Identifier: FSL-1.1-Apache-2.0

Common template helpers.
*/}}

{{- define "controlplane.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "controlplane.fullname" -}}
{{- $name := default .Chart.Name .Values.nameOverride -}}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "controlplane.labels" -}}
app.kubernetes.io/name: {{ include "controlplane.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/version: {{ .Chart.AppVersion }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/part-of: integration-monitor
{{- end -}}

{{- define "controlplane.selectorLabels" -}}
app.kubernetes.io/name: {{ include "controlplane.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}
