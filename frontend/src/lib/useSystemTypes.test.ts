// SPDX-License-Identifier: FSL-1.1-Apache-2.0

import { describe, expect, it } from "vitest";
import { SYSTEM_KINDS, systemKindLabel } from "./systemKinds";

// The static table is the FALLBACK now, not the source of truth. These
// pin the two facts that made it wrong, so a future edit to the table
// cannot quietly re-introduce either.
describe("the static system-kind table", () => {
  it("still labels the kinds it knows, for the offline fallback", () => {
    expect(systemKindLabel("rabbitmq")).toBe("RabbitMQ");
    expect(systemKindLabel(undefined)).toBe("System");
  });

  // The reason useSystemKindLabel exists: a real type the table has never
  // heard of came out as its raw key.
  it("returns the raw key for a type it has not heard of", () => {
    expect(SYSTEM_KINDS.some((k) => k.value === "dotnet-service")).toBe(false);
    expect(systemKindLabel("dotnet-service")).toBe("dotnet-service");
  });
});
