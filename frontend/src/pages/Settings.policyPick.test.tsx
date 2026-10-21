// SPDX-License-Identifier: FSL-1.1-Apache-2.0

import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, it, vi } from "vitest";

const created = vi.hoisted(() => [] as Array<Record<string, unknown>>);
vi.mock("../lib/useSystemTypes", () => ({ useSystemTypes: () => [] }));
vi.mock("../lib/useProductName", () => ({ useProductName: () => "Sluicio" }));
vi.mock("../api/client", () => ({
  api: {
    listIntegrations: () =>
      Promise.resolve({
        integrations: [
          { id: "id-inv", name: "Invoice dispatch" },
          { id: "id-ord", name: "Order processing" },
          { id: "id-shp", name: "Shipment events" },
        ],
      }),
    createGroupPolicy: (_group: string, body: Record<string, unknown>) => {
      created.push(body);
      return Promise.resolve({});
    },
    updateGroupPolicy: () => Promise.resolve({}),
    listSystems: () => Promise.resolve({ systems: [] }),
  },
}));

const { CreatePolicyForm } = await import("./Settings");

describe("granting two of three integrations", () => {
  // Picked by name, stored by id: one policy per integration, so a
  // rename changes nothing and deleting one integration removes only its
  // grant.
  it("creates one id-based policy per picked integration", async () => {
    const onCreated = vi.fn();
    render(
      <MemoryRouter>
        <CreatePolicyForm groupId="g1" onClose={() => {}} onCreated={onCreated} />
      </MemoryRouter>,
    );
    await userEvent.selectOptions(screen.getByRole("combobox"), "integration");
    await userEvent.click(await screen.findByRole("button", { name: /Pick integrations/ }));
    await userEvent.click(screen.getByRole("option", { name: "Invoice dispatch" }));
    await userEvent.click(screen.getByRole("button", { name: /Add another/ }));
    await userEvent.click(screen.getByRole("option", { name: "Shipment events" }));
    await userEvent.click(screen.getByRole("button", { name: "Add policy" }));

    await waitFor(() => expect(onCreated).toHaveBeenCalled());
    expect(created.map((b) => b.target_integration_id)).toEqual(["id-inv", "id-shp"]);
    expect(created.every((b) => b.kind === "integration" && !("conditions" in b))).toBe(true);
  });
});
