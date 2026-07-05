// SPDX-License-Identifier: FSL-1.1-Apache-2.0
import type {
  BreakdownRowData,
  CounterData,
  ErrorRatePointData,
  LatencyPointData,
  TimePointData,
  WidgetResult,
} from "../../api/types";
import BreakdownTable from "./BreakdownTable";
import CounterCard from "./CounterCard";
import ErrorRateChart from "./ErrorRateChart";
import LatencyChart from "./LatencyChart";
import ThroughputChart from "./ThroughputChart";

/**
 * WidgetRenderer dispatches by widget kind. Each kind has a strongly
 * typed component; this is the only place that downcasts the union
 * data field. If a widget's data is null (backend computation
 * failed), we render an empty-state card rather than blow up.
 */
export default function WidgetRenderer({ widget }: { widget: WidgetResult }) {
  if (widget.data == null) {
    return (
      <div className="widget widget--error">
        <div className="widget__title">{widget.name}</div>
        <div className="widget__empty">Could not load this widget.</div>
      </div>
    );
  }

  switch (widget.kind) {
    case "counter":
      return (
        <CounterCard
          data={widget.data as CounterData}
          name={widget.name}
          description={widget.description}
          tone={tone(widget.name)}
        />
      );
    case "throughput":
      return (
        <ThroughputChart
          data={widget.data as TimePointData[]}
          name={widget.name}
          description={widget.description}
        />
      );
    case "error_rate":
      return (
        <ErrorRateChart
          data={widget.data as ErrorRatePointData[]}
          name={widget.name}
          description={widget.description}
        />
      );
    case "latency":
      return (
        <LatencyChart
          data={widget.data as LatencyPointData[]}
          name={widget.name}
          description={widget.description}
        />
      );
    case "breakdown":
      return (
        <BreakdownTable
          data={widget.data as BreakdownRowData[]}
          name={widget.name}
          description={widget.description}
        />
      );
  }
}

// Light heuristic so the counter card for error counts visually
// pops red, success counts read neutral. Kept simple — names like
// "Errors", "Failed requests", "Publish errors" all flag.
function tone(name: string): "ok" | "errors" | "neutral" {
  const lower = name.toLowerCase();
  if (lower.includes("error") || lower.includes("failed") || lower.includes("failure")) {
    return "errors";
  }
  return "neutral";
}
