// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// The connector geometry, which is invisible in code review and obvious
// on screen. A narrow card wraps after every column, and then the source
// and the target of an edge share an x - the case the lane routing was
// never written for.

import { describe, expect, it } from "vitest";
import { detour } from "./SpanGraph";

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
