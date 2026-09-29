# Recorded walkthroughs

Scripted screen recordings of Sluicio, for product videos and docs. Each `*.rec.ts` file drives the real UI at a pace a viewer can follow and leaves an MP4 behind. They are not tests: they never run with the e2e suite, and they only run against the recording stack, never against a cell with real data.

## Once: start the recording stack

```bash
recordings/stack.sh up
```

A throwaway Sluicio on its own ports (UI on `http://localhost:8095`, ingest on `4325`) with a continuous seeder, an on-camera account ("Alex Morgan") and an org called "Acme Logistics". The account's password, the stack URL and the seeder's ingest key are written to `e2e/.env.recording`, which git ignores; nothing secret is printed.

Give it **30 minutes of traffic** before a take, so counts, charts and health have history. A take checks the stack is ready before it starts and says so if it is not.

`RECORDING_TAG` picks the image version (default `latest`); set it to the release you want on screen. `recordings/stack.sh status` shows what is running; `recordings/stack.sh down` removes the stack **and its data**.

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
