// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Breadcrumb leaf context. The section crumb (Integrations, Services, …)
// is derived from the URL in AppShell, but the trailing entity name often
// isn't in the URL — e.g. an integration's route carries its id, not its
// name. A detail page sets the leaf via useBreadcrumbLeaf(name) while
// mounted; AppShell's Breadcrumb reads it. Cleared on unmount so a name
// never bleeds into the next page.
//
// Pages that are reached from several places (e.g. the full trace view,
// reachable from Logs, Messages, an integration…) can go further and set
// the WHOLE trail via useBreadcrumbTrail, so the breadcrumb reflects
// where the user actually came from and links back there.

import {
  createContext,
  useContext,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from "react";

export interface Crumb {
  label: string;
  to?: string;
}

interface BreadcrumbCtx {
  leaf: string | null;
  setLeaf: (v: string | null) => void;
  trail: Crumb[] | null;
  setTrail: (v: Crumb[] | null) => void;
}

const Ctx = createContext<BreadcrumbCtx | null>(null);

export function BreadcrumbProvider({ children }: { children: ReactNode }) {
  const [leaf, setLeaf] = useState<string | null>(null);
  const [trail, setTrail] = useState<Crumb[] | null>(null);
  // The useState setters are stable; value identity only changes when
  // leaf/trail do, so the Breadcrumb re-renders exactly when needed.
  const value = useMemo(() => ({ leaf, setLeaf, trail, setTrail }), [leaf, trail]);
  return <Ctx.Provider value={value}>{children}</Ctx.Provider>;
}

// useBreadcrumbLeafValue is read by the Breadcrumb renderer.
export function useBreadcrumbLeafValue(): string | null {
  return useContext(Ctx)?.leaf ?? null;
}

// useBreadcrumbLeaf sets the trailing breadcrumb label for the current
// page (e.g. an integration's name) and clears it on unmount.
export function useBreadcrumbLeaf(label: string | null | undefined) {
  const setLeaf = useContext(Ctx)?.setLeaf;
  useEffect(() => {
    if (!setLeaf) return;
    setLeaf(label?.trim() ? label.trim() : null);
    return () => setLeaf(null);
  }, [setLeaf, label]);
}

// useBreadcrumbTrailValue is read by the Breadcrumb renderer; when set it
// replaces the URL-derived trail entirely.
export function useBreadcrumbTrailValue(): Crumb[] | null {
  return useContext(Ctx)?.trail ?? null;
}

// useBreadcrumbTrail sets the full breadcrumb trail for the current page
// (origin crumbs included) and clears it on unmount. Pass null to fall
// back to the URL-derived trail. Deps compare by content, so callers may
// build the array inline without memoizing.
export function useBreadcrumbTrail(trail: Crumb[] | null | undefined) {
  const setTrail = useContext(Ctx)?.setTrail;
  const key = JSON.stringify(trail ?? null);
  useEffect(() => {
    if (!setTrail) return;
    setTrail(trail && trail.length > 0 ? trail : null);
    return () => setTrail(null);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [setTrail, key]);
}
