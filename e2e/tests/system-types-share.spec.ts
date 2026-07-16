// SPDX-License-Identifier: Apache-2.0
//
// Shareable system types (docs/system-types-sharing.md): export any
// catalog entry as a portable YAML/JSON document, import it into
// another org/cell. The community-sharing loop: fork a built-in,
// tweak, publish the file, someone else imports it.
import { test, expect } from "@playwright/test";
import { logIn } from "./fixtures";

test.describe("System types — export/import", () => {
  test("export a built-in, retarget the key, import as a custom type; conflicts need replace", async ({ page }) => {
    await logIn(page);
    const admin = page.request;
    const stamp = Date.now().toString(36);
    const key = `e2e-shared-${stamp}`;

    // Export the built-in RabbitMQ type as YAML — forkable by design.
    const exp = await admin.get("/api/v1/system-types/rabbitmq/export");
    expect(exp.status()).toBe(200);
    expect(exp.headers()["content-disposition"]).toContain("rabbitmq.systemtype.yaml");
    const yamlText = await exp.text();
    expect(yamlText).toContain("format: sluicio/system-type/v1");
    expect(yamlText).toContain("key: rabbitmq");
    expect(yamlText).toContain("checks:");

    // Retarget the key + label → import as a NEW custom type.
    const forked = yamlText.replace("key: rabbitmq", `key: ${key}`).replace(/label: .*/, `label: E2E Shared ${stamp}`);
    let importedID = "";
    try {
      const imp = await admin.post("/api/v1/system-types/import", {
        headers: { "Content-Type": "application/yaml" },
        data: forked,
      });
      expect(imp.status()).toBe(201);
      const dto = await imp.json();
      importedID = dto.id;
      expect(dto.key).toBe(key);
      expect((dto.checks ?? []).length).toBeGreaterThan(0);

      // It's in the effective catalog now.
      const list = await (await admin.get("/api/v1/system-types")).json();
      const mine = (list.system_types ?? []).find((t: { key: string }) => t.key === key);
      expect(mine).toBeTruthy();
      expect(mine.built_in).toBe(false);

      // Re-import without replace → 409; with replace → 200.
      expect((await admin.post("/api/v1/system-types/import", { headers: { "Content-Type": "application/yaml" }, data: forked })).status()).toBe(409);
      expect(
        (await admin.post("/api/v1/system-types/import?replace=true", { headers: { "Content-Type": "application/yaml" }, data: forked })).status(),
      ).toBe(200);

      // JSON export round-trips too.
      const jexp = await admin.get(`/api/v1/system-types/${key}/export?format=json`);
      expect(jexp.status()).toBe(200);
      expect((await jexp.json()).format).toBe("sluicio/system-type/v1");
    } finally {
      if (importedID) await admin.delete(`/api/v1/system-types/${importedID}`);
    }

    // Garbage and out-of-vocabulary documents are rejected clearly.
    const bad = await admin.post("/api/v1/system-types/import", {
      headers: { "Content-Type": "application/yaml" },
      data: `format: sluicio/system-type/v1\nkey: bad-type\nlabel: Bad\nchecks:\n  - name: x\n    signal: spans\n`,
    });
    expect(bad.status()).toBe(400);
    expect(await bad.text()).toContain("unknown signal");
    expect((await admin.post("/api/v1/system-types/import", { headers: { "Content-Type": "application/yaml" }, data: "not: a: valid: doc" })).status()).toBe(400);
  });

  test("UI: export link on every row, import via file picker", async ({ page }) => {
    await logIn(page);
    const stamp = Date.now().toString(36);
    const key = `e2e-ui-shared-${stamp}`;
    await page.goto("/system-types");

    // Every row (built-ins included) offers Export.
    const rabbitRow = page.locator("div").filter({ hasText: /RabbitMQ/ }).last();
    await expect(rabbitRow.getByRole("link", { name: "Export" }).first()).toBeVisible();

    // Import a minimal doc through the real file picker.
    const doc = [
      "format: sluicio/system-type/v1",
      `key: ${key}`,
      `label: E2E UI Shared ${stamp}`,
      "is_system: true",
      "detect_prefixes:",
      `  - ${key}.`,
      "checks:",
      "  - name: Error logs",
      "    signal: log",
      "    min_severity: 17",
    ].join("\n");
    const picker = page.waitForEvent("filechooser");
    await page.getByRole("button", { name: "Import…" }).click();
    (await picker).setFiles({ name: `${key}.systemtype.yaml`, mimeType: "application/yaml", buffer: Buffer.from(doc) });

    await expect(page.getByText(`E2E UI Shared ${stamp}`).first()).toBeVisible({ timeout: 10_000 });

    // Clean up via the API (row id from the list endpoint).
    const list = await (await page.request.get("/api/v1/system-types")).json();
    const mine = (list.system_types ?? []).find((t: { key: string }) => t.key === key);
    if (mine?.id) await page.request.delete(`/api/v1/system-types/${mine.id}`);
  });
});
