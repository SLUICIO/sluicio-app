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
import { test, expect, type Page } from "@playwright/test";
import { logIn } from "./fixtures";

const stamp = Date.now().toString(36);
const SYSTEM_NAME = `e2e-dash-sys-${stamp}`;

// Talks to the API through the logged-in page's cookie jar.
async function api(page: Page, method: string, path: string, body?: unknown) {
  return page.request.fetch(`/api/v1${path}`, {
    method,
    ...(body === undefined ? {} : { data: body }),
  });
}

test.describe("Dashboard system entities", () => {
  test.describe.configure({ mode: "serial" });

  test("a pinned system survives a save and reload", async ({ page }) => {
    await logIn(page);

    // A system entity to pin. Any built-in type works; the card shows
    // the type key, so this also proves the card reads the entity and
    // not some service that happens to share its name.
    const created = await api(page, "POST", "/systems", {
      name: SYSTEM_NAME,
      type_key: "rabbitmq",
    });
    expect(created.ok(), `create system: ${created.status()}`).toBeTruthy();
    const systemId = (await created.json()).id as string;

    try {
      await page.goto("/health");
      await page.getByRole("button", { name: "edit dashboard" }).click();

      // The only system picker there is. It offers ENTITIES; the older
      // list of services flagged is_system was removed — a board holds
      // integrations and systems, not individual services.
      const picker = page.getByLabel("add system");
      await expect(picker).toBeVisible();
      await picker.click();
      await page.getByPlaceholder("Filter systems…").last().fill(SYSTEM_NAME);
      await page.getByText(SYSTEM_NAME, { exact: false }).last().click();

      // Present in the draft before saving — proves the picker wired up.
      await expect(page.getByText(SYSTEM_NAME).first()).toBeVisible();

      await page.getByRole("button", { name: "save" }).click();
      await expect(page.getByRole("button", { name: "edit dashboard" })).toBeVisible();

      // The real assertion: it comes back from the server. A card that
      // only ever lived in local state passes every check above.
      await page.reload();
      await expect(page.getByText(SYSTEM_NAME).first()).toBeVisible();

      // And it links to the system entity, not to a service of the same
      // name — the two routes are what distinguish the card kinds.
      await expect(page.locator(`a[href="/systems/${systemId}"]`).first()).toBeVisible();

      // Removing it must also persist, or the card is unpinnable.
      await page.getByRole("button", { name: "edit dashboard" }).click();
      await page
        .locator(`a[href="/systems/${systemId}"]`)
        .first()
        .locator("xpath=../..")
        .getByRole("button", { name: "Remove from dashboard" })
        .click();
      await page.getByRole("button", { name: "save" }).click();
      await expect(page.getByRole("button", { name: "edit dashboard" })).toBeVisible();
      await page.reload();
      await expect(page.locator(`a[href="/systems/${systemId}"]`)).toHaveCount(0);
    } finally {
      await api(page, "DELETE", `/systems/${systemId}`);
    }
  });

  test("no card offers a remove control outside edit mode", async ({ page }) => {
    // The × used to render on system cards in view mode. Clicking it
    // mutated a draft that is not what the page renders, so the card
    // stayed put and nothing saved — a control that looks live and does
    // nothing. Asserted across the WHOLE dashboard, not just the system
    // strip, because the rule is per-page, not per-card-kind.
    await logIn(page);
    const created = await api(page, "POST", "/systems", {
      name: `${SYSTEM_NAME}-viewmode`,
      type_key: "rabbitmq",
    });
    const systemId = (await created.json()).id as string;
    try {
      await page.goto("/health");
      await page.getByRole("button", { name: "edit dashboard" }).click();
      await page.getByLabel("add system").click();
      await page.getByPlaceholder("Filter systems…").last().fill(`${SYSTEM_NAME}-viewmode`);
      await page.getByText(`${SYSTEM_NAME}-viewmode`, { exact: false }).last().click();
      await page.getByRole("button", { name: "save" }).click();

      // Back in view mode, with a system card definitely on the board.
      await expect(page.getByRole("button", { name: "edit dashboard" })).toBeVisible();
      await expect(page.locator(`a[href="/systems/${systemId}"]`).first()).toBeVisible();
      await expect(page.getByRole("button", { name: "Remove from dashboard" })).toHaveCount(0);

      // And it comes back in edit mode — the fix must not simply delete it.
      await page.getByRole("button", { name: "edit dashboard" }).click();
      await expect(
        page.getByRole("button", { name: "Remove from dashboard" }).first(),
      ).toBeVisible();
      await page.getByRole("button", { name: "cancel" }).click();
    } finally {
      await api(page, "DELETE", `/systems/${systemId}`);
      // Leave the shared dashboard as it was found.
      const boards = await (await api(page, "GET", "/dashboards")).json();
      for (const d of boards.dashboards ?? []) {
        const items = (d.items ?? []).filter((i: { systemId?: string }) => i.systemId !== systemId);
        if (items.length !== (d.items ?? []).length) {
          await api(page, "PUT", `/dashboards/${d.id}`, {
            name: d.name,
            isDefault: d.isDefault,
            autoIncludeAll: d.autoIncludeAll,
            defaultWidgetType: d.defaultWidgetType,
            position: d.position,
            items: items.map((i: Record<string, unknown>) => ({
              entityKind: i.entityKind,
              integrationId: i.integrationId,
              systemName: i.systemName,
              systemId: i.systemId,
              widgetType: i.widgetType,
              position: i.position,
            })),
          });
        }
      }
    }
  });

  test("the editor offers integrations and systems, never a bare service", async ({ page }) => {
    // The old picker listed services flagged is_system under the label
    // "add system" — wrong label, and off-model for an integration-
    // centric product. Its absence is the assertion; a stray extra
    // picker would otherwise creep back unnoticed.
    await logIn(page);
    await page.goto("/health");
    await page.getByRole("button", { name: "edit dashboard" }).click();
    await expect(page.getByLabel("add system entity")).toHaveCount(0);
    await expect(page.getByLabel("add system")).toHaveCount(1);
    await page.getByRole("button", { name: "cancel" }).click();
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
