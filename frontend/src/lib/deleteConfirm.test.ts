// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// The failure this guards against is a confirmation that undersells the
// click: deleting an integration also deletes its health checks, and a
// dialog that does not say so (or says so only when the count loaded)
// lets someone remove alerting they did not mean to.

import { describe, expect, it } from "vitest";
import { boundChecksSentence, integrationDeleteConfirm, systemDeleteConfirm } from "./deleteConfirm";

describe("boundChecksSentence", () => {
  it("counts the checks that go", () => {
    expect(boundChecksSentence(1)).toBe("Its 1 health check is deleted with it.");
    expect(boundChecksSentence(3)).toBe("Its 3 health checks are deleted with it.");
  });

  it("says nothing when no checks are bound", () => {
    expect(boundChecksSentence(0)).toBe("");
  });

  it("still warns when the count could not be loaded", () => {
    expect(boundChecksSentence(null)).toMatch(/deleted with it/);
  });
});

describe("delete confirmations", () => {
  it("names the integration and its checks", () => {
    expect(integrationDeleteConfirm("orders-queue", 2)).toBe(
      'Delete integration "orders-queue"? Its matchers are removed. Its 2 health checks are deleted with it.',
    );
    expect(integrationDeleteConfirm("orders-queue", 0)).toBe(
      'Delete integration "orders-queue"? Its matchers are removed.',
    );
  });

  it("separates the system's own checks from its members' checks", () => {
    expect(systemDeleteConfirm("broker-1", 1)).toBe(
      'Delete system "broker-1"? Its services are detached and keep their own health checks. Its 1 health check is deleted with it.',
    );
  });
});
