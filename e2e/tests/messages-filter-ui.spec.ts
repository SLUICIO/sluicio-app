// SPDX-License-Identifier: Apache-2.0
//
// Message-search FilterEditor UI (2026-07-16 findings review): the
// error-type value picker offers the OBSERVED error types (not a blind
// text box), and integration↔service cross-narrowing — once one side
// is chosen, the other picker only offers compatible values.
import { test, expect, type Page } from "@playwright/test";
import { logIn } from "./fixtures";

// Adding a filter is one of the first things anyone does with the
// product, so it gets a test that drives the button in a browser rather
// than only unit-testing the id helper underneath it.
//
// The interesting case is the one that reached a user: on a self-hosted
// cell at http://box.local:8080 the page is NOT a secure context, so
// crypto.randomUUID does not exist and the click threw
// "crypto.randomUUID is not a function", emptying the view.
//
// Playwright always drives http://localhost, which IS a secure context,
// and Chromium has no switch to make an origin less trusted — only
// --unsafely-treat-insecure-origin-as-secure, which goes the wrong way.
// So the context is removed at the only place that matters to this code:
// the API surface. Deleting randomUUID before any script runs leaves the
// page in exactly the state a LAN-hostname browser hands it —
// getRandomValues present, randomUUID absent — in a real browser, on the
// real page, through the real click.
/**
 * Waits for a filter row to render, and says WHY if it does not.
 *
 * The row failing to appear is the exact symptom of the editor throwing
 * mid-render — which is the whole point of this file — but the plain
 * assertion reports only "element(s) not found", and the page-error
 * check sits on the NEXT line where it never runs. Every CI failure here
 * has therefore named a missing button and hidden the exception that
 * caused it.
 *
 * Errors are read when the wait fails, not when it starts, so one thrown
 * during the wait is still reported.
 */
async function expectFilterRow(page: Page, crashes: string[]) {
  try {
    await expect(page.getByRole("button", { name: /attribute/ }).last()).toBeVisible();
  } catch (e) {
    const why = crashes.length ? crashes.join(" | ") : "no page errors captured";
    throw new Error(`the filter row did not render — page errors: ${why}\n\n${String(e)}`);
  }
}

test("adding a filter works without crypto.randomUUID — the non-secure-context cell", async ({ page }) => {
  const crashes: string[] = [];
  page.on("pageerror", (e) => crashes.push(e.message));
  await page.addInitScript(() => {
    // @ts-expect-error deleting an optional platform API on purpose
    delete Crypto.prototype.randomUUID;
    // @ts-expect-error some builds expose it as an own property too
    delete crypto.randomUUID;
  });
  await logIn(page);

  // Guard the guard: if a future browser or polyfill puts randomUUID back,
  // this test would silently go back to exercising the secure-context
  // path and prove nothing.
  expect(
    await page.evaluate(() => typeof (crypto as Crypto).randomUUID),
    "randomUUID is still present — this test is no longer reproducing the reported failure",
  ).toBe("undefined");

  await page.goto("/search?s=any");
  await page.getByRole("button", { name: "+ add a filter" }).click();
  // The row must actually render — not merely fail to throw.
  await expectFilterRow(page, crashes);
  expect(crashes, `adding a filter threw: ${crashes.join(" | ")}`).toEqual([]);
  // This deliberately does NOT click twice to assert two rows. A fresh
  // row is a bare "payload" with no fieldPath, and writeFiltersToParams
  // does not serialize those — so a second click races the URL
  // round-trip and the count is legitimately 1 or 2 depending on timing.
  // It failed exactly that way under full-suite load. Nothing about the
  // crash this file guards needs a second row, and id uniqueness is
  // covered properly (5000 ids) in frontend/src/lib/uid.test.ts.
});

test("error-type list + integration↔service cross-narrowing on /search", async ({ page }) => {
  // The budget must exceed what this test is ALLOWED to spend waiting.
  // Two polls below wait up to 60s and 45s for data that resolves
  // asynchronously on a cold cell — 105s — so a 90s test timeout meant
  // the test could not pass on any cell slow enough to need them. It
  // survived on warm cells and died on busy ones, which reads as
  // flakiness but is really arithmetic: raise this if either poll grows.
  //
  // 180s was still not enough. The first poll lists EVERY integration on
  // the cell, and a suite run used to leave one behind PERMANENTLY —
  // protocol-group-visibility created a stamped integration through the
  // UI and never removed it — so on a long-lived throwaway stack that
  // call grew without bound while the rest of the suite competed for the
  // same cell. Alone this test finishes in ~3s; under a full parallel
  // run it spent the entire budget and died in its cleanup.
  //
  // That leak is fixed, and the count is now flat across runs. The
  // budget stays generous because the poll deliberately uses the LIST
  // endpoint — that is the response cross-narrowing consumes — so its
  // cost still tracks however many integrations a cell legitimately has.
  // If it ever needs raising AGAIN, look for a new leak first rather
  // than adding another minute.
  test.setTimeout(300_000);

  // Fail on any uncaught exception during the filter flow.
  //
  // Building a filter is among the first things anyone does with the
  // product, and when the editor throws it takes the whole view down
  // with it — the page just empties, with the reason only in a console
  // nobody has open. Asserting the absence of uncaught errors turns that
  // into a test failure rather than a support conversation.
  //
  // Scoped to pageerror (uncaught exceptions) rather than console.error,
  // which carries React warnings and failed background fetches and would
  // make this flaky for no gain.
  //
  // Note what this canNOT catch: anything that only misbehaves outside a
  // secure context. Playwright drives http://localhost, where
  // crypto.randomUUID and friends exist — a self-hosted cell on
  // http://box.local:8080 is a different world. That class of bug is
  // guarded in frontend/src/lib/uid.test.ts, where the context can be
  // taken away deliberately.
  const crashes: string[] = [];
  page.on("pageerror", (e) => crashes.push(e.message));
  await logIn(page);
  const admin = page.request;
  const stamp = Date.now().toString(36);
  // Two integrations with disjoint members.
  const mkA = await admin.post("/api/v1/integrations", {
    data: { slug: `probe-a-${stamp}`, name: `Probe A ${stamp}`, matchers: [{ operator: "equals", value: "order-api" }] },
  });
  const a = (await mkA.json()).integration.id;
  const mkB = await admin.post("/api/v1/integrations", {
    data: { slug: `probe-b-${stamp}`, name: `Probe B ${stamp}`, matchers: [{ operator: "equals", value: "payment-service" }] },
  });
  const b = (await mkB.json()).integration.id;

  try {
    // ?s seeds a private DRAFT view — deterministic row set regardless
    // of the org's shared saved views (which other suite workers touch;
    // starting from the default shared view made pill indexing racy).
    // Cross-narrowing needs Integration.services on the LIST response —
    // persisted catalog membership that resolves ASYNC after creating a
    // matcher. On a cold cell the list can report services: [] for a
    // while, which disables narrowing and strands the assertions. Wait
    // for both probes to carry their member before driving the UI.
    await expect
      .poll(
        async () => {
          // range=1h, not 30d: this poll only needs to know that the two
          // probes carry their member service, and membership comes from
          // the PERSISTED catalog, which does not depend on the window.
          // The window only adds freshly-matched services on top. The
          // range does drive per-integration traffic queries though, so
          // 30d made the readiness check scale with both the integration
          // count AND a month of telemetry — the cost that kept eating
          // this test's budget on CI.
          const list = (await (await admin.get("/api/v1/integrations?range=1h")).json()).integrations ?? [];
          const withMembers = list.filter(
            (i: { id: string; services?: string[] }) => (i.id === a || i.id === b) && (i.services ?? []).length > 0,
          );
          return withMembers.length;
        },
        { timeout: 60_000 },
      )
      .toBe(2);

    // The error-type picker renders its list only once the fields
    // catalog carries observed error types — wait for the API to have
    // them (seeded data can lag on a cold CI cell) BEFORE driving the UI.
    await expect
      .poll(
        async () => {
          const fields = (await (await page.request.get("/api/v1/messages/fields?range=24h")).json()).fields ?? [];
          return (fields.find((f: { field: string }) => f.field === "errorType")?.enumValues ?? []).length;
        },
        { timeout: 45_000 },
      )
      .toBeGreaterThan(0);
    // ?range=24h must match the window the poll above checked. The page
    // otherwise opens on DEFAULT_WINDOW ("1h") while we verified error
    // types exist over 24h — so on a cell whose seed is more than an hour
    // old the picker is legitimately empty and this test fails for a
    // reason that has nothing to do with the picker. That passed on CI
    // only because CI seeds minutes before it asserts.
    await page.goto("/search?s=any&range=24h");
    await expect(page.getByRole("button", { name: "+ add a filter" })).toBeVisible();
    // Add a filter row → defaults to payload; switch it to error type.
    await page.getByRole("button", { name: "+ add a filter" }).click();
    await expectFilterRow(page, crashes);
    await page.getByRole("button", { name: /attribute/ }).last().click();
    await page.getByRole("button", { name: "error type", exact: true }).click();
    // Open the value pill → the observed error-type list must render.
    await page.getByRole("button", { name: /—/ }).last().click();
    await expect(page.getByText("Error types seen in this window")).toBeVisible();
    await page.keyboard.press("Escape");

    // Switch the row to integration and pick Probe A.
    await page.getByRole("button", { name: /error type ▾|error type/ }).first().click();
    await page.getByRole("button", { name: "integration", exact: true }).click();
    await page.getByRole("button", { name: /—/ }).last().click();
    await page.getByRole("button", { name: `Probe A ${stamp}` }).click();

    // Add a service row: the picker must offer ONLY Probe A's member.
    await page.getByRole("button", { name: "+ add a filter" }).click();
    await page.getByRole("button", { name: /attribute/ }).last().click();
    await page.getByRole("button", { name: "service", exact: true }).click();
    await page.getByRole("button", { name: /—/ }).last().click();
    await expect(page.getByRole("button", { name: "order-api" })).toBeVisible();
    await expect(page.getByRole("button", { name: "payment-service" })).toHaveCount(0);
    await page.getByRole("button", { name: "order-api" }).click();

    // Flip the check: with service=order-api chosen, the integration
    // picker offers Probe A but not Probe B.
    await page.getByRole("button", { name: `Probe A ${stamp}` }).click();
    await expect(page.getByRole("button", { name: `Probe A ${stamp}` }).last()).toBeVisible();
    await expect(page.getByRole("button", { name: `Probe B ${stamp}` })).toHaveCount(0);
    expect(crashes, `the filter editor threw: ${crashes.join(" | ")}`).toEqual([]);
  } finally {
    await admin.delete(`/api/v1/integrations/${a}`);
    await admin.delete(`/api/v1/integrations/${b}`);
  }
});
