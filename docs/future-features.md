# Future features (post-1.0)

Deferred features that exist in some form in the codebase but aren't committed
to a release yet.

Nothing is currently deferred — the previously-listed items have shipped:

- **Topology** — multi-perspective relationship explorer at `/topology`:
  - *Services* — flat service dependency graph (`GET /api/v1/topology`,
    handlers_topology.go), rendered by IntegrationFlow.
  - *Integrations* / *Systems* / *Metadata* — drill-down trees (ExpandableTree):
    integration→services, system→services, and field→value→integration→service.
    Built client-side from `/integrations` (services[]), `/systems` (members),
    and `/metadata-graph` (handlers_metadata_graph.go). The topology endpoint
    also exposes `?view=integrations` (rolled-up dependency graph) for reuse.

Add new deferred features here as they appear.
