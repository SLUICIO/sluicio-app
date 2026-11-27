// SPDX-License-Identifier: FSL-1.1-Apache-2.0
import { describe, expect, it } from "vitest";
import type { Collector } from "../api/types";
import { collectorName, collectorStatus } from "./Collectors";

const base: Collector = {
  id: "c1",
  group_id: "g1",
  group_name: "Platform",
  instance_uid: "01a21d74-30d8-7874-a84a-dace2f4fcfba",
  kind: "extension",
  capabilities: [],
  description: {},
  healthy: null,
  online: true,
  transport: "websocket",
  remote_addr: "",
  first_seen_at: "2026-11-27T10:00:00Z",
  last_seen_at: "2026-11-27T10:00:00Z",
};

describe("collectorName", () => {
  it("uses the host name the agent reports", () => {
    expect(
      collectorName({ ...base, description: { "host.name": "gw-01" } }),
    ).toBe("gw-01");
  });
  it("falls back to the instance UID", () => {
    expect(collectorName(base)).toBe(base.instance_uid);
  });
});

describe("collectorStatus", () => {
  it("says offline before anything about health: a stale report is not current health", () => {
    expect(
      collectorStatus({ ...base, online: false, healthy: true }).label,
    ).toBe("Offline");
  });
  it("reads an online collector's own health", () => {
    expect(collectorStatus({ ...base, healthy: false })).toEqual({
      kind: "err",
      label: "Unhealthy",
    });
    expect(collectorStatus({ ...base, healthy: true })).toEqual({
      kind: "ok",
      label: "Healthy",
    });
  });
  it("is just online when no health was reported", () => {
    expect(collectorStatus(base)).toEqual({ kind: "ok", label: "Online" });
  });
});
