// SPDX-License-Identifier: FSL-1.1-Apache-2.0
import { levelBadgeLabel, severityBand } from "../../lib/severity";

// The 5-variant level badge (DBUG/INFO/WARN/ERR/FATAL) from the design.
// Used in log rows and the drawer header.
export default function LevelBadge({ num }: { num: number }) {
  const band = severityBand(num);
  const variant = band === "debug" ? "" : `lvl--${band}`;
  return <span className={`lvl ${variant}`}>{levelBadgeLabel(num)}</span>;
}
