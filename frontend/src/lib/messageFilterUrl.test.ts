// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Reported: "filters reset when you refresh the page". Add a filter with
// any operator other than equals, reload (or duplicate the tab, or open
// the link you copied), and every operator is equals again.
//
// The URL is the state of this page. A link that comes back as a
// different search is worse than a link that comes back empty: the
// reader gets an answer to a question they did not ask, and nothing on
// screen says so.

import { describe, expect, it } from "vitest";
import { hydrateFiltersFromUrl, writeFiltersToParams } from "./messageFilterUrl";
import type { Filter } from "../components/search/FilterEditor";

const row = (patch: Partial<Filter>): Filter => ({
  id: "x",
  field: "payload",
  fieldPath: "order.id",
  op: "equals",
  value: "ORD-1",
  removable: true,
  ...patch,
});

// What a link does to a filter set: write it, then read it back.
function roundTrip(filters: Filter[]): Filter[] {
  const params = new URLSearchParams();
  writeFiltersToParams(filters, params);
  return hydrateFiltersFromUrl(params.toString());
}

describe("a filter survives the URL", () => {
  it("keeps the operator", () => {
    for (const op of ["equals", "contains", "matches", "not_equals", "not_contains"] as const) {
      const back = roundTrip([row({ op })]);
      expect(back, `${op} lost its row`).toHaveLength(1);
      expect(back[0].op, `${op} came back as ${back[0].op}`).toBe(op);
      expect(back[0].value).toBe("ORD-1");
      expect(back[0].fieldPath).toBe("order.id");
    }
  });

  it("keeps a row that asks about the key rather than the value", () => {
    for (const op of ["exists", "not_exists"] as const) {
      const back = roundTrip([row({ op, value: "" })]);
      expect(back, `${op} disappeared`).toHaveLength(1);
      expect(back[0].op).toBe(op);
    }
  });

  it("keeps the fields that are not payload", () => {
    const back = roundTrip([
      row({ field: "service", fieldPath: undefined, op: "is", value: "order-api" }),
      row({ field: "errorType", fieldPath: undefined, op: "contains", value: "timeout" }),
      row({ field: "traceId", fieldPath: undefined, op: "is", value: "abc123" }),
    ]);
    expect(back.map((f) => [f.field, f.op, f.value])).toEqual([
      ["service", "is", "order-api"],
      ["errorType", "contains", "timeout"],
      ["traceId", "is", "abc123"],
    ]);
  });

  it("keeps the child-spans switch", () => {
    const back = roundTrip([row({ op: "equals", includeDescendants: true })]);
    expect(back[0].includeDescendants).toBe(true);
  });

  it("keeps a value with a comma or a colon in it", () => {
    const back = roundTrip([row({ value: "a,b:c" })]);
    expect(back[0].value).toBe("a,b:c");
  });

  it("still reads the short form older links use", () => {
    const back = hydrateFiltersFromUrl("q=order.id:ORD-1&s=err%20only");
    expect(back.find((f) => f.field === "payload")?.op).toBe("equals");
    expect(back.find((f) => f.field === "payload")?.value).toBe("ORD-1");
    expect(back.find((f) => f.field === "status")?.value).toBe("err only");
  });

  it("writes the short form when there is nothing to add", () => {
    const params = new URLSearchParams();
    writeFiltersToParams([row({})], params);
    expect(params.get("q")).toBe("order.id:ORD-1");
  });
});
