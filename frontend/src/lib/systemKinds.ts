// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// The known "system" kinds a service can be flagged as. The kind drives the
// label/icon in the Systems view + the System badge, and (later) which built-in
// monitoring template applies. "" = a system with no specific kind; "other" is
// the explicit catch-all.

export const SYSTEM_KINDS: { value: string; label: string }[] = [
  { value: "rabbitmq", label: "RabbitMQ" },
  { value: "activemq", label: "ActiveMQ" },
  { value: "artemis", label: "ActiveMQ Artemis" },
  { value: "azure-servicebus", label: "Azure Service Bus" },
  { value: "kafka", label: "Apache Kafka" },
  { value: "confluent-kafka", label: "Confluent Kafka" },
  { value: "nats", label: "NATS" },
  { value: "debezium", label: "Debezium" },
  { value: "krakend", label: "KrakenD API Gateway" },
  { value: "wso2-apim", label: "WSO2 API Manager" },
  { value: "redis", label: "Redis" },
  { value: "sqlserver", label: "SQL Server" },
  { value: "postgresql", label: "PostgreSQL" },
  { value: "mysql", label: "MySQL" },
  { value: "mongodb", label: "MongoDB" },
  { value: "elasticsearch", label: "Elasticsearch" },
  { value: "other", label: "Other" },
];

export function systemKindLabel(kind: string | undefined): string {
  if (!kind) return "System";
  return SYSTEM_KINDS.find((k) => k.value === kind)?.label ?? kind;
}

// Kinds that ship a built-in monitoring template (the apply-template endpoint
// creates its checks on the flagged service). Mirror of the backend catalog's
// System:true entries (system_templates.go). RabbitMQ/KrakenD are grounded in
// real metrics; the rest are grounded in the receivers'/exporters' documented
// names — tune after applying.
export const TEMPLATE_KINDS = new Set([
  "rabbitmq",
  "artemis",
  "azure-servicebus",
  "krakend",
  "kafka",
  "confluent-kafka",
  "nats",
  "debezium",
  "wso2-apim",
]);

export function hasSystemTemplate(kind: string | undefined): boolean {
  return !!kind && TEMPLATE_KINDS.has(kind);
}

// Service-type templates — applied to any service (not just flagged systems)
// via the general apply-template endpoint. Mirror of the backend catalog's
// non-system kinds; used by the manual picker. Auto-detection (the metric-name
// suggestions) returns its own labels, so this is only the fallback list.
export const SERVICE_TEMPLATE_KINDS: { value: string; label: string }[] = [
  { value: "otel-collector", label: "OpenTelemetry Collector" },
  { value: "dotnet-service", label: ".NET service" },
];

export function templateKindLabel(kind: string | undefined): string {
  if (!kind) return "service";
  return (
    SERVICE_TEMPLATE_KINDS.find((k) => k.value === kind)?.label ??
    SYSTEM_KINDS.find((k) => k.value === kind)?.label ??
    kind
  );
}
