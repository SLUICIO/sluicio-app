// SPDX-License-Identifier: FSL-1.1-Apache-2.0
import { describe, expect, it } from "vitest";
import { deliveredChannelIds, deliveryLabel } from "./delivery";

const name = (id: string) => ({ a: "incident.io", b: "Slack" })[id] ?? id;

describe("deliveryLabel", () => {
  it("names the rule's own channels", () => {
    expect(deliveryLabel({ via: "channels", channel_ids: ["a"] }, name)).toBe("incident.io (own)");
  });
  it("names the profile a check without channels follows", () => {
    expect(deliveryLabel({ via: "profile", profile_name: "Ops", channel_ids: ["a", "b"] }, name)).toBe(
      "Profile “Ops”: incident.io, Slack",
    );
    expect(deliveryLabel({ via: "profile", profile_name: "Prod", restricted: true, channel_ids: [] }, name)).toBe(
      "Profile “Prod” (some environments): no channels",
    );
  });
  it("says when nothing will be sent", () => {
    expect(deliveryLabel({ via: "none", channel_ids: [] }, name)).toBe("Nowhere: no channels and no profile");
    expect(deliveryLabel(undefined, name)).toBe("—");
  });
});

describe("deliveredChannelIds", () => {
  it("unions what the listed rules reach", () => {
    const d = {
      r1: { via: "channels" as const, channel_ids: ["a"] },
      r2: { via: "profile" as const, profile_name: "Ops", channel_ids: ["a", "b"] },
      r3: { via: "none" as const, channel_ids: [] },
    };
    expect([...deliveredChannelIds(d, ["r1", "r2", "r3"])].sort()).toEqual(["a", "b"]);
    expect([...deliveredChannelIds(d, ["r3"])]).toEqual([]);
  });
});
