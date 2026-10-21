// SPDX-License-Identifier: FSL-1.1-Apache-2.0

import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { describe, expect, it } from "vitest";
import { ListValueInput } from "./Settings";

// The editor owns the list, as the policy form does.
function Harness({ options, seen }: { options?: string[]; seen: string[][] }) {
  const [values, setValues] = useState<string[]>([]);
  return (
    <ListValueInput
      values={values}
      options={options}
      onChange={(next) => {
        seen.push(next);
        setValues(next);
      }}
    />
  );
}

describe("an \"is one of\" list in an access policy", () => {
  // The field used to redraw from the parsed list on every keystroke, so
  // a typed comma (or a trailing space) vanished at once and a second
  // value could never be started.
  it("takes several values typed one key at a time, spaces included", async () => {
    const seen: string[][] = [];
    render(<Harness seen={seen} />);
    const input = screen.getByPlaceholderText("a, b, c");
    await userEvent.type(input, "Invoice dispatch, Order processing");
    expect((input as HTMLInputElement).value).toBe("Invoice dispatch, Order processing");
    expect(seen.at(-1)).toEqual(["Invoice dispatch", "Order processing"]);
  });

  it("offers existing names to pick when there are some", async () => {
    const seen: string[][] = [];
    render(<Harness seen={seen} options={["Invoice dispatch", "Order processing", "Shipment events"]} />);
    await userEvent.click(screen.getByRole("button", { name: /Pick/ }));
    await userEvent.click(screen.getByRole("option", { name: "Invoice dispatch" }));
    await userEvent.click(screen.getByRole("button", { name: /Add another/ }));
    await userEvent.click(screen.getByRole("option", { name: "Shipment events" }));
    expect(seen.at(-1)).toEqual(["Invoice dispatch", "Shipment events"]);
    // A picked name is no longer offered, and can be taken back out.
    await userEvent.click(screen.getByRole("button", { name: /Add another/ }));
    expect(screen.queryByRole("option", { name: "Invoice dispatch" })).toBeNull();
    await userEvent.keyboard("{Escape}");
    await userEvent.click(screen.getByRole("button", { name: "Remove Invoice dispatch" }));
    expect(seen.at(-1)).toEqual(["Shipment events"]);
  });
});
