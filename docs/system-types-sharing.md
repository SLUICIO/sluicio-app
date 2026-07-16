<!-- SPDX-License-Identifier: Apache-2.0 -->

# Sharing system types

A system type — its detection prefixes and starter health checks — is a
portable document. Export one as a single YAML (or JSON) file, put it
in a gist or a GitHub repo, and anyone can import it into their own
Sluicio cell. Built-ins export too, so the natural community loop is:
fork the closest built-in, tune it for your broker/gateway/runtime,
share the file.

## Export

System types → **Export** on any row, or:

```
GET /api/v1/system-types/{key}/export            # YAML (default)
GET /api/v1/system-types/{key}/export?format=json
```

## Import

System types → **Import…** (any editor), or:

```
POST /api/v1/system-types/import          # body: the YAML or JSON document
POST /api/v1/system-types/import?replace=true   # overwrite your existing org type
```

Importing never touches the built-in catalog: a file whose key matches
a built-in becomes your org's **override** of it (the normal
customization path); a new key becomes a custom type. A key that
already has an org row answers 409 until you pass `replace=true`.
Documents are strictly validated — unknown signals/severities, missing
names, or oversized files are rejected with a specific message.

## The format — `sluicio/system-type/v1`

```yaml
format: sluicio/system-type/v1
key: mosquitto              # lowercase letters, digits, . _ - (max 63)
label: Eclipse Mosquitto
is_system: true             # appears in the Systems view
detect_prefixes:            # metric-name prefixes that auto-identify the type
  - mosquitto.
checks:
  # metric check (signal omitted or "metric")
  - name: Dropped messages
    signal: metric
    metric: mosquitto.messages.dropped
    agg: increase           # aggregation over the window
    op: ">"
    threshold: 0
    severity: warning
    unit: msgs
    split_by: listener      # evaluate per distinct attribute value
    attrs:                  # optional attribute predicates
      - { key: listener, op: eq, value: "1883" }

  # log check
  - name: Error logs
    signal: log
    min_severity: 17        # OTLP severity floor (17 ≈ error)
    log_threshold: 5        # matches over the window that fire

  # trace checks
  - name: Failed traces
    signal: trace_error     # fires on >= trace_threshold failed traces
    trace_threshold: 3
    window_seconds: 600
    severity: critical
  - name: Slow requests
    signal: trace_latency   # fires when p95 latency >= threshold_ms
    threshold_ms: 2000
  - name: Traffic present
    signal: trace_volume    # dead-man's switch: fires when traces drop BELOW
    trace_threshold: 1
```

The `format` field is required and versioned — files published in the
wild keep working when the schema evolves.

## The community repo

[**SLUICIO/sluicio-system-types**](https://github.com/SLUICIO/sluicio-system-types)
is the shared collection — seeded with Sluicio's six built-ins and open
to PRs. If your type could help anyone else, publish it there.

## Sharing etiquette

- Name the file `<key>.systemtype.yaml`.
- Don't put credentials or internal hostnames in check names or
  attribute values — the file is meant to travel.
- A short comment header (`# what this monitors, which versions it was
  tested against`) makes a shared type much more adoptable.
