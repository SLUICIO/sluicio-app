// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// The sentence under the pills says what the query does. A row that is
// still waiting for a value does not restrict anything - the search
// engine skips it - so it must not appear there claiming to.

import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import FilterEditor, { type Filter } from "./FilterEditor";

const row = (patch: Partial<Filter>): Filter => ({
  id: patch.id ?? "r",
  field: "payload",
  fieldPath: "edi.message_type",
  op: "equals",
  value: "",
  removable: true,
  ...patch,
});

describe("the filter summary", () => {
  it("leaves out a row with no value yet", () => {
    render(<FilterEditor filters={[row({})]} onChange={() => {}} />);
    expect(screen.getByText(/showing all messages in the selected time range/i)).toBeTruthy();
  });

  it("names a row once it has one", () => {
    render(<FilterEditor filters={[row({ value: "ORDERS" })]} onChange={() => {}} />);
    // Twice: once in the value pill, once in the sentence.
    expect(screen.getAllByText("ORDERS").length).toBeGreaterThan(1);
  });

  it("keeps a row whose operator takes no value", () => {
    render(<FilterEditor filters={[row({ id: "x", op: "exists" })]} onChange={() => {}} />);
    expect(screen.queryByText(/showing all messages in the selected time range/i)).toBeNull();
  });
});
