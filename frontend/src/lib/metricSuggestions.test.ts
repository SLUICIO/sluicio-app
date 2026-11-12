// SPDX-License-Identifier: FSL-1.1-Apache-2.0

import { describe, expect, it } from "vitest";
import { GROUP_INTEGRATION, GROUP_OTHER, GROUP_SUPPORTING, globMatch, orderMetricOptions, sourceMetrics } from "./metricSuggestions";

describe("metric check suggestions", () => {
  it("offers supporting telemetry first, then the integration's, then the rest, each once", () => {
    const { options, groupOf } = orderMetricOptions(
      ["rabbitmq.queue.messages"],
      ["http.server.duration", "rabbitmq.queue.messages"],
      ["zz.other", "http.server.duration", "rabbitmq.queue.messages", "a.other"],
    );
    expect(options).toEqual(["rabbitmq.queue.messages", "http.server.duration", "a.other", "zz.other"]);
    expect(groupOf("rabbitmq.queue.messages")).toBe(GROUP_SUPPORTING);
    expect(groupOf("http.server.duration")).toBe(GROUP_INTEGRATION);
    expect(groupOf("a.other")).toBe(GROUP_OTHER);
  });

  it("draws no heading when there is only one list", () => {
    const { groupOf } = orderMetricOptions([], [], ["a"]);
    expect(groupOf("a")).toBeUndefined();
  });

  it("narrows a source to its globs", () => {
    expect(globMatch("rabbitmq.queue.*", "rabbitmq.queue.messages")).toBe(true);
    expect(globMatch("rabbitmq.queue.*", "rabbitmqXqueue.messages")).toBe(false);
    expect(sourceMetrics(["rabbitmq.queue.messages", "rabbitmq.node.mem"], ["rabbitmq.queue.*"])).toEqual(["rabbitmq.queue.messages"]);
    expect(sourceMetrics(["a", "b"], [])).toEqual(["a", "b"]);
  });
});
