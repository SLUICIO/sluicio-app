// SPDX-License-Identifier: FSL-1.1-Apache-2.0

import { afterEach, describe, expect, it } from "vitest";
import { act, renderHook } from "@testing-library/react";
import { useResolvedTheme } from "./useResolvedTheme";

afterEach(() => document.documentElement.removeAttribute("data-theme"));

describe("useResolvedTheme", () => {
  it("reads the attribute that is already set", () => {
    document.documentElement.setAttribute("data-theme", "dark");
    const { result } = renderHook(() => useResolvedTheme());
    expect(result.current).toBe("dark");
  });

  it("defaults to light when nothing is set", () => {
    const { result } = renderHook(() => useResolvedTheme());
    expect(result.current).toBe("light");
  });

  // The toggle flips the attribute on a page that is already rendered,
  // so an editor mounted in light mode has to follow it into dark.
  it("follows a later change to the attribute", async () => {
    const { result } = renderHook(() => useResolvedTheme());
    expect(result.current).toBe("light");
    await act(async () => {
      document.documentElement.setAttribute("data-theme", "dark");
      // MutationObserver callbacks run as a microtask.
      await Promise.resolve();
    });
    expect(result.current).toBe("dark");
  });

  it("treats any other value as light", () => {
    document.documentElement.setAttribute("data-theme", "auto");
    const { result } = renderHook(() => useResolvedTheme());
    expect(result.current).toBe("light");
  });
});
