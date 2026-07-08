# Service facets

A **facet** describes *what a service does* in terms of the data boundaries
it crosses — receiving files, publishing to a queue, serving HTTP, writing to
a database, and so on. Sluicio reads each service's telemetry, works out which
facets fit, and then tailors the service's dashboard to match: a file-input
service shows pickup rates and source hosts; an HTTP service shows routes and
status codes; a queue consumer shows messages processed per destination.

Facets are what turn a flat pile of spans into a dashboard that looks like
*your* service. This guide covers what they are, how detection works, and the
three ways you can steer it — **overrides**, **classification rules**, and
**custom facets**.

> A service can carry **zero, one, or many** facets — they aren't mutually
> exclusive. A gateway that receives files and republishes them to a queue is
> both `file-input` and `queue-output`, and its dashboard stacks both sets of
> widgets. Every service also always carries the built-in **`core`** facet (a
> baseline overview), so a service is never "facet-less".

## The built-in facets

| Facet | What it means | Detected from |
|---|---|---|
| `file-input` | Receives files (FTP/SFTP/SMB/local) | `io.kind=file`, `io.role=input` |
| `file-output` | Sends files to a remote/share | `io.kind=file`, `io.role=output` |
| `queue-input` | Consumes messages from a queue/topic | `io.kind=queue`, `io.role=input` |
| `queue-output` | Publishes messages to a queue/topic | `io.kind=queue`, `io.role=output` |
| `stream-input` | Consumes from Kafka/Kinesis/Event Hubs | `io.kind=stream`, `io.role=input` |
| `stream-output` | Produces to a data stream | `io.kind=stream`, `io.role=output` |
| `http-input` | Handles inbound HTTP requests | `io.kind=http`, `io.role=input` |
| `http-output` | Makes outbound HTTP calls | `io.kind=http`, `io.role=output` |
| `db-output` | Reads/writes a database | `io.kind=db`, `io.role=output` |
| `email-output` | Sends email (SMTP / provider API) | `io.kind=email`, `io.role=output` |
| `worker` | Background work only — internal spans, no I/O | `SpanKind=Internal` and no I/O facet |
| `core` | Always-on overview (spans, errors, throughput) | Always |

The classification comes down to two span attributes:

- **`io.kind`** — one of `file`, `queue`, `stream`, `http`, `db`, `email`
- **`io.role`** — `input` or `output`

## How auto-detection works

When you open a service, Sluicio samples its recent spans in the selected time
window and builds a *profile*: which span kinds it emits (Internal, Server,
Client…), which attribute keys appear, and which `(io.kind, io.role)` pairs it
carries. Each built-in facet has a rule that fires against that profile — e.g.
`file-input` fires when the profile contains `io.kind=file` + `io.role=input`.

Two consequences worth knowing:

- **Detection follows the window.** If a service only did file transfers last
  week, widen the time range and its `file-input` facet reappears.
- **You don't have to instrument `io.kind` yourself if it's inconvenient** —
  you can derive it from attributes you *do* emit (see
  [Classification rules](#classifying-services-that-dont-emit-iokind) below).

### Instrumenting for clean detection

The most reliable path is to set `io.kind` and `io.role` on the spans that
cross a boundary. Alongside them, a handful of well-known attributes make the
per-facet widgets richer (they're surfaced first on trace views too):

| Facet | Helpful span attributes |
|---|---|
| `file-input` / `file-output` | `file.name`, `transfer.source.host`, `transfer.source.path`, `transfer.protocol` |
| `http-input` / `http-output` | `http.route`, `http.method`, `http.status_code`, `net.peer.name` |
| `queue-*` / `stream-*` | `messaging.system`, `messaging.destination.name`, `messaging.operation` |
| `db-output` | `db.system`, `db.name`, `db.operation`, `db.sql.table` |

## Adjusting a service's facets (overrides)

Sometimes detection is wrong or incomplete — a service does file work that
isn't tagged, or a facet fires that you don't care about. **Overrides** let you
force a facet on or off for one service.

**Where:** the service detail page → **Service facets** card.

1. You'll see every facet as a checkbox, each labelled **detected**,
   **manual**, or **hidden**.
2. Tick a facet to force it **on** (an *include*); untick a detected one to
   force it **off** (an *exclude*).
3. Click **Save facets**. The dashboard re-renders with the new facet set.

The effective set is simply:

```
effective = (auto-detected ∪ your includes) − your excludes
```

Notes:

- **`core` can't be removed** — the overview is always available.
- Overrides are **deltas, not snapshots**: if a service later starts (or stops)
  emitting the telemetry for a facet, your include/exclude still applies on top
  of whatever detection now says. Nothing to clean up.
- A facet you *include* but that has no matching telemetry will show its
  widgets **empty** rather than inventing data — an honest "nothing here yet".

**Access:** anyone with view access to the service can *see* the facets;
changing them needs **contributor (editor)** role or above.

## Classifying services that don't emit `io.kind`

Many real services clearly do file / queue / HTTP work but never emit
`io.kind` / `io.role` — they carry domain attributes instead (`peer.service`,
`messaging.system`, `http.route`, …). **Facet classification rules** let you
say, for one service, *"treat spans where attribute X matches Y as
`io.kind=K`, `io.role=R`"* — without re-instrumenting anything.

**Where:** the service detail page → **Facet classification rules** card.

Each rule has:

- **Source** — `span` or `resource` attribute
- **Key** — e.g. `peer.service`, `messaging.system`
- **Operator** — `equals`, `prefix`, `suffix`, `contains`, or `is present`
- **Value** — the text to match (ignored for `is present`)
- **Result** — the `io.kind` + `io.role` to assign

**Example.** A transfer service emits `peer.service = sftp.bank.com` but no
`io.kind`. Add a rule:

> Span attribute `peer.service` **equals** `sftp.bank.com` → `io.kind=file`,
> `io.role=input`

The service now detects as **File input** and its dashboard shows file-input
widgets. Rules feed detection, so the normal facet matching runs on top of the
values you derived.

How rules behave:

- **Real telemetry always wins.** If a span already carries `io.kind`, the rule
  is ignored for that span — so adding proper instrumentation later
  automatically supersedes the rule, no coordination needed.
- Rules apply in the order you create them (first match wins).

**Access:** view needs service access; adding/removing rules needs
**contributor (editor)** or above. For the internals (data model, SQL, edge
cases) see [facet-mappings.md](facet-mappings.md).

## Custom facets

Beyond the built-ins, an organization can define its **own** facet labels —
e.g. "Data warehouse", "PCI-scoped", "Legacy". A custom facet is a
classification label for grouping and browsing; it has **no auto-detection and
no widgets** of its own, so it's always assigned by hand (an *include*
override on a service).

**Manage them** on the **Service facets** page (left nav → **Service facets**):
click **New facet**, give it a name + description (the slug is generated from
the name and is immutable). Then assign it to a service the same way as any
other facet — tick it in the **Service facets** card on the service detail page.

Custom-facet CRUD needs **contributor (editor)** role or above and is
org-global (not gated per service).

## Where facets pay off

- **Services list** — each service shows its facets at a glance, so you can
  scan the shape of a pipeline (file → queue → http) without opening anything.
- **Service dashboard** — one section per effective facet, in a fixed order,
  each with widgets tuned to that facet. Manually-assigned facets are badged so
  you can tell them from detected ones.
- **Browse by facet** — the **Service facets** page lists every facet; open one
  to see all services that carry it ("show me all the File Inputs in the org").
- **Integration flow** — a service's facets drive the compact pipeline glyph
  used on integration views, so a flow reads as `file → queue → http`.
- **Trace views** — the key attributes for a service's facets (`file.name`,
  `http.route`, …) surface first on its spans.

## Overrides vs rules vs custom facets — which do I use?

| You want to… | Use | Why |
|---|---|---|
| Force a built-in facet on/off for one service | **Override** | Direct, declarative — no telemetry needed |
| Get a service auto-classified from attributes it *does* emit | **Classification rule** | Data-driven — derives `io.kind`/`io.role`, then detection runs |
| Add an org-specific label for grouping/browsing | **Custom facet** | A label, assigned manually via an override |

Rule of thumb: reach for a **rule** when the right classification is derivable
from telemetry (it keeps working as the service scales); reach for an
**override** when it's a one-off human decision.

## API reference

All routes are org-scoped and require authentication. Reads need **viewer**;
writes need **contributor (editor)** or above.

| Method & path | Does |
|---|---|
| `GET /api/v1/service-facets` | List all facets (built-in + custom) with definitions & widgets |
| `GET /api/v1/service-facets/{slug}` | One facet + every service currently carrying it |
| `POST /api/v1/service-facets` | Create a custom facet `{name, description}` (slug auto-generated) |
| `PUT /api/v1/service-facets/{slug}` | Rename / re-describe a custom facet (slug is immutable) |
| `DELETE /api/v1/service-facets/{slug}` | Delete a custom facet (403 for built-ins) |
| `GET /api/v1/services/{name}/facet-overrides` | Facet vocab with detected / override / effective state |
| `PUT /api/v1/services/{name}/facet-overrides` | Replace the override set `{include:[…], exclude:[…]}` |
| `GET /api/v1/services/{name}/facet-mappings` | List a service's classification rules |
| `POST /api/v1/services/{name}/facet-mappings` | Add a rule (see body below) |
| `DELETE /api/v1/services/{name}/facet-mappings/{id}` | Delete one rule |
| `GET /api/v1/services/{name}/widgets` | Compute the service's effective facets + their widgets |

Classification-rule POST body:

```json
{
  "attribute_source": "span",
  "attribute_key":    "peer.service",
  "match_operator":   "equals",
  "match_value":      "sftp.bank.com",
  "set_io_kind":      "file",
  "set_io_role":      "input"
}
```

## Troubleshooting

- **"My service only shows as a Worker / Overview."** It isn't emitting
  `io.kind` / `io.role`. Either instrument those two attributes, or add a
  [classification rule](#classifying-services-that-dont-emit-iokind) from an
  attribute it already emits.
- **"A facet I added shows empty widgets."** You *included* a facet the service
  has no telemetry for. That's expected — it fills in once matching spans
  arrive, or you can untick it.
- **"A facet disappeared."** Detection is window-bounded — widen the time range,
  or pin it with an *include* override.
- **"I added a rule but nothing changed."** Rules only apply where the raw
  `io.kind` is absent (real telemetry wins), and detection re-runs on the
  current window — confirm spans in the window actually match your predicate.

## Related

- [facet-mappings.md](facet-mappings.md) — classification-rule internals (data
  model, SQL, edge cases)
- [systems.md](systems.md) — grouping services into monitored systems
  (orthogonal to facets)
- [tags.md](tags.md) — flat org-wide labels (orthogonal to facets)
