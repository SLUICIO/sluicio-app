// SPDX-License-Identifier: FSL-1.1-Apache-2.0
import { describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";

const state = vi.hoisted(() => ({
  orgWrite: false,
  editorGroups: [] as { id: string; slug: string; name: string }[],
}));
vi.mock("../../lib/useCurrentUser", () => ({ useCurrentUser: () => ({ can: () => state.orgWrite }) }));
vi.mock("../../lib/useAccess", () => ({ useAccess: () => ({ editorGroups: state.editorGroups }) }));

const { default: SaveAsViewDialog } = await import("./SaveAsViewDialog");

function open(onSubmit = vi.fn()) {
  render(<SaveAsViewDialog open filters={[]} suggestedName="orders" onClose={() => {}} onSubmit={onSubmit} />);
  return onSubmit;
}

describe("the save-as-view dialog", () => {
  it("lets a viewer save a private view and nothing wider", () => {
    state.orgWrite = false;
    state.editorGroups = [];
    const onSubmit = open();
    expect(screen.getByRole("button", { name: "Team" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Org-wide" })).toBeDisabled();
    fireEvent.click(screen.getByRole("button", { name: /save view/ }));
    expect(onSubmit).toHaveBeenCalledWith(expect.objectContaining({ visibility: "private", groupId: undefined }));
  });

  it("offers the teams the caller edits and sends the one picked", () => {
    state.orgWrite = false;
    state.editorGroups = [
      { id: "t1", slug: "orders", name: "Orders" },
      { id: "t2", slug: "payments", name: "Payments" },
    ];
    const onSubmit = open();
    expect(screen.getByRole("button", { name: "Org-wide" })).toBeDisabled();
    fireEvent.click(screen.getByRole("button", { name: "Team" }));
    fireEvent.change(screen.getByLabelText("Team"), { target: { value: "t2" } });
    expect(screen.getByText("Members of Payments can see and load this view.")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: /save view/ }));
    expect(onSubmit).toHaveBeenCalledWith(expect.objectContaining({ visibility: "team", groupId: "t2" }));
  });
});
