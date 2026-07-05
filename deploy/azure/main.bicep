// SPDX-License-Identifier: Apache-2.0
//
// Sluicio "cell" on Azure Container Apps.
//
// Deploys the two stateless Go services as Container Apps:
//   - cell-api    : API (external HTTPS ingress on 8081). Runs the alert /
//                   trace / log evaluators + catalog reconciler IN-PROCESS
//                   with NO leader election, so it is pinned to a SINGLE
//                   replica. Do not raise maxReplicas until those loops are
//                   extracted into the separate cell-alerting service.
//   - cell-ingest : OTLP/HTTP receiver (external HTTPS ingress on 4318).
//                   Stateless → autoscales on HTTP concurrency.
//
// NOT deployed here (use managed / external services — ACA is the wrong
// home for stateful stores):
//   - Postgres  → Azure Database for PostgreSQL Flexible Server (POSTGRES_DSN)
//   - ClickHouse→ ClickHouse Cloud (Azure) or ClickHouse on AKS/VM
//   - Prometheus→ Azure Monitor managed Prometheus (only for metric alerts)
//   - Frontend  → the cell-api image is API-only; host the SPA on Azure
//                 Static Web Apps and proxy /api to the cell-api FQDN.
//
// Deploy:
//   az deployment group create -g <rg> -f deploy/azure/main.bicep \
//     -p acrLoginServer=<acr>.azurecr.io acrName=<acr> imageTag=<tag> \
//        postgresDsn='postgres://…' clickhouseEndpoint='host:9440' \
//        clickhousePassword='…'

targetScope = 'resourceGroup'

@description('Azure region for all resources.')
param location string = resourceGroup().location

@description('Prefix for resource names.')
param prefix string = 'sluicio'

@description('ACR login server, e.g. myreg.azurecr.io.')
param acrLoginServer string

@description('Existing ACR name (for the AcrPull role assignment).')
param acrName string

@description('Image tag to deploy for both services.')
param imageTag string = 'latest'

@description('Postgres DSN, e.g. postgres://user:pass@host:5432/sluicio?sslmode=require')
@secure()
param postgresDsn string

@description('ClickHouse native endpoint host:port (e.g. xxx.azure.clickhouse.cloud:9440).')
param clickhouseEndpoint string
param clickhouseDatabase string = 'telemetry'
param clickhouseUsername string = 'default'
@secure()
param clickhousePassword string

// Log Analytics workspace — required backing store for the ACA environment.
resource law 'Microsoft.OperationalInsights/workspaces@2023-09-01' = {
  name: '${prefix}-logs'
  location: location
  properties: {
    sku: { name: 'PerGB2018' }
    retentionInDays: 30
  }
}

// Container Apps environment. For production, add VNet integration
// (infrastructureSubnetId) so the apps reach managed Postgres + ClickHouse
// over private endpoints.
resource cae 'Microsoft.App/managedEnvironments@2024-03-01' = {
  name: '${prefix}-env'
  location: location
  properties: {
    appLogsConfiguration: {
      destination: 'log-analytics'
      logAnalyticsConfiguration: {
        customerId: law.properties.customerId
        sharedKey: law.listKeys().primarySharedKey
      }
    }
  }
}

resource acr 'Microsoft.ContainerRegistry/registries@2023-07-01' existing = {
  name: acrName
}

// Built-in AcrPull role.
var acrPullRoleId = subscriptionResourceId('Microsoft.Authorization/roleDefinitions', '7f951dda-4ed3-4680-a7ca-43fe172d538d')

// ── cell-api ────────────────────────────────────────────────────────────
resource cellApi 'Microsoft.App/containerApps@2024-03-01' = {
  name: '${prefix}-cell-api'
  location: location
  identity: { type: 'SystemAssigned' }
  properties: {
    managedEnvironmentId: cae.id
    configuration: {
      activeRevisionsMode: 'Single'
      ingress: {
        external: true
        targetPort: 8081
        transport: 'auto'
        allowInsecure: false
      }
      registries: [
        { server: acrLoginServer, identity: 'system' }
      ]
      secrets: [
        { name: 'postgres-dsn', value: postgresDsn }
        { name: 'clickhouse-password', value: clickhousePassword }
      ]
    }
    template: {
      containers: [
        {
          name: 'cell-api'
          image: '${acrLoginServer}/cell-api:${imageTag}'
          resources: { cpu: json('1.0'), memory: '2Gi' }
          env: [
            { name: 'CELL_API_ADDR', value: ':8081' }
            { name: 'POSTGRES_DSN', secretRef: 'postgres-dsn' }
            { name: 'CLICKHOUSE_ENDPOINT', value: clickhouseEndpoint }
            { name: 'CLICKHOUSE_DATABASE', value: clickhouseDatabase }
            { name: 'CLICKHOUSE_USERNAME', value: clickhouseUsername }
            { name: 'CLICKHOUSE_PASSWORD', secretRef: 'clickhouse-password' }
          ]
        }
      ]
      // SINGLE replica: in-process evaluators have no leader election, and
      // scale-to-zero would freeze the background loops. Do not change.
      scale: { minReplicas: 1, maxReplicas: 1 }
    }
  }
}

// ── cell-ingest (OTLP/HTTP) ───────────────────────────────────────────────
resource cellIngest 'Microsoft.App/containerApps@2024-03-01' = {
  name: '${prefix}-cell-ingest'
  location: location
  identity: { type: 'SystemAssigned' }
  properties: {
    managedEnvironmentId: cae.id
    configuration: {
      activeRevisionsMode: 'Single'
      ingress: {
        external: true
        targetPort: 4318
        transport: 'auto'
        allowInsecure: false
      }
      registries: [
        { server: acrLoginServer, identity: 'system' }
      ]
      secrets: [
        { name: 'clickhouse-password', value: clickhousePassword }
      ]
    }
    template: {
      containers: [
        {
          name: 'cell-ingest'
          image: '${acrLoginServer}/cell-ingest:${imageTag}'
          resources: { cpu: json('0.5'), memory: '1Gi' }
          env: [
            { name: 'CELL_INGEST_ADDR', value: ':4318' }
            { name: 'CLICKHOUSE_ENDPOINT', value: clickhouseEndpoint }
            { name: 'CLICKHOUSE_DATABASE', value: clickhouseDatabase }
            { name: 'CLICKHOUSE_USERNAME', value: clickhouseUsername }
            { name: 'CLICKHOUSE_PASSWORD', secretRef: 'clickhouse-password' }
            // Metrics: when enabling metric alerts, add the cell-ingest
            // Prometheus remote-write env var here (confirm its name in
            // services/cell-ingest config).
          ]
        }
      ]
      scale: {
        minReplicas: 1
        maxReplicas: 5
        rules: [
          { name: 'http-concurrency', http: { metadata: { concurrentRequests: '100' } } }
        ]
      }
    }
  }
}

// ── AcrPull for both app identities ──────────────────────────────────────
// NOTE: on the very first deploy the image pull can fail until the role
// assignment propagates (~seconds–minutes). If a revision is stuck on
// "image pull", restart it once RBAC has propagated.
resource cellApiPull 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(acr.id, cellApi.id, acrPullRoleId)
  scope: acr
  properties: {
    principalId: cellApi.identity.principalId
    roleDefinitionId: acrPullRoleId
    principalType: 'ServicePrincipal'
  }
}
resource cellIngestPull 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(acr.id, cellIngest.id, acrPullRoleId)
  scope: acr
  properties: {
    principalId: cellIngest.identity.principalId
    roleDefinitionId: acrPullRoleId
    principalType: 'ServicePrincipal'
  }
}

output cellApiUrl string = 'https://${cellApi.properties.configuration.ingress.fqdn}'
output cellIngestUrl string = 'https://${cellIngest.properties.configuration.ingress.fqdn}'
