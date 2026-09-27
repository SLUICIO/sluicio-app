// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// The membership editor as somebody uses it: the add box, what it says
// back, the one keystroke that must never submit a form, and the setting
// that stays folded away until it matters.

import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { Rule } from "../MatcherRules";

vi.mock("../../api/client", () => ({
  api: {
    listServices: () =>
      Promise.resolve({
        services: [
          { service_name: "order-gateway", status: "ok" },
          { service_name: "order-processor", status: "ok" },
          { service_name: "billing", status: "ok" },
          { service_name: "api-gateway", status: "ok" },
          { service_name: "gateway", status: "ok" },
        ],
      }),
    messageFields: () => Promise.resolve({ fields: [] }),
    previewIntegrationRules: () => Promise.resolve({ services: [], service_count: 0 }),
    serviceNeighbors: () => Promise.resolve({ upstream: [], downstream: [] }),
  },
}));

const MembershipEditor = (await import("./MembershipEditor")).default;

const member = (service: string): Rule => ({ serviceOp: "equals", service, combine: "any", attrs: [] });

function view(props: Partial<Parameters<typeof MembershipEditor>[0]> = {}) {
  const changes: Rule[][] = [];
  const modes: string[] = [];
  render(
    <MembershipEditor
      rules={[]}
      onChange={(r) => changes.push(r)}
      combine="any"
      onCombineChange={(m) => modes.push(m)}
      windowVal="24h"
      {...props}
    />,
  );
  return { changes, modes, box: () => screen.getByRole("combobox", { name: "Add a service or pattern" }) };
}

describe("the add box", () => {
  // A quiet nightly job is exactly the service people need to add by
  // hand, so a name the cell has not seen is added, not refused.
  it("adds a service the cell has not seen", async () => {
    const { changes, box } = view();
    await userEvent.type(box(), "nightly-batch{Enter}");
    expect(changes.at(-1)).toEqual([member("nightly-batch")]);
  });

  it("adds a pattern, and says how many services it takes in now", async () => {
    const { changes, box } = view();
    await userEvent.click(box());
    await screen.findByRole("option", { name: /order-gateway/ });
    await userEvent.type(box(), "order-*");
    const option = await screen.findByRole("option", { name: /services starting with/ });
    expect(option.textContent).toContain("matches 2 now");
    await userEvent.keyboard("{Enter}");
    expect(changes.at(-1)).toEqual([{ serviceOp: "prefix", service: "order-", combine: "any", attrs: [] }]);
  });

  it("adds exactly the name typed, even when another name sorts ahead of it", async () => {
    const { changes, box } = view();
    await userEvent.click(box());
    await screen.findByRole("option", { name: /api-gateway/ });
    // "api-gateway" sorts ahead of "gateway" and contains it.
    await userEvent.type(box(), "gateway");
    await userEvent.keyboard("{Enter}");
    expect(changes.at(-1)).toEqual([member("gateway")]);
  });

  // A longer name that merely contains what was typed would be added
  // silently, traffic and all; a literal the cell has not seen at least
  // says so on its row.
  it("adds what was typed rather than a longer name that contains it", async () => {
    const { changes, box } = view();
    await userEvent.click(box());
    await screen.findByRole("option", { name: /order-gateway/ });
    await userEvent.type(box(), "order");
    await userEvent.keyboard("{Enter}");
    expect(changes.at(-1)).toEqual([member("order")]);
  });

  it("still lets a partial match be picked with the arrow keys", async () => {
    const { changes, box } = view();
    await userEvent.click(box());
    await screen.findByRole("option", { name: /order-gateway/ });
    await userEvent.type(box(), "order-g");
    await userEvent.keyboard("{ArrowDown}{Enter}");
    expect(changes.at(-1)).toEqual([member("order-gateway")]);
  });

  // Typing a member's name and getting nothing back reads as "that
  // service does not exist".
  it("says a name is already a member instead of going quiet", async () => {
    const { changes, box } = view({ rules: [member("order-gateway")] });
    await userEvent.type(box(), "order-gateway");
    expect((await screen.findByRole("option", { name: /order-gateway/ })).textContent).toContain("already a member");
    await userEvent.keyboard("{Enter}");
    expect(changes).toEqual([]);
  });

  // On the create page this box sits inside the form. Enter here means
  // "add this", never "create the integration".
  it("never submits the form around it", async () => {
    const submitted = vi.fn((e: Event) => e.preventDefault());
    const changes: Rule[][] = [];
    render(
      <form onSubmit={(e) => submitted(e.nativeEvent)}>
        <MembershipEditor rules={[]} onChange={(r) => changes.push(r)} combine="any" onCombineChange={() => {}} windowVal="24h" />
      </form>,
    );
    await userEvent.type(screen.getByRole("combobox", { name: "Add a service or pattern" }), "x{Enter}");
    expect(submitted).not.toHaveBeenCalled();
    expect(changes.at(-1)).toEqual([member("x")]);
  });
});

describe("how members combine", () => {
  it("is folded away while it says the usual thing", async () => {
    const { modes } = view({ rules: [member("a")] });
    expect(screen.queryByRole("radiogroup", { name: "How the members combine" })).toBeNull();
    await userEvent.click(screen.getByRole("button", { name: /Advanced matching/ }));
    await userEvent.click(screen.getByRole("radio", { name: /One trace through every member/ }));
    expect(modes).toEqual(["all"]);
  });

  // A setting that changes what the whole integration means is not one
  // to hide.
  it("is never folded away while it says the unusual thing", () => {
    view({ rules: [member("a")], combine: "all" });
    expect(screen.getByRole("radiogroup", { name: "How the members combine" })).toBeTruthy();
    expect(screen.getByText(/only when one trace passed through every member/)).toBeTruthy();
  });
});

describe("read-only", () => {
  it("shows the members with nothing to act on", () => {
    view({ rules: [member("order-gateway")], readOnly: true });
    expect(screen.getByText("order-gateway")).toBeTruthy();
    expect(screen.queryByRole("combobox")).toBeNull();
    expect(screen.queryByRole("button", { name: /Remove|Edit/ })).toBeNull();
    expect(screen.queryByRole("button", { name: /Advanced matching/ })).toBeNull();
  });
});

describe('"or" where members must all appear in one trace', () => {
  const orMember: Rule = {
    serviceOp: "equals",
    service: "order-gateway",
    combine: "any",
    attrs: [
      { attribute: "x", operator: "equals", value: "1" },
      { attribute: "x", operator: "equals", value: "2" },
    ],
  };

  it("refuses to switch into that mode, and names the member in the way", async () => {
    const { modes } = view({ rules: [orMember] });
    await userEvent.click(screen.getByRole("button", { name: /Advanced matching/ }));
    const strict = screen.getByRole("radio", { name: /One trace through every member/ }) as HTMLInputElement;
    expect(strict.disabled).toBe(true);
    expect(screen.getByText(/Not available while order-gateway joins its conditions/)).toBeTruthy();
    await userEvent.click(strict);
    expect(modes).toEqual([]);
  });

  // Stored before the guard existed: kept, but said.
  it("flags a member already stored that way", async () => {
    view({ rules: [orMember], combine: "all" });
    expect(await screen.findByText(/which this mode reads as every one of them being required/)).toBeTruthy();
  });
});
