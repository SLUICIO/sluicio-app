# Build-in-public — infrastructure & pricing "it clicked" moment

**Pillar:** Build-in-Public · **Channel:** LinkedIn (personal profile)
**Goal:** founder authenticity + reinforce cost / no-lock-in / architecture positioning

> Framed as "open core + open-source infra, no license tax" — EE tier deliberately left out of this post (it's a vibe/story post, not a pricing page). Claim stays accurate without mentioning EE.

---

## LinkedIn version

For weeks the infrastructure didn't sit right. This week it finally clicked.

The thing I kept fighting: how do you run a monitoring platform that's cheap enough to operate *and* ready to run around the clock? Those usually pull in opposite directions — high availability means more moving parts, and more parts usually means a bigger bill.

The unlock was leaning all the way into how Sluicio is built. Every component runs as its own Docker container — ingest, the cell API, alerting, storage (ClickHouse), metrics (Prometheus). So I don't scale "the platform." I scale the one piece that's actually under load and leave the rest alone.

And the cost base stays honest: it's an open core on open-source infrastructure. No per-host license tax bolted on top, the way commercial APM and some databases charge. The money goes to compute you actually use — not to a licensing meter.

Running 24/7 still adds complexity, no pretending otherwise. But the architecture is finally one where high availability is a *scaling decision*, not a rewrite.

Good week. 🙂

#BuildInPublic #OpenTelemetry #Observability

---

*Notes for Robert:*
- *"No per-host license tax" is accurate and doesn't contradict the EE tier (SSO/RBAC/audit/long retention), which simply isn't mentioned here. If a comment asks about pricing, that's the moment to mention EE — not the post body.*
- *"High availability is a scaling decision, not a rewrite" describes capability, not a proven 24/7 track record — keep it that way until beta proves it.*
- *Optional: a one-line, real detail makes these posts sing — e.g. the specific component you were scaling when it clicked (ingest under load?). Add it if true.*
- *No sluicio.com link needed — pure build-in-public; let it breathe. Add the link only if you want the CTA.*
