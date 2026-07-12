// SPDX-License-Identifier: Apache-2.0
//
// Two UX gaps from live demo-cell feedback:
//   1. Editing an integration must include its metadata — the Metadata
//      panel now also mounts on the integration Settings page.
//   2. When no ingest URL is configured, exporter snippets fall back to
//      the UI host, which is wrong on split-host deploys — the
//      Ingestion tab must nudge admins toward the System-settings field.
import { test, expect, request as pwRequest } from "@playwright/test";
import { logIn, ADMIN_EMAIL, ADMIN_PASSWORD } from "./fixtures";

const BASE_URL = process.env.E2E_BASE_URL || "http://localhost:5173";

test("integration Settings page carries the metadata editor", async ({ page }) => {
  const admin = await pwRequest.newContext({ baseURL: BASE_URL });
  await admin.post("/api/v1/auth/login", { data: { email: ADMIN_EMAIL, password: ADMIN_PASSWORD } });
  const created = await (
    await admin.post("/api/v1/integrations", {
      data: { slug: "e2e-ism-integ", name: "E2E ISM", matchers: [{ operator: "equals", value: "e2e-ism-svc" }] },
    })
  ).json();
  const integID = created.integration?.id ?? created.id;
  try {
    await logIn(page);
    await page.goto(`/integrations/${integID}/settings`);
    // With no metadata fields defined the panel shows its empty-state
    // pointer; with fields it shows the editor — either proves presence.
    await expect(
      page.getByText(/No metadata fields are defined yet|Metadata/).first(),
    ).toBeVisible();
    await expect(page.getByRole("heading", { name: "Metadata", exact: true })).toBeVisible();
  } finally {
    await admin.delete(`/api/v1/integrations/${integID}`);
    await admin.dispose();
  }
});

test("ingestion tab nudges when no ingest URL is configured", async ({ page }) => {
  await logIn(page);
  await page.route("**/api/v1/cell-settings/system", (route) =>
    route.fulfill({ json: { environment: "e2e", ingest_base_url: "", ingest_url_source: "unset" } }),
  );
  await page.goto("/settings?tab=ingestion");
  await expect(page.getByText(/set the Ingest URL/)).toBeVisible();
});

test("env-managed ingest URL renders read-only on System settings", async ({ page }) => {
  await logIn(page);
  await page.route("**/api/v1/cell-settings/system", (route) =>
    route.fulfill({
      json: { environment: "e2e", ingest_base_url: "https://demo-ingest.example.com", ingest_url_source: "env" },
    }),
  );
  await page.goto("/settings?tab=system");
  await expect(page.getByText(/Managed by the deployment/)).toBeVisible();
  await expect(page.getByLabel("Ingest base URL")).toBeDisabled();
});

test("no nudge once the ingest URL is set — snippets use it", async ({ page }) => {
  await logIn(page);
  await page.route("**/api/v1/cell-settings/system", (route) =>
    route.fulfill({ json: { environment: "e2e", ingest_base_url: "https://demo-ingest.example.com", ingest_url_source: "setting" } }),
  );
  await page.goto("/settings?tab=ingestion");
  await expect(page.getByText("Ingest keys authenticate")).toBeVisible();
  await expect(page.getByText(/set the Ingest URL/)).toHaveCount(0);
});

test("creating an integration captures metadata inline; required blocks", async ({ page }) => {
  const admin = await pwRequest.newContext({ baseURL: BASE_URL });
  await admin.post("/api/v1/auth/login", { data: { email: ADMIN_EMAIL, password: ADMIN_PASSWORD } });
  const field = await (
    await admin.post("/api/v1/metadata-fields", {
      data: {
        key: "e2e-contact", label: "E2E Contact", type: "text", description: "",
        applies_to_integration: true, applies_to_service: false, applies_to_system: false,
        system_type_key: "", required: true,
      },
    })
  ).json();
  try {
    await logIn(page);
    await page.goto("/integrations/new");
    // The metadata section renders the org's integration fields inline.
    await expect(page.getByText("E2E Contact")).toBeVisible();
    await page.getByLabel(/^Name/).first().fill("E2E Meta Create");
    // Required metadata blocks creation before any request fires.
    await page.getByRole("button", { name: /Create integration/ }).click();
    await expect(page.getByText('"E2E Contact" is required.')).toBeVisible();
  } finally {
    await admin.delete(`/api/v1/metadata-fields/${field.id}`);
    await admin.dispose();
  }
});
