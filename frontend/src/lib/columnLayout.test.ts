// SPDX-License-Identifier: FSL-1.1-Apache-2.0

import { describe, expect, it } from "vitest";
import { resolveColumnOrder, resolveHiddenColumns, type LayoutSources } from "./columnLayout";

const DEFAULTS = ["name", "description", "slug", "traces"];
const sources = (patch: Partial<LayoutSources>): LayoutSources => ({
  session: null,
  colsParam: null,
  saved: null,
  legacy: null,
  defaultOrder: DEFAULTS,
  ...patch,
});

describe("column layout", () => {
  it("falls back to the default order when nobody has said anything", () => {
    expect(resolveColumnOrder(sources({}))).toEqual(DEFAULTS);
    expect([...resolveHiddenColumns(sources({}))]).toEqual([]);
  });

  // The one this is really about: what the reader just did outranks the
  // URL, so the control does not wait for a navigation to land.
  it("lets this visit's choice win over the link that was opened", () => {
    const s = sources({
      colsParam: "name,description,slug,traces",
      session: { order: ["slug", "name"], hidden: ["description"] },
    });
    expect(resolveColumnOrder(s)).toEqual(["slug", "name", "description", "traces"]);
    expect([...resolveHiddenColumns(s)]).toEqual(["description"]);
  });

  it("reads a link's ?cols= as the visible list it is", () => {
    const s = sources({ colsParam: "slug,name" });
    expect(resolveColumnOrder(s)).toEqual(["slug", "name", "description", "traces"]);
    expect([...resolveHiddenColumns(s)].sort()).toEqual(["description", "traces"]);
  });

  // A saved layout stores what is HIDDEN, so a column shipped later is
  // visible for a reader who saved one before it existed.
  it("shows a new column to somebody with a saved layout", () => {
    const s = sources({ saved: { order: ["name", "description"], hidden: ["description"] } });
    expect(resolveColumnOrder(s)).toEqual(["name", "description", "slug", "traces"]);
    expect([...resolveHiddenColumns(s)]).toEqual(["description"]);
  });

  it("hides a new column from an old ?cols= link, which is a visible list", () => {
    expect([...resolveHiddenColumns(sources({ colsParam: "name,description,slug" }))]).toEqual(["traces"]);
  });

  it("drops a column that no longer exists", () => {
    const s = sources({ session: { order: ["slug", "meta:gone", "name"], hidden: ["meta:gone"] } });
    expect(resolveColumnOrder(s)).toEqual(["slug", "name", "description", "traces"]);
    expect([...resolveHiddenColumns(s)]).toEqual([]);
  });

  it("uses the legacy value only when nothing better exists", () => {
    expect([...resolveHiddenColumns(sources({ legacy: "name" }))].sort()).toEqual([
      "description",
      "slug",
      "traces",
    ]);
    expect([...resolveHiddenColumns(sources({ legacy: "name", saved: { order: [], hidden: [] } }))]).toEqual([]);
  });
});
