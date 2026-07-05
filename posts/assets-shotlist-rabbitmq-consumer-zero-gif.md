# Flagship GIF shot-list — "the consumer hit zero"

**Asset:** short looping screen-capture GIF (no narration) for the RabbitMQ how-to post + LinkedIn teaser
**Story:** a healthy queue, consumers silently drop to zero, backlog climbs, Sluicio flags it — the silent outage that service-level monitoring never sees.
**Why this one first:** it's the most visceral, it maps to a real metric (`rabbitmq.consumer.count`), and it doubles as a design-partner hook.

## Specs

- **Length:** 12–18 seconds, seamless loop.
- **Format:** GIF or muted MP4/WebM (LinkedIn prefers native video; for the blog, an MP4 with `loop autoplay muted playsinline` is lighter than a GIF — export both).
- **Dimensions:** 1200×675 (16:9) for blog; also export a 1:1 (1080×1080) crop for LinkedIn feed.
- **Frame rate:** 15–20 fps is plenty; keeps file size down.
- **Target size:** under ~5 MB so it loads fast and LinkedIn doesn't recompress it to mush.
- **No audio, no narration** — readable in a silent autoplay feed.

## Setup before recording

- Use a clean demo tenant with one obvious queue (e.g. `partner-edi.inbound`) that has 1–2 active consumers.
- Have the Sluicio view open on the queue / its `rabbitmq.consumer.count` and `rabbitmq.message.current` (state=ready) panels.
- Drive it with a script: a publisher trickling messages in steadily, a consumer you can kill on cue. Pre-rehearse so the backlog climbs visibly within the clip's runtime (compress time if needed).
- Hide anything you don't want on camera (other tenants, real customer names, internal URLs). Use a neutral browser profile, no bookmarks bar.

## Beat-by-beat

| Time | On screen | Caption overlay (optional, lower third) |
|---|---|---|
| 0–3s | Queue looks healthy: consumer count = 2, ready messages flat and low, deliver rate keeping pace with publish rate. | "Every dashboard says healthy." |
| 3–6s | The consumer process is killed. `rabbitmq.consumer.count` drops 2 → 0. Hold on that number for a beat. | "Then the consumers quietly stop." |
| 6–12s | `rabbitmq.message.current` (state=ready) starts climbing — the backlog visibly stacking up while nothing else looks alarming. | "Messages pile up. The services still look fine." |
| 12–16s | Sluicio surfaces it — the alert/indicator on `consumer.count == 0` (or backlog threshold) lights up. | "Sluicio catches the outage nobody else sees." |
| last frame | Settle on the lit alert + climbing backlog; hold ~1s, then loop. | (end card optional) |

## Punchline / end card (optional)

If adding a closing card (good for LinkedIn, skip for inline blog): one line — **"consumer.count = 0. The silent outage."** — small Sluicio wordmark, sluicio.com. Keep it 1–1.5s.

## Production tips

- **Zoom the UI** to ~125–150% before recording so numbers are legible at feed size; trim chrome.
- **Cursor:** move deliberately, or hide it — jittery mouse reads as amateurish.
- **One idea only.** Resist showing five panels. The whole point is: count → zero, backlog → up, alert → fires.
- **Tools:** macOS screen record or [Kap](https://getkap.co) / ScreenStudio for capture; convert/trim with `ffmpeg` or [gifski](https://gif.ski) for a high-quality small GIF. asciinema is the companion tool for the *setup* clips, not this one.
- **Re-record budget:** because the product is pre-GA, expect to redo this when the UI shifts — keep the driver script saved so a re-shoot is 10 minutes, not an afternoon.

## Reuse

This one asset works in at least three places: embedded in the RabbitMQ blog post, as the native video on the LinkedIn how-to teaser, and in design-partner / intro-call decks as the "this is what 15 minutes shows you" proof.

---

*Recommended rollout (also reflected in the calendar's new "Visual Asset" column): GIFs only for the how-to and one-pane posts, charts/diagrams for category & positioning pieces, and nothing for the text-led build-in-public posts. Don't let asset production outrun shipping.*
