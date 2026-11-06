// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// How a saved view's visibility reads wherever a view is listed, so the
// rail, the view header and the open-saved menu say the same thing.

import type { SavedView } from "./types";

export function visibilityLabel(v: Pick<SavedView, "visibility" | "groupName">): string {
  switch (v.visibility) {
    case "private":
      return "private";
    case "team":
      return v.groupName ? `team · ${v.groupName}` : "team";
    default:
      return "org-wide";
  }
}
