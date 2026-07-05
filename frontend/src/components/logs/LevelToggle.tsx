// SPDX-License-Identifier: FSL-1.1-Apache-2.0
import { LEVEL_SEGMENTS } from "../../lib/severity";

// Six-segment severity threshold control (All·Debug·Info·Warn·Error·
// Fatal). Single-pick threshold — "Error" means error AND fatal. value
// is the OTLP severity floor (0 = All); onChange emits the new floor.
export default function LevelToggle({
  value,
  onChange,
}: {
  value: number;
  onChange: (min: number) => void;
}) {
  return (
    <div className="level-seg" role="radiogroup" aria-label="Severity threshold">
      {LEVEL_SEGMENTS.map((s) => {
        const active = value === s.min;
        const tint =
          active && s.band === "fatal"
            ? "is-fatal"
            : active && s.band === "err"
              ? "is-err"
              : active && s.band === "warn"
                ? "is-warn"
                : "";
        return (
          <button
            key={s.label}
            type="button"
            role="radio"
            aria-checked={active}
            className={`level-seg__btn ${tint}`}
            onClick={() => onChange(s.min)}
          >
            {s.label}
          </button>
        );
      })}
    </div>
  );
}
