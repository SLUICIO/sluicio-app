// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Copying to the clipboard on a self-hosted cell, which is often plain
// HTTP: the async clipboard API only exists in a secure context.

// copyText copies via the async clipboard API, falling back to a
// hidden textarea + execCommand for non-secure contexts (http://, some
// embedded webviews) where navigator.clipboard is unavailable.
export async function copyText(text: string): Promise<boolean> {
  try {
    if (navigator.clipboard?.writeText) {
      await navigator.clipboard.writeText(text);
      return true;
    }
  } catch {
    /* fall through to the legacy path */
  }
  try {
    const ta = document.createElement("textarea");
    ta.value = text;
    ta.style.position = "fixed";
    ta.style.opacity = "0";
    document.body.appendChild(ta);
    ta.select();
    const ok = document.execCommand("copy");
    document.body.removeChild(ta);
    return ok;
  } catch {
    return false;
  }
}
