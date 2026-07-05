// SPDX-License-Identifier: FSL-1.1-Apache-2.0
import type { CounterData } from "../../api/types";
import { formatNumber } from "../../lib/format";

export default function CounterCard({
  data,
  name,
  description,
  tone = "neutral",
}: {
  data: CounterData;
  name: string;
  description?: string;
  tone?: "ok" | "errors" | "neutral";
}) {
  return (
    <div className={`widget widget--counter widget--${tone}`}>
      <div className="widget__title">{name}</div>
      <div className="widget__counter-value">{formatNumber(data.value)}</div>
      {data.subtitle && <div className="widget__counter-sub">{data.subtitle}</div>}
      {description && <div className="widget__desc">{description}</div>}
    </div>
  );
}
