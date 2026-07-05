# Group access policies (Enterprise `rbac_advanced`)

Two independent axes control what a user can do:

- **Org role** (`admin` / `editor` / `viewer`) — *capability*: what you may do.
- **Groups + access policies** — *visibility*: which services you may see.

A user's visible service set is the **union** of what every policy on every
group they belong to grants. No policy → they see nothing (deny by default).
Admins bypass visibility entirely. Policies never grant capability, and there
are **no deny rules** — a policy can only ever add to what a user sees, so one
group can't hide what another group granted.

## Policy kinds

| Kind | Grants |
|---|---|
| `service` | one named service |
| `integration` | every service in an integration (via its matchers) |
| `system` | every service flagged as a system, optionally one system kind |
| `attributes` | services whose resource attributes match **all** the given key=value pairs (AND) |
| `compound` | an integration/service target **AND** an attribute filter |
| `all_org` | everything in the org (wildcard) |
| `expression` | an arbitrary boolean tree — see below |

## Expression policies

The `expression` kind carries a boolean tree in `conditions`, giving a single
policy full AND / OR / NOT logic over two kinds of leaf:

- **service-name leaf** — no `attr` field; matches the service name.
- **attribute leaf** — `attr` is the resource-attribute key.

**Operators**: `equals`, `not_equals`, `prefix`, `suffix`, `contains`,
`regex` (RE2 — linear-time, safe), `in` (value ∈ `values`), and — attribute
leaves only — `exists` / `not_exists`.

Example — *services starting with `ABC`, on team orders or payments, but not
the sandbox ones*:

```json
{
  "kind": "expression",
  "conditions": {
    "op": "and",
    "children": [
      { "match": "prefix", "value": "ABC" },
      { "op": "or", "children": [
        { "attr": "team", "match": "equals", "value": "orders" },
        { "attr": "team", "match": "equals", "value": "payments" }
      ]},
      { "op": "not", "children": [
        { "attr": "env", "match": "equals", "value": "sandbox" }
      ]}
    ]
  }
}
```

### Negation and missing attributes

This is the one subtlety worth stating explicitly, because both readings are
useful and the operators let you pick:

- `not_equals` on `env` (or `NOT (env = X)`) matches a service that has
  `env ≠ X` **or has no `env` attribute at all** — the intuitive "everything
  except the X ones".
- To require the attribute to be present *and* differ, AND an `exists` leaf:
  `(env exists) AND (env not_equals X)`.

### Guarantees

- **Fail closed** — a malformed, empty, or over-deep tree resolves to *no*
  services, never "all".
- **Org-scoped `NOT`** — complement is taken against the org's own service
  universe only; it can never surface another tenant's services.
- **Bounded** — trees are capped at 24 levels deep / 256 nodes; regexes must
  compile at write time (400 otherwise).

Evaluation: each leaf resolves to a set of service names, `and` intersects,
`or` unions, `not` complements against the org universe; the result is
UNIONed with the user's other policies.
