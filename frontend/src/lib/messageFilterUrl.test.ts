// SPDX-License-Identifier: FSL-1.1-Apache-2.0
import { describe, it, expect } from "vitest";
import { hydrateFiltersFromUrl, writeFiltersToParams } from "./messageFilterUrl";

describe("hydrateFiltersFromUrl", () => {
  it("reads a status filter from ?s", () => {
    const out = hydrateFiltersFromUrl("?s=err");
    expect(out).toHaveLength(1);
    expect(out[0]).toMatchObject({ field: "status", op: "is", value: "err" });
  });

  it("reads payload filters from ?q", () => {
    const out = hydrateFiltersFromUrl("?q=orderId:123,region:eu");
    expect(out.map((f) => [f.fieldPath, f.value])).toEqual([
      ["orderId", "123"],
      ["region", "eu"],
    ]);
  });

  it("strips a leading payload. prefix", () => {
    const [f] = hydrateFiltersFromUrl("?q=payload.orderId:5");
    expect(f).toMatchObject({ field: "payload", fieldPath: "orderId", value: "5" });
  });

  it("ignores malformed chunks", () => {
    expect(hydrateFiltersFromUrl("?q=nocolon")).toHaveLength(0);
  });
});

describe("writeFiltersToParams", () => {
  it("serialises status + payload rows and skips optional/locked", () => {
    const params = new URLSearchParams();
    writeFiltersToParams(
      [
        { id: "1", field: "status", op: "is", value: "err", removable: true },
        { id: "2", field: "payload", fieldPath: "orderId", op: "equals", value: "123", removable: true },
        { id: "3", field: "payload", fieldPath: "scope", op: "equals", value: "x", locked: true, removable: false },
      ],
      params,
    );
    expect(params.get("s")).toBe("err");
    expect(params.get("q")).toBe("orderId:123");
  });
});

describe("round-trip", () => {
  it("hydrate → write reproduces the original query", () => {
    const filters = hydrateFiltersFromUrl("?s=err&q=orderId:123,region:eu");
    const params = new URLSearchParams();
    writeFiltersToParams(filters, params);
    expect(params.get("s")).toBe("err");
    expect(params.get("q")).toBe("orderId:123,region:eu");
  });
});
