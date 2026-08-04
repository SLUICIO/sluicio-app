// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// "My default" has to actually decide something.
//
// It used to lose to the last-used board on every visit after the first,
// which meant one click on any other tab retired the setting for good.
// These tests state the precedence so it cannot quietly invert again.

import { describe, expect, it } from "vitest";
import { orderDashboards, pickActiveDashboard } from "./dashboardPick";
import type { Dashboard } from "../api/types";

function dash(over: Partial<Dashboard> & { id: string }): Dashboard {
  return {
    name: over.id,
    isDefault: false,
    autoIncludeAll: false,
    defaultWidgetType: "traffic_sparkline",
    position: 0,
    mine: true,
    items: [],
    createdAt: "2026-08-04T00:00:00Z",
    updatedAt: "2026-08-04T00:00:00Z",
    ...over,
  };
}

describe("orderDashboards", () => {
  it("puts the default first however the server ordered it", () => {
    const list = [
      dash({ id: "a", position: 0 }),
      dash({ id: "b", position: 1 }),
      dash({ id: "c", position: 2, isDefault: true }),
    ];
    expect(orderDashboards(list).map((d) => d.id)).toEqual(["c", "a", "b"]);
  });

  it("keeps the server's order among the rest", () => {
    const list = [
      dash({ id: "c", position: 2 }),
      dash({ id: "a", position: 0 }),
      dash({ id: "b", position: 1 }),
    ];
    expect(orderDashboards(list).map((d) => d.id)).toEqual(["a", "b", "c"]);
  });

  it("breaks a position tie by creation, so the strip cannot flicker", () => {
    const list = [
      dash({ id: "younger", position: 0, createdAt: "2026-08-04T10:00:00Z" }),
      dash({ id: "older", position: 0, createdAt: "2026-08-04T09:00:00Z" }),
    ];
    expect(orderDashboards(list).map((d) => d.id)).toEqual(["older", "younger"]);
  });

  it("does not mutate the array it was given", () => {
    // It is React state; sorting in place would skip re-renders.
    const list = [dash({ id: "a", position: 1 }), dash({ id: "b", position: 0 })];
    orderDashboards(list);
    expect(list.map((d) => d.id)).toEqual(["a", "b"]);
  });
});

describe("pickActiveDashboard", () => {
  it("lands on the default even when another board was last used", () => {
    // The reported behaviour: this returned "b" before.
    const list = [dash({ id: "a", isDefault: true }), dash({ id: "b" })];
    expect(pickActiveDashboard(list, "b")?.id).toBe("a");
  });

  it("falls back to the last-used board when nothing is default", () => {
    const list = [dash({ id: "a" }), dash({ id: "b" })];
    expect(pickActiveDashboard(list, "b")?.id).toBe("b");
  });

  it("ignores a remembered board that no longer exists", () => {
    // Deleted on another device, or by someone else.
    const list = [dash({ id: "a", position: 0 }), dash({ id: "b", position: 1 })];
    expect(pickActiveDashboard(list, "gone")?.id).toBe("a");
  });

  it("falls back to the FIRST TAB, not the server's first row", () => {
    // Otherwise the landing board and the leftmost tab disagree.
    const list = [dash({ id: "a", position: 0 }), dash({ id: "b", position: 1 })];
    expect(pickActiveDashboard(list, null)?.id).toBe("a");
  });

  it("returns null only when there is nothing to show", () => {
    expect(pickActiveDashboard([], null)).toBeNull();
    expect(pickActiveDashboard([], "anything")).toBeNull();
  });
});
