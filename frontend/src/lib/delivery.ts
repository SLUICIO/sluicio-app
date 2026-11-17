// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Where a check's firings go, as the rule lists show it. The lists used
// to show only a rule's own channels, so a check routed by its
// notification profile, which is most of them, looked as if it went
// nowhere.

import type { RuleDelivery } from "../api/types";

// deliveryLabel is the one-line answer: "incident.io (own)", "Profile
// “Ops”: incident.io, Slack", or that nothing will be sent.
export function deliveryLabel(d: RuleDelivery | undefined, channelName: (id: string) => string): string {
  if (!d || d.via === "unknown") return "—";
  const names = d.channel_ids.map(channelName).join(", ");
  if (d.via === "channels") return `${names} (own)`;
  if (d.via === "profile") {
    const scope = d.restricted ? " (some environments)" : "";
    return names ? `Profile “${d.profile_name}”${scope}: ${names}` : `Profile “${d.profile_name}”${scope}: no channels`;
  }
  return "Nowhere: no channels and no profile";
}

// deliveredChannelIds is every channel some rule's firings reach.
export function deliveredChannelIds(deliveries: Record<string, RuleDelivery> | undefined, ruleIds: string[]): Set<string> {
  const out = new Set<string>();
  for (const id of ruleIds) for (const c of deliveries?.[id]?.channel_ids ?? []) out.add(c);
  return out;
}
