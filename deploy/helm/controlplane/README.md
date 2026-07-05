# integration-monitor-controlplane

Helm chart for the Integration Monitor control plane.

This chart is **FSL-1.1-Apache-2.0** because it is the deployment shape
of the SaaS-side product. On-premise customers should install the
[`cell`](../cell) chart (Apache 2.0) instead — they do not need a
control plane.

## Install

```bash
helm install controlplane ./deploy/helm/controlplane \
  --set postgres.dsn="postgres://..." \
  --set keycloak.url="https://auth.example.com"
```

See [`values.yaml`](./values.yaml) for the full list of values. This
chart is a skeleton; real templates will be added as the services are
implemented.
