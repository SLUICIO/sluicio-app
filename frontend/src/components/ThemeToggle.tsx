// SPDX-License-Identifier: FSL-1.1-Apache-2.0
import { Theme, useTheme } from "../lib/useTheme";

const order: Theme[] = ["light", "dark", "auto"];
const labels: Record<Theme, string> = {
  light: "Light",
  dark: "Dark",
  auto: "Auto",
};

export default function ThemeToggle() {
  const [theme, setTheme] = useTheme();
  const cycle = () => {
    const i = order.indexOf(theme);
    setTheme(order[(i + 1) % order.length]);
  };
  return (
    <button
      type="button"
      className="theme-toggle"
      onClick={cycle}
      title={`Theme: ${labels[theme]}. Click to cycle.`}
      aria-label={`Switch theme. Currently ${labels[theme]}.`}
    >
      <ThemeIcon theme={theme} />
      <span>{labels[theme]}</span>
    </button>
  );
}

function ThemeIcon({ theme }: { theme: Theme }) {
  if (theme === "light") {
    return (
      <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5" aria-hidden>
        <circle cx="8" cy="8" r="3" />
        <path
          d="M8 1v2M8 13v2M1 8h2M13 8h2M3.5 3.5l1.4 1.4M11.1 11.1l1.4 1.4M3.5 12.5l1.4-1.4M11.1 4.9l1.4-1.4"
          strokeLinecap="round"
        />
      </svg>
    );
  }
  if (theme === "dark") {
    return (
      <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5" aria-hidden>
        <path d="M13 9.5A6 6 0 116.5 3a5 5 0 006.5 6.5z" />
      </svg>
    );
  }
  // auto: half-filled circle
  return (
    <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5" aria-hidden>
      <circle cx="8" cy="8" r="6" />
      <path d="M8 2a6 6 0 010 12V2z" fill="currentColor" stroke="none" />
    </svg>
  );
}
