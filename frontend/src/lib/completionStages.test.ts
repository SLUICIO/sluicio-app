// SPDX-License-Identifier: FSL-1.1-Apache-2.0

import { describe, expect, it } from "vitest";
import { addSpan, spanLabel, spansOf } from "./completionStages";

describe("completion stages", () => {
  it("reads a stage saved before spans existed as names from any service", () => {
    expect(spansOf({ span_names: ["Klart", "Ok"] })).toEqual([{ span_name: "Klart" }, { span_name: "Ok" }]);
  });

  it("names the service when the span has one", () => {
    expect(spanLabel({ service: "erp-loader", span_name: "Step.Send" })).toBe("erp-loader: Step.Send");
    expect(spanLabel({ span_name: "Klart" })).toBe("Klart");
  });

  it("adds a span once, keyed on service and name", () => {
    let spans = addSpan([], { service: "a", span_name: " Step.Send " });
    spans = addSpan(spans, { service: "a", span_name: "Step.Send" });
    spans = addSpan(spans, { service: "b", span_name: "Step.Send" });
    spans = addSpan(spans, { span_name: "Step.Send" });
    spans = addSpan(spans, { span_name: "  " });
    expect(spans).toEqual([
      { service: "a", span_name: "Step.Send" },
      { service: "b", span_name: "Step.Send" },
      { span_name: "Step.Send" },
    ]);
  });
});
