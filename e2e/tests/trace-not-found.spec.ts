// SPDX-License-Identifier: Apache-2.0
//
// A log names the trace it was written in whether or not that trace was
// ever sent to the cell, so following it could end on a bare "not
// found" that read like a broken link. The trace endpoint now says the
// trace is missing and how long traces are kept, and the trace page
// explains the likely reasons, or that the trace has most likely
// expired when the log is older than that.
import { test, expect } from "@playwright/test";
import { logIn } from "./fixtures";

// A trace id no cell will hold.
const absent = "0000000000000000000000000000e2e0";

test.describe("A trace with no spans", () => {
  test("the API says it is missing and how long traces are kept", async ({ page }) => {
    await logIn(page);
    const res = await page.request.get(`/api/v1/traces/${absent}`);
    expect(res.status()).toBe(404);
    const body = await res.json();
    expect(body.missing).toBe(true);
    expect(body.trace_retention_days).toBeGreaterThan(0);
  });

  test("the trace page explains it, and calls an old log's trace expired", async ({ page }) => {
    await logIn(page);
    await page.goto(`/traces/${absent}`);
    const box = page.getByTestId("trace-not-found");
    await expect(box).toContainText("This trace has no spans here");
    await expect(box).toContainText("It was never sent here");

    await page.goto(`/traces/${absent}?seen=2020-01-01T00:00:00Z`);
    await expect(page.getByTestId("trace-not-found")).toContainText("most likely been deleted");
  });
});
