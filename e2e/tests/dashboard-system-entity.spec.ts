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
  const name = `e2e-dash-${tag}-${stamp}`;
  const sys = await api(page, "POST", "/systems", { name, type_key: "rabbitmq" });
  expect(sys.ok(), `create system: ${sys.status()}`).toBeTruthy();
  const systemId = (await sys.json()).id as string;

  const board = await api(page, "POST", "/dashboards", { name, autoIncludeAll: false });
  expect(board.ok(), `create dashboard: ${board.status()}`).toBeTruthy();
  const dashboardId = (await board.json()).id as string;

  const open = async () => {
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
  await expect(picker).toBeVisible();
  await picker.click();
  await page.getByPlaceholder("Filter systems…").last().fill(name);
  await page.getByText(name, { exact: false }).last().click();
}

test.describe("Dashboard system entities", () => {
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
      await expect(page.getByLabel("add system entity")).toHaveCount(0);
      await expect(page.getByLabel("add system")).toHaveCount(1);
    } finally {
      await s.cleanup();
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
