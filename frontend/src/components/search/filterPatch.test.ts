// SPDX-License-Identifier: FSL-1.1-Apache-2.0

import { describe, expect, it } from "vitest";
import { applyFilterPatch, type Filter } from "./FilterEditor";

const muted = (over: Partial<Filter> = {}): Filter => ({
  id: "f1",
  field: "payload",
  fieldPath: "customer.id",
  op: "equals",
  value: "",
  removable: true,
  optional: true,
  ...over,
});

describe("applyFilterPatch", () => {
  // The one that matters. An integration's configured filter fields are
  // seeded as muted rows, and muted rows are dropped from the query. A
  // row that stayed muted after somebody typed in it would look like a
  // live filter and narrow nothing.
  it("un-mutes a muted row when it gets a value", () => {
    const next = applyFilterPatch(muted(), { value: "C-42" });
    expect(next.optional).toBe(false);
    expect(next.value).toBe("C-42");
  });

  it("mutes it again when the value is cleared", () => {
    const filled = applyFilterPatch(muted(), { value: "C-42" });
    const emptied = applyFilterPatch({ ...filled, optional: true }, { value: "" });
    expect(emptied.optional).toBe(true);
  });

  // Whitespace is not a value. A row holding " " would otherwise count
  // as a live filter matching nothing.
  it("treats whitespace as empty", () => {
    expect(applyFilterPatch(muted(), { value: "   " }).optional).toBe(true);
  });

  it("leaves a row that was never muted alone", () => {
    const plain = muted({ optional: undefined });
    expect(applyFilterPatch(plain, { value: "x" }).optional).toBeUndefined();
    expect(applyFilterPatch(plain, { value: "" }).optional).toBeUndefined();
  });

  it("passes other edits through untouched", () => {
    const next = applyFilterPatch(muted({ value: "x", optional: false }), { op: "contains" });
    expect(next.op).toBe("contains");
    expect(next.fieldPath).toBe("customer.id");
  });
});
