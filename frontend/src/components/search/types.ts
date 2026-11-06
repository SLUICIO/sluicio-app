// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Shared types for the Message search page.

import type { Filter } from "./FilterEditor";
import type { MessageColumn, MessageViewVisibility } from "../../api/types";

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
  description?: string;
  filters: Filter[];
  mine: boolean;
  pinned: boolean;
  visibility: MessageViewVisibility;
  /** The team of a team view, and its name. */
  groupId?: string;
  groupName?: string;
  /** Whether you may change or delete it. A draft is yours, so true. */
  canManage: boolean;
  resultCount?: number;
  lastEditedAt?: string;
  /** The view's own column set for the Messages table. Absent means it
   *  inherits the integration's; empty means it deliberately shows
   *  none. Carried so loading a view restores the table it was saved
   *  with, not just the filters. */
  messageColumns?: MessageColumn[];
  // scope is empty for global views, populated for views pinned to an
  // entity. The Search page reads scope.integrationId to route the
  // user back to the entity's tab when they open a scoped view.
  scope?: SavedViewScope;
}
