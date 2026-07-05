// SPDX-License-Identifier: FSL-1.1-Apache-2.0
import {
  CartesianGrid,
  Legend,
  Line,
  LineChart,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from "recharts";
import type { LatencyPointData } from "../../api/types";
import { hhmm, useChartTheme } from "./chartTheme";

export default function LatencyChart({
  data,
  name,
  description,
}: {
  data: LatencyPointData[];
  name: string;
  description?: string;
}) {
  const { axisStyle, gridStyle, tooltipStyle, legendStyle, colors } = useChartTheme();
  const series = data.map((p) => ({
    ts: hhmm(p.ts),
    p50: +p.p50_ms.toFixed(1),
    p95: +p.p95_ms.toFixed(1),
    p99: +p.p99_ms.toFixed(1),
  }));
  return (
    <div className="widget widget--chart">
      <div className="widget__title">{name}</div>
      {description && <div className="widget__desc">{description}</div>}
      <div className="widget__chart">
        {series.length === 0 ? (
          <div className="widget__empty">No data in this window.</div>
        ) : (
          <ResponsiveContainer width="100%" height={240}>
            <LineChart data={series}>
              <CartesianGrid {...gridStyle} />
              <XAxis dataKey="ts" {...axisStyle} />
              <YAxis
                tickFormatter={(v: number) => `${v} ms`}
                {...axisStyle}
              />
              <Tooltip
                {...tooltipStyle}
                formatter={(value: number) => [`${value} ms`, undefined]}
              />
              <Legend {...legendStyle} />
              <Line type="monotone" dataKey="p50" stroke={colors.accent} strokeWidth={2} dot={false} name="p50" />
              <Line type="monotone" dataKey="p95" stroke={colors.warning} strokeWidth={2} dot={false} name="p95" />
              <Line type="monotone" dataKey="p99" stroke={colors.critical} strokeWidth={2} dot={false} name="p99" />
            </LineChart>
          </ResponsiveContainer>
        )}
      </div>
    </div>
  );
}
