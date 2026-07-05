// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// usePageTitle sets the document.title for the page that calls it.
// The product name suffix is added automatically, so a page just
// passes its own label ("Services", "Order Sync", …) and the browser
// tab reads "Services · Sluicio".
//
// Passing null or an empty string falls back to the product name only.
// On unmount the title is reset so a page that didn't set its own
// (e.g. while transitioning) doesn't inherit the previous one.

import { useEffect } from "react";

export const PRODUCT_NAME = "Sluicio";

export function usePageTitle(title: string | null | undefined) {
  useEffect(() => {
    const trimmed = title?.trim();
    document.title = trimmed ? `${trimmed} · ${PRODUCT_NAME}` : PRODUCT_NAME;
    return () => {
      document.title = PRODUCT_NAME;
    };
  }, [title]);
}
