// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// useTheme is the app-wide hook for switching between light and
// dark, with "auto" as an optional follow-the-OS mode. Persists the
// user's choice to localStorage. The actual initial application of
// the theme happens in an inline script in index.html so there's no
// light-then-dark flash on load.
//
// Theme is communicated via `data-theme="light" | "dark"` on <html>
// per the Sluicio color-system handoff. The .dark class is also
// toggled so Tailwind's darkMode: 'class' continues to resolve.
//
// Default: light. The handoff's demo HTML opens with
// data-theme="light" and the README only "optionally" respects
// prefers-color-scheme — users opt in to dark via the toggle.

import { useEffect, useState } from "react";

export type Theme = "light" | "dark" | "auto";

const STORAGE_KEY = "im.theme";
const DEFAULT_THEME: Theme = "light";

function readSystemPref(): "light" | "dark" {
  if (
    typeof window !== "undefined" &&
    window.matchMedia &&
    window.matchMedia("(prefers-color-scheme: dark)").matches
  ) {
    return "dark";
  }
  return "light";
}

function applyTheme(theme: Theme) {
  const resolved = theme === "auto" ? readSystemPref() : theme;
  document.documentElement.setAttribute("data-theme", resolved);
  document.documentElement.classList.toggle("dark", resolved === "dark");
}

function readStored(): Theme {
  try {
    const v = localStorage.getItem(STORAGE_KEY);
    if (v === "light" || v === "dark" || v === "auto") return v;
  } catch {
    /* private mode — fall through */
  }
  return DEFAULT_THEME;
}

/**
 * useTheme returns the user's preferred theme and a setter. Setting a
 * value applies it immediately (toggling the .dark class on <html>)
 * and persists the choice. When the value is "auto" the hook also
 * listens for OS-level theme changes and re-applies.
 */
export function useTheme(): [Theme, (next: Theme) => void] {
  const [theme, setThemeState] = useState<Theme>(() => readStored());

  useEffect(() => {
    applyTheme(theme);
    try {
      localStorage.setItem(STORAGE_KEY, theme);
    } catch {
      /* ignore */
    }
  }, [theme]);

  // When in auto mode, re-apply if the OS preference changes mid-session.
  useEffect(() => {
    if (theme !== "auto") return;
    if (!window.matchMedia) return;
    const mq = window.matchMedia("(prefers-color-scheme: dark)");
    const handler = () => applyTheme("auto");
    mq.addEventListener("change", handler);
    return () => mq.removeEventListener("change", handler);
  }, [theme]);

  return [theme, setThemeState];
}
