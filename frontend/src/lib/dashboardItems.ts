// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Turning a dashboard's in-memory item list into what gets SAVED.
//
// This is the step where a card can silently disappear. Both functions
// used to test for one specific kind — `entityKind === "system"` — so the
// moment a third kind arrived (system entities), the auto-include
// materializer dropped it and the write mapper fell through to the
// integration branch, posting an item with an empty integrationId. The
// user pins a system, hits Save, and it is gone with no error.
//
// So both are written as "integration, or NOT an integration", and the
// tests below assert the property rather than the branches: whatever
// items go in, the same COUNT of non-integration items comes out, and
// every one keeps its own kind.

import type {
  Dashboard,
  DashboardItem,
  DashboardItemRequest,
  Integration,
} from "../api/types";

// materializeItems turns a dashboard into an explicit list of items
// representing every card the renderer would currently show. Used by
// the remove flows: in auto-include-all mode, items[] holds only the
// per-card widget *overrides* — filtering items[] then flipping to
// manual mode would drop every non-overridden card. Materializing
// first guarantees that "remove one" is what the user sees.
export function materializeItems(
  dashboard: Dashboard,
  integrations: Integration[],
): DashboardItem[] {
  if (!dashboard.autoIncludeAll) {
    // Manual mode: items[] is already the source of truth.
    return [...dashboard.items];
  }
  const overrideById = new Map(
    dashboard.items.map((i) => [i.integrationId, i] as const),
  );
  // composeCards puts overrides first (in declared position) then
  // every remaining integration by traffic. Mirror that ordering here
  // so the persisted manual list keeps the same on-screen order.
  const overrides = dashboard.items
    .slice()
    .sort((a, b) => a.position - b.position)
    .map((i) => i.integrationId);
  const overrideSet = new Set(overrides);
  const rest = integrations
    .filter((i) => !overrideSet.has(i.id))
    .sort((a, b) => (b.trace_count ?? 0) - (a.trace_count ?? 0))
    .map((i) => i.id);
  const byId = new Map(integrations.map((i) => [i.id, i] as const));
  const ordered = [...overrides, ...rest].filter((id) => byId.has(id));
  const integrationItems: DashboardItem[] = ordered.map((id, idx) => {
    const existing = overrideById.get(id);
    return {
      id: existing?.id ?? `draft-${id}`,
      entityKind: "integration",
      integrationId: id,
      widgetType: existing?.widgetType ?? dashboard.defaultWidgetType,
      position: existing?.position ?? idx,
      createdAt: existing?.createdAt ?? new Date().toISOString(),
    };
  });
  // System items (both kinds) live in items[] too but aren't part of the
  // auto-include expansion — carry them through untouched. Written as
  // "not an integration" so a future kind is carried, not silently
  // dropped the first time someone materializes an auto-include board.
  const systemItems = dashboard.items.filter((i) => i.entityKind !== "integration");
  return [...integrationItems, ...systemItems];
}

// toItemRequest maps a (possibly draft) DashboardItem to the write shape,
// emitting the integration or system form by entityKind.
export function toItemRequest(i: DashboardItem, idx: number): DashboardItemRequest {
  if (i.entityKind === "system_entity") {
    return {
      entityKind: "system_entity",
      systemId: i.systemId,
      widgetType: "system_health",
      position: i.position ?? idx,
    };
  }
  if (i.entityKind === "system") {
    return {
      entityKind: "system",
      systemName: i.systemName,
      widgetType: "system_health",
      position: i.position ?? idx,
    };
  }
  return {
    entityKind: "integration",
    integrationId: i.integrationId,
    widgetType: i.widgetType,
    position: i.position ?? idx,
  };
}
