# Recorded walkthroughs

Scripted screen recordings of Sluicio, for product videos and docs. Each `*.rec.ts` file drives the real UI at a pace a viewer can follow and leaves an MP4 behind. They are not tests: they never run with the e2e suite, and they only run against the recording stack, never against a cell with real data.

## Once: start the recording stack

```bash
recordings/stack.sh up
```

A throwaway Sluicio on its own ports (UI on `http://localhost:8095`, ingest on `4325`) with a continuous seeder, an on-camera account ("Alex Morgan") and an org called "Acme Logistics". The account's password, the stack URL and the seeder's ingest key are written to `e2e/.env.recording`, which git ignores; nothing secret is printed.

Give it **30 minutes of traffic** before a take, so counts, charts and health have history. A take checks the stack is ready before it starts and says so if it is not.

`RECORDING_TAG` picks the image version (default `latest`); set it to the release you want on screen. `recordings/stack.sh status` shows what is running; `recordings/stack.sh down` removes the stack **and its data**.

### RabbitMQ in the stack

`stack.sh up` also starts a RabbitMQ broker and the OpenTelemetry Collector that streams its metrics in, set up the way the [RabbitMQ guide](https://docs.sluicio.com/guides/rabbitmq-queue-depth/) tells a customer to: per-queue metrics from the management API, node alarms from the prometheus plugin, a read-only `monitoring` user, OTLP/HTTP with an ingest key. The collector sets `service.name: rabbitmq`, so every queue arrives under one service and each can become an integration of its own ("rabbitmq, where `rabbitmq.queue.name` is `invoices.outbound`").

Three queues, each with a different story (`recordings/rabbitmq/docker-compose.yml`):

| Queue | Story |
|---|---|
| `orders.inbound` | Healthy: consumers keep up, depth stays near zero. |
| `invoices.outbound` | Bursty: a one-minute burst builds a backlog of about 1,500 that drains over the next three minutes. |
| `shipments.events` | Stalled: no consumer, so depth climbs about 180 a minute to a 6,000 cap. "No consumers" fires at once, "Queue backlog" once it passes 5,000. |

The broker's management UI is at `http://localhost:15680` (user `admin`, password in `e2e/.env.recording`). The collector is pinned to contrib 0.157.0 and its config is validated against that version: component names change between collector versions, so re-validate before moving the tag.

## Record

```bash
recordings/record.sh                              # every walkthrough
recordings/record.sh integration-to-message       # one of them
RECORDING_CAPTIONS=1 recordings/record.sh         # with on-screen captions, for a silent cut
RECORDING_HEADED=1 recordings/record.sh           # a visible browser, to capture with your own recorder
```

Each take is written to `e2e/recordings-output/<walkthrough>-<date>[-captions].mp4` (H.264, 1440x900). A take that fails leaves a Playwright trace in `recordings-output/raw/` showing where it stopped.

Knobs: `RECORDING_PACE` (default `1`; `1.5` is slower), `RECORDING_WIDTH` / `RECORDING_HEIGHT`, `RECORDING_THEME=dark`.

## Walkthroughs

| File | What it shows |
|---|---|
| `integration-to-message.rec.ts` | Building an integration from one service, adding the next one the traces suggest, then finding one customer's order among its messages and opening its whole path. |

## Writing one

`stagecraft.ts` has what makes a run watchable: a drawn pointer (Playwright's recordings have none), `glideClick` and `typeInto` so the pointer moves and types at a human pace, `caption` for silent cuts, and `beat` for a pause that scales with `RECORDING_PACE`. A walkthrough should clean up after its previous take, so every take starts from the same place.
