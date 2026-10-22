// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// #42: an exception a span recorded as an event (.NET's
// Activity.AddException and its equivalents) is shown, leads the panel,
// and keeps its stack trace one click away.

import { describe, it, expect } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import SpanEvents, { exceptionOf } from "./SpanEvents";
import type { SpanSummary } from "../api/types";

const base: SpanSummary = {
  timestamp: "2026-10-22T10:00:00.000Z",
  trace_id: "t1",
  span_id: "s1",
  service_name: "checkout-api",
  span_name: "charge",
  span_kind: "Server",
  status_code: "Error",
  duration_ms: 12,
};

const withException: SpanSummary = {
  ...base,
  events: [
    { timestamp: "2026-10-22T10:00:00.004Z", name: "retry", attributes: { attempt: "2" } },
    {
      timestamp: "2026-10-22T10:00:00.010Z",
      name: "exception",
      attributes: {
        "exception.type": "System.InvalidOperationException",
        "exception.message": "Payment provider returned 503",
        "exception.stacktrace": "System.InvalidOperationException: Payment provider returned 503\n   at Checkout.PaymentClient.ChargeAsync()",
      },
    },
  ],
  events_total: 5,
};

describe("exceptionOf", () => {
  it("reads the exception event first", () => {
    expect(exceptionOf(withException)).toMatchObject({
      type: "System.InvalidOperationException",
      message: "Payment provider returned 503",
    });
  });

  it("falls back to exception.* attributes", () => {
    const s = { ...base, attributes: { "exception.type": "TimeoutException", "exception.message": "timed out" } };
    expect(exceptionOf(s)).toEqual({ type: "TimeoutException", message: "timed out", stacktrace: undefined });
  });

  it("is null for a span that only set a status", () => {
    expect(exceptionOf({ ...base, status_message: "boom" })).toBeNull();
  });

});

describe("SpanEvents", () => {
  it("shows the exception, the other events and how many were dropped", () => {
    const { container } = render(<SpanEvents span={withException} />);
    expect(screen.getByText("System.InvalidOperationException")).toBeInTheDocument();
    expect(screen.getByText("Payment provider returned 503")).toBeInTheDocument();
    expect(screen.getByText("retry")).toBeInTheDocument();
    expect(container.textContent).toContain("showing 2 of 5");
    expect(container.textContent).toContain("+10 ms");
  });

  it("keeps the stack trace one click away", () => {
    const { container } = render(<SpanEvents span={withException} />);
    expect(container.textContent).not.toContain("ChargeAsync");
    fireEvent.click(screen.getByRole("button", { name: /Stack trace/ }));
    expect(container.textContent).toContain("at Checkout.PaymentClient.ChargeAsync()");
  });

  it("renders nothing for a span without events", () => {
    const { container } = render(<SpanEvents span={base} />);
    expect(container.firstChild).toBeNull();
  });
});
