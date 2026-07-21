// SPDX-License-Identifier: FSL-1.1-Apache-2.0
import { SeverityBadge } from "@sluicio/frontend";

const frame: React.CSSProperties = {
  padding: 24,
  background: "var(--surface)",
  display: "flex",
  gap: 12,
  alignItems: "center",
  fontFamily: "Inter, system-ui, sans-serif",
};

// The OTLP severity bands as they render in the log tables: ERROR+ red,
// WARN amber, INFO/DEBUG muted mono text. `num` is the OTLP SeverityNumber.
export const Levels = () => (
  <div style={frame}>
    <SeverityBadge text="DEBUG" num={5} />
    <SeverityBadge text="INFO" num={9} />
    <SeverityBadge text="WARN" num={13} />
    <SeverityBadge text="ERROR" num={17} />
    <SeverityBadge text="FATAL" num={21} />
  </div>
);

// Falls back to "S<num>" when a log carries a severity number but no text.
export const NumericFallback = () => (
  <div style={frame}>
    <SeverityBadge text="" num={10} />
    <SeverityBadge text="" num={14} />
    <SeverityBadge text="" num={18} />
  </div>
);
