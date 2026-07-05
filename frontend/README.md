# frontend

The Integration Monitor web UI. React + TypeScript, built with Vite.

## Pages (v1 scaffold)

- **Integration health** (`/health`) — per-integration throughput, error
  rate, latency.
- **Topology** (`/topology`) — service graph for an integration scope.
- **Stuck messages** (`/stuck`) — oldest in-flight work per integration.
- **Alerts** (`/alerts`) — rules and currently firing alerts.

All pages are placeholders at this stage.

## Develop

```bash
npm install
npm run dev      # http://localhost:5173
npm run build    # type-check + production build
npm run lint
```

The dev server proxies `/api` to `http://localhost:8081` (the local
`cell-api`).

License: FSL-1.1-Apache-2.0.
