// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Renders an OTLP log severity as a colored pill. OTLP SeverityNumber
// bands: INFO=9-12, WARN=13-16, ERROR=17-20, FATAL=21-24. ERROR+ is
// red, WARN amber, everything below is muted text.

export default function SeverityBadge({ text, num }: { text: string; num: number }) {
  const label = text || `S${num}`;
  if (num >= 17) return <span className="pill pill--errors">{label}</span>;
  if (num >= 13) return <span className="pill pill--quiet">{label}</span>;
  return (
    <span className="mono muted" style={{ fontSize: 12 }}>
      {label}
    </span>
  );
}
