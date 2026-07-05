// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Breadcrumb leaf context. The section crumb (Integrations, Services, …)
// is derived from the URL in AppShell, but the trailing entity name often
// isn't in the URL — e.g. an integration's route carries its id, not its
// name. A detail page sets the leaf via useBreadcrumbLeaf(name) while
// mounted; AppShell's Breadcrumb reads it. Cleared on unmount so a name
// never bleeds into the next page.

import {
  createContext,
  useContext,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from "react";

interface BreadcrumbCtx {
  leaf: string | null;
  setLeaf: (v: string | null) => void;
}

const Ctx = createContext<BreadcrumbCtx | null>(null);

export function BreadcrumbProvider({ children }: { children: ReactNode }) {
  const [leaf, setLeaf] = useState<string | null>(null);
  // setLeaf (useState setter) is stable; value identity only changes when
  // leaf does, so the Breadcrumb re-renders exactly when needed.
  const value = useMemo(() => ({ leaf, setLeaf }), [leaf]);
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
