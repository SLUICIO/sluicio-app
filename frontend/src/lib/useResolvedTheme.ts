// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// The theme that is actually in effect, as opposed to the preference.
//
// useTheme owns the preference - which includes "auto" - and writing to
// it applies and persists a choice. Components that only need to KNOW
// whether the page is currently dark must not call it: reading would
// re-apply and re-persist, and "auto" is not an answer they can use.
//
// Reads `data-theme` off <html>, which is where every writer puts the
// resolved value: the inline script in index.html, useTheme's effect,
// and its OS-preference listener. Observing the attribute rather than
// the preference means this stays correct no matter which one moved it.

import { useEffect, useState } from "react";

export type ResolvedTheme = "light" | "dark";

function read(): ResolvedTheme {
  if (typeof document === "undefined") return "light";
  return document.documentElement.getAttribute("data-theme") === "dark" ? "dark" : "light";
}

/** "light" or "dark" - never "auto" - kept in step with <html data-theme>. */
export function useResolvedTheme(): ResolvedTheme {
  const [theme, setTheme] = useState<ResolvedTheme>(read);

  useEffect(() => {
    // The attribute can be set before this effect runs (the inline
    // script beats React to it), so take a reading rather than trusting
    // the one useState captured.
    setTheme(read());
    const obs = new MutationObserver(() => setTheme(read()));
    obs.observe(document.documentElement, { attributes: true, attributeFilter: ["data-theme"] });
    return () => obs.disconnect();
  }, []);

  return theme;
}
