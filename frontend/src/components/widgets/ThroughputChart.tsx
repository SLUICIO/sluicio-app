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
import type { TimePointData } from "../../api/types";
import { hhmm, useChartTheme } from "./chartTheme";

export default function ThroughputChart({
  data,
  name,
  description,
}: {
  data: TimePointData[];
  name: string;
  description?: string;
}) {
  const { axisStyle, gridStyle, tooltipStyle, colors } = useChartTheme();
  const series = data.map((p) => ({ ts: hhmm(p.ts), value: p.value }));
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
              <YAxis allowDecimals={false} {...axisStyle} />
              <Tooltip {...tooltipStyle} />
              <Line
                type="monotone"
                dataKey="value"
                stroke={colors.accent}
                strokeWidth={2}
                dot={false}
                name="Spans / min"
              />
            </LineChart>
          </ResponsiveContainer>
        )}
      </div>
    </div>
  );
}
