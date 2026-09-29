// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Stagecraft for recorded walkthroughs: what makes a Playwright run
// watchable by a person rather than merely correct.
//
// A test clicks as fast as the page allows and teleports the pointer;
// a viewer needs to see where the pointer goes, have a moment to read
// what changed, and - in a video with no voice - a line saying what is
// happening. Playwright's own recordings do not draw the pointer at
// all, so the cursor here is drawn into the page.

import { readFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import type { Locator, Page } from "@playwright/test";

/**
 * Loads e2e/.env.recording (gitignored) into process.env without
 * overriding anything already set. It holds the recording stack's URL
 * and the on-camera account, so neither ends up in the repo.
 */
export function loadRecordingEnv(): void {
  const here = path.dirname(fileURLToPath(import.meta.url));
  const file = path.join(here, "..", ".env.recording");
  let text = "";
  try {
    text = readFileSync(file, "utf8");
  } catch {
    return;
  }
  for (const line of text.split("\n")) {
    const m = /^\s*([A-Z0-9_]+)\s*=\s*(.*?)\s*$/.exec(line);
    if (m && process.env[m[1]] === undefined) process.env[m[1]] = m[2];
  }
}

export function requireEnv(name: string): string {
  const v = process.env[name];
  if (!v) throw new Error(`${name} is not set - see e2e/recordings/README.md`);
  return v;
}

// Pace. One number to change for a slower or snappier cut.
const PACE = Number(process.env.RECORDING_PACE ?? "1");
export const beat = (page: Page, ms: number) => page.waitForTimeout(ms * PACE);

/**
 * Draws a pointer and a click ripple into every page of the context.
 * Driven by the real mouse events Playwright dispatches, so it is
 * exactly where the click lands.
 */
export async function installCursor(page: Page): Promise<void> {
  await page.addInitScript(() => {
    const draw = () => {
      if (document.getElementById("__rec-cursor")) return;
      const c = document.createElement("div");
      c.id = "__rec-cursor";
      c.innerHTML =
        '<svg width="22" height="22" viewBox="0 0 24 24"><path d="M4 2 L4 19 L8.5 14.5 L11.5 21 L14 20 L11 13.5 L17.5 13.5 Z" fill="#111" stroke="#fff" stroke-width="1.5" stroke-linejoin="round"/></svg>';
      Object.assign(c.style, {
        position: "fixed",
        left: "0",
        top: "0",
        zIndex: "2147483647",
        pointerEvents: "none",
        transform: "translate(-3px, -2px)",
        transition: "left 30ms linear, top 30ms linear",
      });
      document.body.appendChild(c);
      document.addEventListener(
        "mousemove",
        (e) => {
          c.style.left = `${e.clientX}px`;
          c.style.top = `${e.clientY}px`;
        },
        true,
      );
      document.addEventListener(
        "mousedown",
        (e) => {
          const r = document.createElement("div");
          Object.assign(r.style, {
            position: "fixed",
            left: `${e.clientX - 16}px`,
            top: `${e.clientY - 16}px`,
            width: "32px",
            height: "32px",
            borderRadius: "50%",
            border: "3px solid rgba(76, 154, 255, 0.85)",
            zIndex: "2147483646",
            pointerEvents: "none",
            transition: "transform 350ms ease-out, opacity 350ms ease-out",
          });
          document.body.appendChild(r);
          requestAnimationFrame(() => {
            r.style.transform = "scale(1.8)";
            r.style.opacity = "0";
          });
          setTimeout(() => r.remove(), 400);
        },
        true,
      );
    };
    if (document.body) draw();
    else document.addEventListener("DOMContentLoaded", draw);
  });
}

const CAPTIONS = process.env.RECORDING_CAPTIONS === "1";

/**
 * A caption across the bottom of the frame, for a cut that runs without
 * a voice. Off by default: with a voice-over it is clutter. Empty text
 * clears it.
 */
export async function caption(page: Page, text: string): Promise<void> {
  if (!CAPTIONS) return;
  await page.evaluate((t) => {
    let el = document.getElementById("__rec-caption");
    if (!el) {
      el = document.createElement("div");
      el.id = "__rec-caption";
      Object.assign(el.style, {
        position: "fixed",
        left: "50%",
        bottom: "28px",
        transform: "translateX(-50%)",
        maxWidth: "70%",
        padding: "10px 18px",
        borderRadius: "10px",
        background: "rgba(17, 24, 39, 0.88)",
        color: "#fff",
        font: "500 17px/1.4 Inter, system-ui, sans-serif",
        textAlign: "center",
        zIndex: "2147483645",
        pointerEvents: "none",
        transition: "opacity 200ms",
      });
      document.body.appendChild(el);
      // Room for the caption: the app scrolls inside <main>, and without
      // a strip after the last element, whatever sits at the bottom of a
      // page - the add box's suggestions, say - stays under the caption
      // however it is scrolled.
      const room = document.createElement("style");
      room.textContent = "main::after { content: ''; display: block; flex-shrink: 0; height: 110px; }";
      document.head.appendChild(room);
    }
    el.textContent = t;
    el.style.opacity = t ? "1" : "0";
  }, text);
}

/** Moves the pointer to the middle of a target at a speed a viewer can follow. */
export async function glideTo(page: Page, target: Locator): Promise<void> {
  await target.waitFor({ state: "visible" });
  // Centred, not merely in view: the action stays in the middle of the
  // frame, clear of the caption along the bottom, and a smooth scroll
  // reads as a camera move rather than a cut.
  await target.evaluate((el) => el.scrollIntoView({ block: "center", behavior: "smooth" }));
  await beat(page, 450);
  const box = await target.boundingBox();
  if (!box) throw new Error("target has no box - is it rendered?");
  await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2, { steps: 30 });
  await beat(page, 250);
}

/**
 * Glides to a target and clicks it where the pointer is. locator.click()
 * would move the pointer there in one jump, which is exactly the motion
 * a viewer cannot follow.
 */
export async function glideClick(page: Page, target: Locator): Promise<void> {
  await glideTo(page, target);
  await page.mouse.down();
  await page.waitForTimeout(70);
  await page.mouse.up();
  await beat(page, 400);
}

/** Types into a target at a human pace. */
export async function typeInto(page: Page, target: Locator, text: string): Promise<void> {
  await glideClick(page, target);
  await target.pressSequentially(text, { delay: Math.round(70 * PACE) });
  await beat(page, 300);
}
