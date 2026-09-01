// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Service names in a dependency graph share long prefixes AND suffixes,
// which is what makes truncation the wrong tool: shortened,
// product-consolidation-aggregator-service and
// product-consolidation-extractor-service render identically and the
// graph stops distinguishing the things it exists to distinguish. The
// box sizes to the name instead.

import { describe, expect, it } from "vitest";
import { fitServiceName, nodeWidthFor } from "./ServiceFlowGraph";

const LONG = "product-consolidation-aggregator-service";
const SIBLING = "product-consolidation-extractor-service";

describe("nodeWidthFor", () => {
  it("keeps the old width for names that always fitted", () => {
    expect(nodeWidthFor(["checkout-api", "orders"])).toBe(168);
  });

  // The reported symptom: 40 characters centred in a 168px box ran over
  // the neighbours on both sides.
  it("widens for a long name", () => {
    const w = nodeWidthFor([LONG]);
    expect(w).toBeGreaterThan(168);
    expect(fitServiceName(LONG, w)).toBe(LONG);
  });

  it("sizes to the longest name, not the first", () => {
    expect(nodeWidthFor(["a", LONG, "b"])).toBe(nodeWidthFor([LONG]));
  });

  // One pathological name must not push the rest of the flow off screen.
  it("caps the width", () => {
    expect(nodeWidthFor(["x".repeat(500)])).toBe(320);
  });

  it("handles an empty graph", () => {
    expect(nodeWidthFor([])).toBe(168);
  });
});

describe("fitServiceName", () => {
  it("leaves a name that fits its box", () => {
    expect(fitServiceName("checkout-api", 168)).toBe("checkout-api");
  });

  // Past the cap there is no way to show it all, and the node carries a
  // title so the full name is one hover away.
  it("shortens only past the cap", () => {
    const name = "x".repeat(200);
    const got = fitServiceName(name, 320);
    expect(got.endsWith("…")).toBe(true);
    expect(got.length).toBeLessThan(name.length);
  });

  // The case that sent this back to the drawing board: at 168px both of
  // these truncated to the same string. Sized to fit, they do not.
  it("keeps siblings with a shared prefix and suffix distinct", () => {
    const w = nodeWidthFor([LONG, SIBLING]);
    expect(fitServiceName(LONG, w)).not.toBe(fitServiceName(SIBLING, w));
  });
});

// The invariant that ties the two together, and the one a second
// per-character estimate broke: if the box was sized for the name, the
// name is not then cut to fit the box.
describe("sizing and shortening agree", () => {
  it("never truncates a name the box was widened for", () => {
    for (const n of [1, 5, 12, 23, 39, 40, 44]) {
      const name = "a".repeat(n);
      const w = nodeWidthFor([name]);
      if (w < 320) {
        expect(fitServiceName(name, w)).toBe(name);
      }
    }
  });
});
