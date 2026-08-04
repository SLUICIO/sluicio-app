// SPDX-License-Identifier: Apache-2.0
//
// Pinning a SYSTEM ENTITY to a dashboard.
//
// The dashboard editor only ever offered services — a system with its
// own members and health checks could not be put on a board at all. It
// now can, which means a new entity_kind flows editor → PUT → Postgres →
// GET → card.
//
// That round-trip is where this silently breaks: every step used to
// branch on one specific kind, so the new one was dropped by the
// materializer, misrouted by the write mapper, or rejected by the shape
// constraint. All three failures look identical to a user — pin a
// system, hit save, and it is simply gone. So the assertion that matters
// is after a RELOAD: the card must come back from the server, not merely
// appear in local state.
//
// Isolation: every test creates its OWN system and its OWN dashboard.
// An earlier version drove the shared default board and read whatever
// systems the cell happened to have, which failed in CI two ways at
// once — the picker only renders when an UNPINNED system exists, so it
// vanished on a cell with none, and editing the shared board raced the
// other dashboard specs.
import { test, expect, type Page } from "@playwright/test";
import { logIn } from "./fixtures";

const stamp = Date.now().toString(36);
// Bumped per scratch() so two invocations of the same test — a Playwright
// retry, or --repeat-each — never collide on the unique system name.
let seq = 0;

// Talks to the API through the logged-in page's cookie jar.
async function api(page: Page, method: string, path: string, body?: unknown) {
  return page.request.fetch(`/api/v1${path}`, {
    method,
    ...(body === undefined ? {} : { data: body }),
  });
}

/**
 * A system and an empty dashboard of this test's own, plus the cleanup
 * that removes both. autoIncludeAll is false so the board holds nothing
 * but what the test pins — no integration cards to scroll past, and no
 * dependence on what else lives in the cell.
 */
async function scratch(page: Page, tag: string) {
  const name = `e2e-dash-${tag}-${stamp}-${++seq}`;
  const sys = await api(page, "POST", "/systems", { name, type_key: "rabbitmq" });
  expect(sys.ok(), `create system: ${sys.status()}`).toBeTruthy();
  const systemId = (await sys.json()).id as string;

  const board = await api(page, "POST", "/dashboards", { name, autoIncludeAll: false });
  expect(board.ok(), `create dashboard: ${board.status()}`).toBeTruthy();
  const dashboardId = (await board.json()).id as string;

  const open = async () => {
    // The page fetches its system list ONCE on mount, so the system has
    // to be listable before we navigate — otherwise the picker renders
    // empty (it is hidden when there are no candidates) and the failure
    // looks like a missing feature rather than a race.
    await expect
      .poll(
        async () => {
          const list = (await (await api(page, "GET", "/systems")).json()).systems ?? [];
          return list.some((x: { id: string }) => x.id === systemId);
        },
        { timeout: 30_000 },
      )
      .toBe(true);
    await page.goto("/health");
    await page.getByRole("button", { name, exact: false }).first().click();
    await expect(page.getByRole("button", { name: "edit dashboard" })).toBeVisible();
  };
  const cleanup = async () => {
    await api(page, "DELETE", `/dashboards/${dashboardId}`);
    await api(page, "DELETE", `/systems/${systemId}`);
  };
  return { name, systemId, dashboardId, open, cleanup };
}

/** Picks this test's system out of the "add system" picker. */
async function pinSystem(page: Page, name: string) {
  const picker = page.getByLabel("add system");
  // The picker is hidden when the page has no systems to offer, and the
  // page fetches them once on mount, swallowing a failure into an empty
  // list. So a single slow or failed request under CI load looks exactly
  // like "the feature is missing". Reload once — selection is kept in
  // localStorage, so this returns to the same board — before believing
  // the picker is genuinely absent.
  if (!(await picker.isVisible().catch(() => false))) {
    await page.reload();
    await page.getByRole("button", { name: "edit dashboard" }).click();
  }
  await expect(picker).toBeVisible({ timeout: 30_000 });
  await picker.click();
  await page.getByPlaceholder("Filter systems…").last().fill(name);
  await page.getByText(name, { exact: false }).last().click();
}

test.describe("Dashboard system entities", () => {
  // The helpers below wait up to 30s for the system to be listable and
  // another 30s for the picker, which does not fit inside Playwright's
  // 30s default — the sum has to be smaller than the budget or the test
  // dies mid-wait. Never bites locally, where both resolve instantly;
  // killed two of these on a loaded CI runner.
  test.describe.configure({ timeout: 120_000 });

  test("a pinned system survives a save and reload", async ({ page }) => {
    await logIn(page);
    const s = await scratch(page, "pin");
    try {
      await s.open();
      await page.getByRole("button", { name: "edit dashboard" }).click();
      await pinSystem(page, s.name);

      // Present in the draft before saving — proves the picker wired up.
      await expect(page.locator(`a[href="/systems/${s.systemId}"]`).first()).toBeVisible();

      await page.getByRole("button", { name: "save" }).click();
      await expect(page.getByRole("button", { name: "edit dashboard" })).toBeVisible();

      // The real assertion: it comes back from the server. A card that
      // only ever lived in local state passes every check above. It also
      // links to the system entity, not to a service of the same name —
      // the two routes are what distinguish the card kinds.
      await page.reload();
      await expect(page.locator(`a[href="/systems/${s.systemId}"]`).first()).toBeVisible();

      // Removing it must also persist, or the card is unpinnable.
      await page.getByRole("button", { name: "edit dashboard" }).click();
      await page.getByRole("button", { name: "Remove from dashboard" }).first().click();
      await page.getByRole("button", { name: "save" }).click();
      await expect(page.getByRole("button", { name: "edit dashboard" })).toBeVisible();
      await page.reload();
      await expect(page.locator(`a[href="/systems/${s.systemId}"]`)).toHaveCount(0);
    } finally {
      await s.cleanup();
    }
  });

  test("no card offers a remove control outside edit mode", async ({ page }) => {
    // The × used to render on system cards in view mode. Clicking it
    // mutated a draft that is not what the page renders, so the card
    // stayed put and nothing saved — a control that looks live and does
    // nothing.
    await logIn(page);
    const s = await scratch(page, "viewmode");
    try {
      await s.open();
      await page.getByRole("button", { name: "edit dashboard" }).click();
      await pinSystem(page, s.name);
      await page.getByRole("button", { name: "save" }).click();

      // Back in view mode, with a system card definitely on the board.
      await expect(page.getByRole("button", { name: "edit dashboard" })).toBeVisible();
      await expect(page.locator(`a[href="/systems/${s.systemId}"]`).first()).toBeVisible();
      await expect(page.getByRole("button", { name: "Remove from dashboard" })).toHaveCount(0);

      // And it comes back in edit mode — the fix must not simply delete it.
      await page.getByRole("button", { name: "edit dashboard" }).click();
      await expect(page.getByRole("button", { name: "Remove from dashboard" }).first()).toBeVisible();
    } finally {
      await s.cleanup();
    }
  });

  test("the editor offers integrations and systems, never a bare service", async ({ page }) => {
    // The old picker listed services flagged is_system under the label
    // "add system" — wrong label, and off-model for an integration-
    // centric product. Its absence is the assertion; a stray extra
    // picker would otherwise creep back unnoticed. The scratch system
    // guarantees the surviving picker has something to offer, since it
    // only renders when an unpinned system exists.
    await logIn(page);
    const s = await scratch(page, "pickers");
    try {
      await s.open();
      await page.getByRole("button", { name: "edit dashboard" }).click();
      // Same reload-retry as pinSystem: an empty systems fetch hides the
      // picker and would read as "the wrong picker survived".
      const picker = page.getByLabel("add system");
      if (!(await picker.isVisible().catch(() => false))) {
        await page.reload();
        await page.getByRole("button", { name: "edit dashboard" }).click();
      }
      await expect(picker).toHaveCount(1, { timeout: 30_000 });
      await expect(page.getByLabel("add system entity")).toHaveCount(0);
    } finally {
      await s.cleanup();
    }
  });

  test("cards are ordered by health, not by the order they were pinned", async ({ page }) => {
    // Asserts SORTEDNESS of what is on screen — worst health first, name
    // within a health state — rather than one expected sequence. The
    // health ladder itself is covered by src/lib/healthOrder.test.ts;
    // what this adds is that the page actually applies it, which pinning
    // the three in reverse alphabetical order makes unmistakable.
    await logIn(page);
    const board = await api(page, "POST", "/dashboards", {
      name: `e2e-dash-order-${stamp}-${++seq}`,
      autoIncludeAll: false,
    });
    const dashboardId = (await board.json()).id as string;
    const boardName = `e2e-dash-order-${stamp}-${seq}`;
    const made: { id: string; name: string }[] = [];
    try {
      // Created and pinned in REVERSE alphabetical order.
      for (const suffix of ["zulu", "mike", "alpha"]) {
        const name = `e2e-order-${suffix}-${stamp}`;
        const r = await api(page, "POST", "/systems", { name, type_key: "rabbitmq" });
        expect(r.ok(), `create ${name}: ${r.status()}`).toBeTruthy();
        made.push({ id: (await r.json()).id as string, name });
      }
      const put = await api(page, "PUT", `/dashboards/${dashboardId}`, {
        name: boardName,
        isDefault: false,
        autoIncludeAll: false,
        defaultWidgetType: "traffic_sparkline",
        position: 0,
        items: made.map((m, idx) => ({
          entityKind: "system_entity",
          systemId: m.id,
          widgetType: "system_health",
          position: idx,
        })),
      });
      expect(put.ok(), `pin systems: ${put.status()}`).toBeTruthy();

      await page.goto("/health");
      await page.getByRole("button", { name: boardName, exact: false }).first().click();
      // Cards paint from items[] before the page's systems list resolves,
      // and until it does every card has neither a name nor a status — so
      // they all compare equal and a stable sort leaves them in pin order.
      // Waiting for every NAME to appear is waiting for that list, which
      // is what the ordering is computed from.
      for (const m of made) {
        await expect(page.getByText(m.name, { exact: false }).first()).toBeVisible({
          timeout: 30_000,
        });
      }

      // Read the status off each rendered card rather than re-fetching
      // it. A freshly created system's rollup settles asynchronously, so
      // a second API call can report a different status than the one the
      // page sorted on — comparing across those two instants fails on
      // timing, not on ordering. Everything below comes from one paint.
      const cards = await page.locator("a[href^='/systems/']").evaluateAll((els) =>
        els.map((e) => ({
          href: (e as HTMLAnchorElement).getAttribute("href") ?? "",
          text: (e as HTMLElement).innerText,
        })),
      );
      const rank = (st: string | undefined) =>
        ({ unhealthy: 0, errors: 1, ok: 2, quiet: 3 })[st ?? ""] ?? 4;
      const mine = cards
        .map((c) => {
          const m = made.find((x) => c.href === `/systems/${x.id}`);
          if (!m) return null;
          return { name: m.name, status: /status\s+(\w+)/i.exec(c.text)?.[1]?.toLowerCase() };
        })
        .filter((x): x is { name: string; status?: string } => Boolean(x));
      expect(mine).toHaveLength(made.length);

      // The invariant: worst health first, name within a health state.
      const sorted = [...mine].sort(
        (x, y) => rank(x.status) - rank(y.status) || x.name.localeCompare(y.name),
      );
      expect(mine.map((c) => c.name)).toEqual(sorted.map((c) => c.name));
      // And it is genuinely a sort: pin order was the reverse of the
      // alphabetical tiebreak, so replaying it could not produce this.
      expect(mine.map((c) => c.name)).not.toEqual(made.map((m) => m.name));
    } finally {
      await api(page, "DELETE", `/dashboards/${dashboardId}`);
      for (const m of made) await api(page, "DELETE", `/systems/${m.id}`);
    }
  });

  test("the server refuses a system from another org", async ({ page }) => {
    // The FK alone only proves the system exists somewhere. A random
    // UUID stands in for another tenant's id: both must be refused, or
    // the board grows a card that can never render.
    await logIn(page);
    const res = await api(page, "POST", "/dashboards", {
      name: `e2e-dash-alien-${stamp}`,
      items: [
        {
          entityKind: "system_entity",
          systemId: "11111111-2222-3333-4444-555555555555",
          widgetType: "system_health",
          position: 0,
        },
      ],
    });
    expect(res.status()).toBe(400);
  });
});
