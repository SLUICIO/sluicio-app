// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// AuthCard — the centered card shell shared by the unauthenticated pages
// (Login, ResetPassword). Keeps their chrome identical.

import type { ReactNode } from "react";

export function AuthCard({ children }: { children: ReactNode }) {
  return (
    <div
      style={{
        minHeight: "100vh",
        display: "grid",
        placeItems: "center",
        background: "var(--surface)",
        color: "var(--ink)",
        padding: "24px 16px",
      }}
    >
      <div
        className="card"
        style={{
          width: "100%",
          maxWidth: 380,
          padding: 28,
          background: "var(--surface-2)",
          border: "1px solid var(--border)",
          borderRadius: 12,
          display: "flex",
          flexDirection: "column",
        }}
      >
        {children}
      </div>
    </div>
  );
}
