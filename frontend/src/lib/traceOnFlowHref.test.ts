// SPDX-License-Identifier: FSL-1.1-Apache-2.0

import { describe, expect, it } from "vitest";
import { traceOnFlowHref } from "./traceOnFlowHref";

describe("traceOnFlowHref", () => {
  it("builds the flow link with the trace as a query param", () => {
    expect(traceOnFlowHref("abc-123", "517c5dec")).toBe(
      "/integrations/abc-123?trace=517c5dec",
    );
  });

  it("returns null without an integration", () => {
    // The projection is a position on ONE integration's graph. With no
    // integration there is nothing to project onto, so the caller must
    // render no link rather than one that lands nowhere useful.
    expect(traceOnFlowHref(undefined, "517c5dec")).toBeNull();
    expect(traceOnFlowHref("", "517c5dec")).toBeNull();
    expect(traceOnFlowHref("   ", "517c5dec")).toBeNull();
  });

  it("returns null without a trace", () => {
    expect(traceOnFlowHref("abc-123", undefined)).toBeNull();
    expect(traceOnFlowHref("abc-123", "")).toBeNull();
  });

  it("encodes both ids", () => {
    // Guards the failure this function exists to prevent: a link that
    // loads the page but projects nothing, which reads as the feature
    // being broken rather than as a bad link.
    expect(traceOnFlowHref("a/b", "x&y=z")).toBe("/integrations/a%2Fb?trace=x%26y%3Dz");
  });

  it("trims surrounding whitespace rather than encoding it", () => {
    expect(traceOnFlowHref(" abc ", " 517c ")).toBe("/integrations/abc?trace=517c");
  });
});
