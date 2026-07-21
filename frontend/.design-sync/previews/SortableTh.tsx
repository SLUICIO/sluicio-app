// SPDX-License-Identifier: FSL-1.1-Apache-2.0
import { SortableTh } from "@sluicio/frontend";

const noop = () => {};
const frame: React.CSSProperties = {
  padding: 24,
  background: "var(--surface)",
  fontFamily: "Inter, system-ui, sans-serif",
};

// A sortable table header row: the active column shows its direction
// (▲ ascending here), inactive columns show the neutral ↕ affordance.
// Pair each <th> with the useTableSort hook (state + onSort) in real use.
export const TableHead = () => (
  <div style={frame}>
    <table style={{ borderCollapse: "collapse", width: 420 }}>
      <thead>
        <tr>
          <SortableTh sortKey="service" state={{ key: "service", dir: "asc" }} onSort={noop}>
            Service
          </SortableTh>
          <SortableTh sortKey="traces" state={{ key: "service", dir: "asc" }} onSort={noop} className="num">
            Traces
          </SortableTh>
          <SortableTh sortKey="errors" state={{ key: "service", dir: "asc" }} onSort={noop} className="num">
            Errors
          </SortableTh>
        </tr>
      </thead>
      <tbody>
        <tr><td>order-gateway</td><td className="num">12,481</td><td className="num">92</td></tr>
        <tr><td>order-processor</td><td className="num">8,204</td><td className="num">0</td></tr>
        <tr><td>payments</td><td className="num">5,617</td><td className="num">3</td></tr>
      </tbody>
    </table>
  </div>
);

// Descending state on the active column.
export const Descending = () => (
  <div style={frame}>
    <table style={{ borderCollapse: "collapse", width: 420 }}>
      <thead>
        <tr>
          <SortableTh sortKey="service" state={{ key: "errors", dir: "desc" }} onSort={noop}>
            Service
          </SortableTh>
          <SortableTh sortKey="errors" state={{ key: "errors", dir: "desc" }} onSort={noop} className="num">
            Errors
          </SortableTh>
        </tr>
      </thead>
      <tbody>
        <tr><td>order-gateway</td><td className="num">92</td></tr>
        <tr><td>payments</td><td className="num">3</td></tr>
      </tbody>
    </table>
  </div>
);
