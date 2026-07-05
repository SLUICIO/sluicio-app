# controlplane

The shared control plane. Owns the global directory: organizations, users,
memberships, invitations, billing, and the mapping of which tenants live
in which cells.

Backed by Postgres. SQL migrations live in
[`./migrations`](./migrations).

In SaaS this is the front door to the product. On-premise deployments do
not run a control plane — the cell stands alone.

License: FSL-1.1-Apache-2.0.
