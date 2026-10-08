// SPDX-License-Identifier: FSL-1.1-Apache-2.0

import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, it, vi } from "vitest";

vi.mock("../lib/useCurrentUser", () => ({
  useCurrentUser: () => ({ can: () => true, user: { id: "me" } }),
}));

// The server's copy of the member's role: what a list refresh returns.
const server = vi.hoisted(() => ({ role: "editor" }));
vi.mock("../api/client", () => ({
  api: {
    listMembers: () =>
      Promise.resolve({
        members: [
          {
            user: { id: "u1", name: "Sam Lee", email: "sam@example.com" },
            role: server.role,
            joined_at: "2026-01-01T00:00:00Z",
            has_password: true,
            sso_providers: [],
          },
        ],
      }),
    updateMemberRole: (_id: string, role: string) => {
      server.role = role;
      return Promise.resolve();
    },
  },
}));

const { MembersTab } = await import("./Settings");

describe("changing a member's role", () => {
  // The blade held the row it was opened with, so the saved role was
  // replaced on screen by the old one until the page was reloaded.
  it("shows the saved role without a reload", async () => {
    render(
      <MemoryRouter>
        <MembersTab />
      </MemoryRouter>,
    );
    fireEvent.click(await screen.findByText("sam@example.com"));
    const blade = await screen.findByRole("dialog");
    const select = within(blade).getByRole("combobox") as HTMLSelectElement;
    expect(select.value).toBe("editor");

    fireEvent.change(select, { target: { value: "admin" } });

    await waitFor(() => expect((within(blade).getByRole("combobox") as HTMLSelectElement).value).toBe("admin"));
    const row = screen.getAllByText("sam@example.com").map((e) => e.closest("tr")).find(Boolean)!;
    expect(within(row).getByText("admin")).toBeTruthy();
  });
});
