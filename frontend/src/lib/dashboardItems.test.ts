// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// The property under test is SURVIVAL: a pinned card must come out of
// materialize→save as the same kind of card it went in as.
//
// The failure this guards against is silent. Both functions used to
// branch on `entityKind === "system"`, so a third kind was dropped by
// the materializer and misrouted by the write mapper — the user pins a
// system, saves, and it vanishes with no error to explain it.

import { describe, expect, it } from "vitest";
import { materializeItems, toItemRequest } from "./dashboardItems";
import type { Dashboard, DashboardItem, Integration } from "../api/types";

function item(over: Partial<DashboardItem>): DashboardItem {
  return {
    id: "i",
    entityKind: "integration",
    integrationId: "",
    widgetType: "traffic_sparkline",
    position: 0,
    createdAt: "2026-08-02T00:00:00Z",
    ...over,
  };
}

function dash(over: Partial<Dashboard>): Dashboard {
  return {
    id: "d",
    name: "Board",
    isDefault: false,
    autoIncludeAll: false,
    defaultWidgetType: "traffic_sparkline",
    position: 0,
    mine: true,
    items: [],
    createdAt: "2026-08-02T00:00:00Z",
    updatedAt: "2026-08-02T00:00:00Z",
    ...over,
  };
}

const integ = (id: string, traces = 0): Integration =>
  ({ id, name: id, trace_count: traces }) as Integration;

describe("materializeItems", () => {
  it("carries system entities through the auto-include expansion", () => {
    const d = dash({
      autoIncludeAll: true,
      items: [item({ entityKind: "system_entity", systemId: "sys-1", widgetType: "system_health" })],
    });
    const out = materializeItems(d, [integ("int-1")]);
    expect(out.filter((i) => i.entityKind === "system_entity")).toHaveLength(1);
    expect(out.find((i) => i.entityKind === "system_entity")?.systemId).toBe("sys-1");
  });

  it("carries both system kinds at once and keeps them distinct", () => {
    const d = dash({
      autoIncludeAll: true,
      items: [
        item({ entityKind: "system", systemName: "svc-a", widgetType: "system_health" }),
        item({ entityKind: "system_entity", systemId: "sys-1", widgetType: "system_health" }),
      ],
    });
    const kinds = materializeItems(d, [integ("int-1")]).map((i) => i.entityKind);
    expect(kinds.filter((k) => k === "system")).toHaveLength(1);
    expect(kinds.filter((k) => k === "system_entity")).toHaveLength(1);
    expect(kinds.filter((k) => k === "integration")).toHaveLength(1);
  });

  it("does not drop a kind it has never heard of", () => {
    // Generalised form of the shipped bug: a kind the server knows and
    // this file does not must still survive a save round-trip.
    const d = dash({
      autoIncludeAll: true,
      items: [item({ entityKind: "fleet" as DashboardItem["entityKind"] })],
    });
    expect(materializeItems(d, [integ("int-1")])).toHaveLength(2);
  });

  it("leaves a manual dashboard's items alone", () => {
    const items = [
      item({ integrationId: "int-1" }),
      item({ entityKind: "system_entity", systemId: "sys-1" }),
    ];
    expect(materializeItems(dash({ items }), [])).toEqual(items);
  });
});

describe("toItemRequest", () => {
  it("emits the system-entity form keyed by id", () => {
    const r = toItemRequest(
      item({ entityKind: "system_entity", systemId: "sys-1", position: 3 }),
      0,
    );
    expect(r).toEqual({
      entityKind: "system_entity",
      systemId: "sys-1",
      widgetType: "system_health",
      position: 3,
    });
    // An entity item must never carry a name — the server's shape
    // constraint rejects a row with both, and silently sending one
    // would 400 the whole save.
    expect(r.systemName).toBeUndefined();
  });

  it("emits the name-keyed form for the older system card", () => {
    const r = toItemRequest(item({ entityKind: "system", systemName: "svc-a" }), 0);
    expect(r.entityKind).toBe("system");
    expect(r.systemName).toBe("svc-a");
    expect(r.systemId).toBeUndefined();
  });

  it("never turns a system entity into an integration request", () => {
    // The exact misroute: falling through to the integration branch
    // posts integrationId:"" and the save fails or writes a blank card.
    const r = toItemRequest(item({ entityKind: "system_entity", systemId: "sys-1" }), 0);
    expect(r.integrationId).toBeUndefined();
  });

  it("falls back to the list index when an item has no position", () => {
    const i = item({ integrationId: "int-1" });
    delete (i as Partial<DashboardItem>).position;
    expect(toItemRequest(i, 7).position).toBe(7);
  });
});
