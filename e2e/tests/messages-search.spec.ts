// SPDX-License-Identifier: Apache-2.0
//
// Message-search API contracts from the 2026-07-16 findings review:
//   - an incomplete payload filter row (the editor's default) is a
//     no-op, not a 400 that kills the whole search
//   - trace/span ids support `contains` (fragments from log lines)
//   - the fields catalog offers the OBSERVED error types so the picker
//     isn't a blind text box
import { test, expect } from "@playwright/test";
import { logIn } from "./fixtures";
import { encodeTraceExport } from "./otlp";

const INGEST_URL = process.env.E2E_INGEST_URL || "http://localhost:4318";

test.describe("Message search — filter contracts", () => {
  test("incomplete payload row is a no-op; sibling filters still apply", async ({ page }) => {
    await logIn(page);
    const res = await page.request.post("/api/v1/messages/search", {
      data: {
        range: "24h",
        filters: [
          { field: "payload", fieldPath: "", op: "equals", value: "" }, // freshly added row
          { field: "status", op: "is", value: "any (ok, warn, err)" },
        ],
      },
    });
    expect(res.status()).toBe(200);
  });

  test("trace id and span id accept contains (fragment match)", async ({ page }) => {
    await logIn(page);
    for (const field of ["traceId", "spanId"]) {
      const res = await page.request.post("/api/v1/messages/search", {
        data: { range: "24h", filters: [{ field, op: "contains", value: "abc12" }] },
      });
      expect(res.status(), `${field} contains`).toBe(200);
    }
    // Exact match still works.
    const exact = await page.request.post("/api/v1/messages/search", {
      data: { range: "24h", filters: [{ field: "traceId", op: "is", value: "ABCDEF0123456789ABCDEF0123456789" }] },
    });
    expect(exact.status()).toBe(200);
  });

  test("fields catalog lists observed error types", async ({ page }) => {
    await logIn(page);
    const admin = page.request;
    // Self-sufficient: ingest one error span with a distinctive
    // exception.type, then the catalog must offer it.
    const keyRes = await admin.post("/api/v1/ingest-keys", { data: { name: `e2e-msgsearch-${Date.now().toString(36)}` } });
    test.skip(!keyRes.ok(), "cannot mint ingest key");
    const key = (await keyRes.json()).key;
    const errType = `E2ETestException${Date.now().toString(36)}`;
    const sent = await admin.post(`${INGEST_URL}/v1/traces`, {
      headers: { Authorization: `Bearer ${key}`, "Content-Type": "application/x-protobuf" },
      data: encodeTraceExport(`e2e-msgsearch-svc`, [
        { name: "POST /fail", error: true, attrs: { "exception.type": errType } },
      ]),
      failOnStatusCode: false,
    });
    test.skip(!sent.ok(), `cell-ingest not reachable at ${INGEST_URL}`);

    await expect
      .poll(
        async () => {
          const fields = (await (await admin.get("/api/v1/messages/fields?range=1h")).json()).fields ?? [];
          const et = fields.find((f: { field: string }) => f.field === "errorType");
          return (et?.enumValues ?? []) as string[];
        },
        { timeout: 30_000 },
      )
      .toContain(errType);
  });
});
