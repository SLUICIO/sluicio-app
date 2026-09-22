// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// An optional row is drawn muted. It was muted by putting opacity on the
// row, and opacity applies to the whole subtree with no way back out, so
// the picker the row opens inherited it: the popover went see-through and
// the message table underneath read straight through its text.
//
// The mute belongs on the pills. These assertions are about WHERE the
// fading is, because that is the part that was wrong, not how it looks.

import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import FilterEditor, { type Filter } from "./FilterEditor";

const optionalRow: Filter[] = [
  {
    id: "seed-edi.message_type",
    field: "payload",
    fieldPath: "edi.message_type",
    op: "equals",
    value: "",
    removable: true,
    optional: true,
  },
];

function opacityOf(el: Element | null): number {
  let node: Element | null = el;
  let acc = 1;
  while (node) {
    const raw = (node as HTMLElement).style?.opacity;
    if (raw) acc *= Number(raw);
    node = node.parentElement;
  }
  return acc;
}

describe("an optional filter row", () => {
  it("opens a picker that is not faded by the row", async () => {
    render(<FilterEditor filters={optionalRow} onChange={() => {}} />);
    const pill = screen.getAllByRole("button")[0];
    expect(opacityOf(pill), "the pill itself should read as muted").toBeLessThan(1);

    await userEvent.click(pill);
    const picker = await screen.findByText(/pick a field/i);
    expect(
      opacityOf(picker),
      "the popover inherited the row's fade and became see-through",
    ).toBe(1);
  });
});
