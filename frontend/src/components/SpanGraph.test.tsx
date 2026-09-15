// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// The connector geometry, which is invisible in code review and obvious
// on screen. A narrow card wraps after every column, and then the source
// and the target of an edge share an x - the case the lane routing was
// never written for.

import { describe, expect, it } from "vitest";
import { crossesABox, detour, sideRoute } from "./SpanGraph";

describe("detour", () => {
  // The reported symptom: between two vertically stacked steps the line
  // vanished and two arrowheads pointed at each other. The path was
  // going left eight pixels, back right sixteen, then up - a squiggle in
  // the gap rather than a connector.
  it("drops straight down when the target is in the same column", () => {
    const d = detour({ x: 300, y: 100 }, { x: 300, y: 200 }, 150);
    expect(d).toBe("M300,100 V200");
    expect(d).not.toContain("H");
    expect(d).not.toContain("Q");
  });

  // Sub-pixel drift from the layout maths must not resurrect the jog.
  it("treats a sub-pixel difference as the same column", () => {
    expect(detour({ x: 300, y: 100 }, { x: 300.4, y: 200 }, 150)).toBe("M300,100 V200");
  });

  // A real carriage return still routes under the row: down, across the
  // lane, up. Collapsing that one would draw a diagonal through every
  // box between the two ends.
  it("routes through the lane when the target is in another column", () => {
    const d = detour({ x: 700, y: 100 }, { x: 120, y: 200 }, 150);
    expect(d).toContain("M700,100");
    expect(d).toContain("V142"); // down to the lane, less the corner radius
    expect(d).toContain("H128"); // across it
    expect(d).toContain("V200"); // up into the target
    expect(d).toContain("Q"); // rounded corners, so it reads as one line
  });

  it("routes left to right as well", () => {
    const d = detour({ x: 120, y: 100 }, { x: 700, y: 200 }, 150);
    expect(d).toContain("H692");
  });
});

describe("crossesABox", () => {
  // The reported symptom: three connectors leaving one step all started
  // at the same point and ran collinear straight down through every box
  // between, so the arrowheads scattered along the line read as arrows
  // pointing in both directions.
  const NODE_W = 168;
  const NODE_H = 46;
  const box = (x: number, y: number) => ({ x, y });

  it("sees a box standing in the way of a straight drop", () => {
    const src = { x: 100 + NODE_W / 2, y: 100 + NODE_H };
    const dst = { x: 100 + NODE_W / 2, y: 300 };
    expect(crossesABox(src, dst, [box(100, 200)])).toBe(true);
  });

  it("lets the next step down through", () => {
    const src = { x: 100 + NODE_W / 2, y: 100 + NODE_H };
    const dst = { x: 100 + NODE_W / 2, y: 200 };
    expect(crossesABox(src, dst, [box(100, 300)])).toBe(false);
  });

  it("ignores a box in another column", () => {
    const src = { x: 100 + NODE_W / 2, y: 100 + NODE_H };
    const dst = { x: 100 + NODE_W / 2, y: 300 };
    expect(crossesABox(src, dst, [box(400, 200)])).toBe(false);
  });

  it("does not fire when the ends are not aligned", () => {
    const src = { x: 100 + NODE_W / 2, y: 100 + NODE_H };
    const dst = { x: 400 + NODE_W / 2, y: 300 };
    expect(crossesABox(src, dst, [box(100, 200)])).toBe(false);
  });
});

describe("sideRoute", () => {
  it("runs down the lane it is given and arrives from above", () => {
    const d = sideRoute({ x: 300, y: 100 }, { x: 300, y: 300 }, 380);
    expect(d.startsWith("M300,100")).toBe(true);
    expect(d).toContain("H372");
    expect(d.endsWith("V300")).toBe(true);
  });

  // The lane sits outside the column, so the layout has to measure the
  // box wide enough to hold it. It used to be routed at render time and
  // the viewBox cut the detour off at the edge of the card.
  it("stays on the side of the lane, never crossing back over the source", () => {
    const d = sideRoute({ x: 300, y: 100 }, { x: 300, y: 300 }, 380);
    const xs = [...d.matchAll(/[MHQ](-?\d+(?:\.\d+)?)/g)].map((m) => Number(m[1]));
    expect(Math.min(...xs)).toBeGreaterThanOrEqual(300);
    expect(Math.max(...xs)).toBeLessThanOrEqual(380);
  });
});
