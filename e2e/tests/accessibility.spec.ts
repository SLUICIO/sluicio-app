// SPDX-License-Identifier: Apache-2.0
//
// WCAG 2.1 AA on the pages people actually live in.
//
// Automated rules catch perhaps a third of the criteria — they cannot
// judge whether a label is meaningful or a focus order is sensible — so
// passing this is a floor, not a certificate. What it does buy is that
// the failures we HAVE fixed cannot come back unnoticed, which is the
// usual way accessibility work erodes: one merged component at a time.
//
// The baseline is zero. When this fails, fix the markup rather than
// widening the exclusions — an exclusion is a decision to ship the
// barrier, and should be argued for in a comment.
import { test, expect } from "@playwright/test";
import AxeBuilder from "@axe-core/playwright";
import { logIn } from "./fixtures";

const TAGS = ["wcag2a", "wcag2aa", "wcag21a", "wcag21aa"];

const PAGES: { name: string; path: string }[] = [
  { name: "dashboard", path: "/health" },
  { name: "integrations", path: "/integrations" },
  { name: "errors", path: "/stuck" },
  { name: "services", path: "/services" },
  { name: "metrics", path: "/metrics" },
  { name: "settings", path: "/settings" },
];

for (const { name, path } of PAGES) {
  test(`${name} has no WCAG 2.1 AA violations`, async ({ page }) => {
    await logIn(page);
    await page.goto(path);
    // Content arrives asynchronously; auditing an empty shell proves
    // nothing, so wait for the page to settle before scanning.
    await page.waitForLoadState("networkidle").catch(() => {});
    await page.waitForTimeout(1500);

    const results = await new AxeBuilder({ page }).withTags(TAGS).analyze();

    // Name every rule and the element it fired on: "3 violations" sends
    // the reader back to the browser, which is where these get ignored.
    const detail = results.violations
      .map((v) => `${v.impact ?? "?"} ${v.id} (${v.nodes.length}) — ${v.help}\n    ${v.nodes.map((n) => n.target.join(" ")).slice(0, 4).join("\n    ")}`)
      .join("\n  ");
    expect(results.violations, `${name}:\n  ${detail}`).toEqual([]);
  });
}
