// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// The order a metric check offers metrics in, for a check on an
// integration: the integration's supporting telemetry first (its metric
// sources, issue #43), then the metrics of its own services, then every
// other metric. Someone checking a broker's queue depth for "Order sync"
// finds rabbitmq.queue.messages at the top instead of somewhere among
// every metric the organization sends.

export const GROUP_SUPPORTING = "Supporting telemetry";
export const GROUP_INTEGRATION = "This integration";
export const GROUP_OTHER = "Other metrics";

/** Whether name matches a source's glob: * is any run, ? one character. */
export function globMatch(glob: string, name: string): boolean {
  const re = new RegExp(
    "^" +
      glob
        .split("")
        .map((c) => (c === "*" ? ".*" : c === "?" ? "." : c.replace(/[.+^${}()|[\]\\]/g, "\\$&")))
        .join("") +
      "$",
  );
  return re.test(name);
}

/** A metric source's names: what its origin sent, narrowed by its globs. */
export function sourceMetrics(sent: string[], globs: string[]): string[] {
  return globs.length === 0 ? sent : sent.filter((n) => globs.some((g) => globMatch(g, n)));
}

/**
 * The options in order, and the group each belongs to. A name appears
 * once, in the first group that has it.
 */
export function orderMetricOptions(
  supporting: string[],
  integration: string[],
  all: string[],
): { options: string[]; groupOf: (name: string) => string | undefined } {
  const group = new Map<string, string>();
  const options: string[] = [];
  const add = (names: string[], g: string) => {
    for (const n of [...names].sort()) {
      if (!group.has(n)) {
        group.set(n, g);
        options.push(n);
      }
    }
  };
  add(supporting, GROUP_SUPPORTING);
  add(integration, GROUP_INTEGRATION);
  // Without anything above, a heading over the whole list says nothing.
  add(all, options.length > 0 ? GROUP_OTHER : "");
  return { options, groupOf: (n) => group.get(n) || undefined };
}
