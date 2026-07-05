// SPDX-License-Identifier: FSL-1.1-Apache-2.0
import {
  CartesianGrid,
  Line,
  LineChart,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from "recharts";
import type { ErrorRatePointData } from "../../api/types";
import { hhmm, useChartTheme } from "./chartTheme";

export default function ErrorRateChart({
  data,
  name,
  description,
}: {
  data: ErrorRatePointData[];
  name: string;
  description?: string;
}) {
  const { axisStyle, gridStyle, tooltipStyle, colors } = useChartTheme();
  const series = data.map((p) => ({
    ts: hhmm(p.ts),
    ratePct: +(p.rate * 100).toFixed(2),
    errors: p.errors,
    total: p.total,
  }));
  return (
    <div className="widget widget--chart">
      <div className="widget__title">{name}</div>
      {description && <div className="widget__desc">{description}</div>}
      <div className="widget__chart">
        {series.length === 0 ? (
          <div className="widget__empty">No data in this window.</div>
        ) : (
          <ResponsiveContainer width="100%" height={220}>
            <LineChart data={series}>
              <CartesianGrid {...gridStyle} />
              <XAxis dataKey="ts" {...axisStyle} />
              <YAxis
                yAxisId="rate"
                domain={[0, "auto"]}
                tickFormatter={(v: number) => `${v}%`}
                {...axisStyle}
              />
              <Tooltip
                {...tooltipStyle}
                formatter={(value: number, name: string) => {
                  if (name === "Error rate") return [`${value}%`, name];
                  return [value, name];
                }}
              />
              <Line
                yAxisId="rate"
                type="monotone"
                dataKey="ratePct"
                stroke={colors.critical}
                strokeWidth={2}
                dot={false}
                name="Error rate"
              />
            </LineChart>
          </ResponsiveContainer>
        )}
      </div>
    </div>
  );
}
