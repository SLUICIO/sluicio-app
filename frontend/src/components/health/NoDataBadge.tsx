// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// NoDataBadge marks a check whose last evaluation found nothing to read.
// Such a check cannot fire, and it used to sit in the list looking like a
// healthy one: a check scoped to an environment its metrics did not
// carry, or bound to a service that had been renamed, stayed "ok" for
// days while the problem it watched for went unalerted.

import type { RuleState } from "../../api/types";
import { formatRelative } from "../../lib/format";

export default function NoDataBadge({ state }: { state?: RuleState }) {
  if (state?.state !== "no_data") return null;
  const when = state.last_evaluated_at ? ` Last evaluated ${formatRelative(state.last_evaluated_at)}.` : "";
  return (
    <span
      className="m-rule-badge sev-warning"
      style={{ marginRight: 6, fontSize: 10 }}
      title={`This check finds no data in its scope, so it cannot fire. Check its metric, service and environments.${when}`}
    >
      no data
    </span>
  );
}
