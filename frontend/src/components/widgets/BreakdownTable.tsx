// SPDX-License-Identifier: FSL-1.1-Apache-2.0
import type { BreakdownRowData } from "../../api/types";
import { formatNumber } from "../../lib/format";

export default function BreakdownTable({
  data,
  name,
  description,
}: {
  data: BreakdownRowData[];
  name: string;
  description?: string;
}) {
  const total = data.reduce((acc, r) => acc + r.total, 0);
  return (
    <div className="widget widget--table">
      <div className="widget__title">{name}</div>
      {description && <div className="widget__desc">{description}</div>}
      {data.length === 0 ? (
        <div className="widget__empty">No data in this window.</div>
      ) : (
        <table className="widget__breakdown">
          <thead>
            <tr>
              <th>Value</th>
              <th className="num">Spans</th>
              <th className="num">Errors</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            {data.map((r) => {
              const share = total > 0 ? (r.total / total) * 100 : 0;
              return (
                <tr key={r.key}>
                  <td className="mono">{r.key}</td>
                  <td className="num">{formatNumber(r.total)}</td>
                  <td className="num">
                    {r.errors > 0 ? (
                      <span className="pill pill--errors">{formatNumber(r.errors)}</span>
                    ) : (
                      <span className="muted">—</span>
                    )}
                  </td>
                  <td className="widget__breakdown-bar-cell">
                    <div className="widget__breakdown-bar">
                      <div
                        className="widget__breakdown-bar-fill"
                        style={{ width: `${share}%` }}
                      />
                    </div>
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      )}
    </div>
  );
}
