// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Shared types for the Message search page.

import type { Filter } from "./FilterEditor";

// SavedViewScope pins a view to a specific entity (an integration, a
// service, …). The same view appears on that entity's Messages tab
// and on the global Message Search rail with an "in <entity>" badge.
// Empty fields = global view.
export interface SavedViewScope {
  integrationId?: string;
  // Human-readable label for the scope, used in badges. Filled in by
  // the page that owns the scope (e.g. the integration's name); not
  // persisted server-side.
  integrationName?: string;
  serviceId?: string;
}

export interface SavedView {
  id: string;
  name: string;
  filters: Filter[];
  mine: boolean;
  pinned: boolean;
  sharedWith?: string[];
  resultCount?: number;
  lastEditedAt?: string;
  // scope is empty for global views, populated for views pinned to an
  // entity. The Search page reads scope.integrationId to route the
  // user back to the entity's tab when they open a scoped view.
  scope?: SavedViewScope;
}
