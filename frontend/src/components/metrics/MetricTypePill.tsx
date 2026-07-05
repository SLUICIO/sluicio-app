// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// The OTLP metric-type pill. A monotonic sum is shown as "counter" (the
// term the UI uses); other types pass through. Colour is keyed to the
// type class in styles.css.

export function metricTypeLabel(type: string): string {
  return type === "sum" ? "counter" : type;
}

export default function MetricTypePill({ type }: { type: string }) {
  return <span className={`m-type-pill t-${type}`}>{metricTypeLabel(type)}</span>;
}
