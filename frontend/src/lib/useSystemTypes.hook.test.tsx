// SPDX-License-Identifier: FSL-1.1-Apache-2.0

import { renderHook, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

vi.mock("../api/client", () => ({
  api: {
    listSystemTypes: () =>
      Promise.resolve({
        system_types: [
          { key: "dotnet-service", label: ".NET service" },
          { key: "rabbitmq", label: "RabbitMQ" },
        ],
      }),
  },
}));

const { useSystemKindLabel, useSystemTypes } = await import("./useSystemTypes");

describe("useSystemKindLabel", () => {
  // The whole point: the static table has never heard of dotnet-service,
  // so a service of that type was badged with its raw key.
  it("labels a type the static table does not know", async () => {
    const { result } = renderHook(() => useSystemKindLabel());
    await waitFor(() => expect(result.current("dotnet-service")).toBe(".NET service"));
  });

  it("still answers for kinds both sources know", async () => {
    const { result } = renderHook(() => useSystemKindLabel());
    await waitFor(() => expect(result.current("rabbitmq")).toBe("RabbitMQ"));
  });

  it("says System for no kind at all", () => {
    const { result } = renderHook(() => useSystemKindLabel());
    expect(result.current(undefined)).toBe("System");
  });

  it("serves the fetched types to the picker too", async () => {
    const { result } = renderHook(() => useSystemTypes());
    await waitFor(() => expect(result.current.map((t) => t.value)).toContain("dotnet-service"));
  });
});
