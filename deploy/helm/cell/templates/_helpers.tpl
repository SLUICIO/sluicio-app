{{/*
SPDX-License-Identifier: Apache-2.0

Common template helpers for the cell chart.
*/}}

{{- define "cell.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "cell.fullname" -}}
{{- printf "%s" .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "cell.componentName" -}}
{{- printf "%s-%s" (include "cell.fullname" .root) .component | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "cell.labels" -}}
app.kubernetes.io/name: {{ include "cell.name" .root }}
app.kubernetes.io/instance: {{ .root.Release.Name }}
app.kubernetes.io/version: {{ .root.Chart.AppVersion }}
app.kubernetes.io/component: {{ .component }}
app.kubernetes.io/part-of: integration-monitor
app.kubernetes.io/managed-by: {{ .root.Release.Service }}
{{- end -}}

{{- define "cell.selectorLabels" -}}
app.kubernetes.io/name: {{ include "cell.name" .root }}
app.kubernetes.io/instance: {{ .root.Release.Name }}
app.kubernetes.io/component: {{ .component }}
{{- end -}}

{{/*
Database wiring. Each service reads a single POSTGRES_DSN and the discrete
CLICKHOUSE_* vars. When postgres.enabled / clickhouse.enabled, point at the
cell-local instance this chart deploys; otherwise use the external values.
*/}}

{{- define "cell.postgresDSN" -}}
{{- if .Values.postgres.enabled -}}
postgres://{{ .Values.postgres.auth.username }}:{{ .Values.postgres.auth.password }}@{{ .Release.Name }}-postgres:5432/{{ .Values.postgres.auth.database }}?sslmode=disable
{{- else -}}
{{ .Values.postgres.dsn }}
{{- end -}}
{{- end -}}

{{- define "cell.clickhouseEndpoint" -}}
{{- if .Values.clickhouse.enabled -}}
{{ .Release.Name }}-clickhouse:9000
{{- else -}}
{{ .Values.clickhouse.endpoint }}
{{- end -}}
{{- end -}}

{{- define "cell.clickhouseUser" -}}
{{- if .Values.clickhouse.enabled -}}{{ .Values.clickhouse.auth.username }}{{- else -}}{{ .Values.clickhouse.username }}{{- end -}}
{{- end -}}

{{/* Name of the Secret holding the ClickHouse password ("" = none, unauthenticated). */}}
{{- define "cell.clickhousePasswordSecret" -}}
{{- if .Values.clickhouse.enabled -}}{{ .Release.Name }}-db{{- else -}}{{ .Values.clickhouse.passwordSecret }}{{- end -}}
{{- end -}}

{{- define "cell.clickhousePasswordKey" -}}
{{- if .Values.clickhouse.enabled -}}clickhouse-password{{- else -}}password{{- end -}}
{{- end -}}

{{/* Shared DB env block for cell-api / cell-ingest / cell-alerting. */}}
{{- define "cell.dbEnv" -}}
- name: POSTGRES_DSN
  value: {{ include "cell.postgresDSN" . | quote }}
- name: CLICKHOUSE_ENDPOINT
  value: {{ include "cell.clickhouseEndpoint" . | quote }}
- name: CLICKHOUSE_DATABASE
  value: {{ .Values.clickhouse.database | quote }}
- name: CLICKHOUSE_USERNAME
  value: {{ include "cell.clickhouseUser" . | quote }}
{{- $sec := include "cell.clickhousePasswordSecret" . -}}
{{- if $sec }}
- name: CLICKHOUSE_PASSWORD
  valueFrom:
    secretKeyRef:
      name: {{ $sec }}
      key: {{ include "cell.clickhousePasswordKey" . }}
{{- end -}}
{{- end -}}
