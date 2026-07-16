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
app.kubernetes.io/part-of: sluicio
app.kubernetes.io/managed-by: {{ .root.Release.Service }}
{{- end -}}

{{- define "cell.selectorLabels" -}}
app.kubernetes.io/name: {{ include "cell.name" .root }}
app.kubernetes.io/instance: {{ .root.Release.Name }}
app.kubernetes.io/component: {{ .component }}
{{- end -}}

{{/* Image reference: tag defaults to the chart's appVersion. */}}
{{- define "cell.image" -}}
{{- printf "%s:%s" .image.repository (default .root.Chart.AppVersion .image.tag) -}}
{{- end -}}

{{- define "cell.serviceAccountName" -}}
{{- if .Values.serviceAccount.create -}}
{{- default (include "cell.fullname" .) .Values.serviceAccount.name -}}
{{- else -}}
{{- default "default" .Values.serviceAccount.name -}}
{{- end -}}
{{- end -}}

{{- define "cell.imagePullSecrets" -}}
{{- with .Values.global.imagePullSecrets }}
imagePullSecrets:
{{- range . }}
  - name: {{ . }}
{{- end }}
{{- end }}
{{- end -}}

{{/*
Public URL defaults: app.appUrl / app.ingestUrl win; otherwise derive from
the ingress/route hosts so a plain `ingress.host: sluicio.acme.com` install
gets working email deep links and SSO redirect bases without repeating the
hostname.
*/}}
{{- define "cell.appUrl" -}}
{{- if .Values.app.appUrl -}}
{{- .Values.app.appUrl -}}
{{- else if and .Values.ingress.enabled .Values.ingress.host -}}
https://{{ .Values.ingress.host }}
{{- else if and .Values.route.enabled .Values.route.host -}}
https://{{ .Values.route.host }}
{{- end -}}
{{- end -}}

{{- define "cell.ingestUrl" -}}
{{- if .Values.app.ingestUrl -}}
{{- .Values.app.ingestUrl -}}
{{- else if and .Values.ingress.ingest.enabled .Values.ingress.ingest.host -}}
https://{{ .Values.ingress.ingest.host }}
{{- else if and .Values.route.enabled .Values.route.ingestHost -}}
https://{{ .Values.route.ingestHost }}
{{- end -}}
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
{{ required "set postgres.dsn (or postgres.enabled: true for the bundled instance)" .Values.postgres.dsn }}
{{- end -}}
{{- end -}}

{{- define "cell.clickhouseEndpoint" -}}
{{- if .Values.clickhouse.enabled -}}
{{ .Release.Name }}-clickhouse:9000
{{- else -}}
{{ required "set clickhouse.endpoint (or clickhouse.enabled: true for the bundled instance)" .Values.clickhouse.endpoint }}
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

{{/* Shared DB env block for cell-api / cell-ingest. */}}
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

{{/*
Secret-backed env var: emits `name` from an existing Secret when
`existingSecret` is set, else from the chart's app Secret when an inline
value is set, else nothing. Call with:
  dict "root" $ "name" "SLUICIO_LICENSE_KEY" "cfg" .Values.license "appKey" "license"
*/}}
{{- define "cell.secretEnv" -}}
{{- if .cfg.existingSecret }}
- name: {{ .name }}
  valueFrom:
    secretKeyRef:
      name: {{ .cfg.existingSecret }}
      key: {{ .cfg.secretKey }}
{{- else if .cfg.key }}
- name: {{ .name }}
  valueFrom:
    secretKeyRef:
      name: {{ .root.Release.Name }}-app
      key: {{ .appKey }}
{{- end -}}
{{- end -}}
