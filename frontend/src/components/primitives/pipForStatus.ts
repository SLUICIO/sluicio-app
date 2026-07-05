// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Translate the backend ServiceStatus into the Sluicio handoff's
// wireframe vocabulary used by StatusPip. Kept in its own file so
// the StatusPip module only exports a React component (which lets
// React Fast Refresh work reliably in dev).

import type { PipKind } from "./StatusPip";

export function pipForStatus(status: string | undefined): PipKind {
  if (!status) return "muted";
  if (status === "ok") return "ok";
  if (status === "quiet") return "muted";
  if (status === "errors" || status === "unhealthy") return "err";
  return "muted";
}
