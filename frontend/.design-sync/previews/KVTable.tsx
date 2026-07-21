// SPDX-License-Identifier: FSL-1.1-Apache-2.0
import { KVTable } from "@sluicio/frontend";

const frame: React.CSSProperties = {
  padding: 24,
  background: "var(--surface)",
  maxWidth: 460,
  fontFamily: "Inter, system-ui, sans-serif",
};

// Mono variant — the code-style key/value list used in span + log detail
// drawers. `copyValue` adds a copy button per row.
export const Attributes = () => (
  <div style={frame}>
    <KVTable
      variant="mono"
      bordered
      rows={[
        { k: "service.name", v: "order-gateway", copyValue: "order-gateway" },
        { k: "trace.id", v: "a3cd07f6dca74119abe443fc", copyValue: "a3cd07f6dca74119abe443fc" },
        { k: "http.method", v: "POST", copyValue: "POST" },
        { k: "http.status_code", v: "500", copyValue: "500" },
        { k: "net.peer.name", v: "payments.internal", copyValue: "payments.internal" },
      ]}
    />
  </div>
);

// Prose variant — human-readable labels + formatted values, with JSX
// values (a ✓ affordance) that omit the copy button.
export const Prose = () => (
  <div style={frame}>
    <KVTable
      variant="prose"
      bordered
      rows={[
        { k: "Environment", v: "production" },
        { k: "Owner", v: "Platform team" },
        { k: "Encrypted", v: <span style={{ color: "var(--ok)" }}>✓ yes</span> },
        { k: "Last deploy", v: "3 hours ago" },
      ]}
    />
  </div>
);

// Empty state.
export const Empty = () => (
  <div style={frame}>
    <KVTable rows={[]} emptyLabel="No attributes on this span." />
  </div>
);
