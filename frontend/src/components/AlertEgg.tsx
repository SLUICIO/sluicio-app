// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// TEMPORARY easter egg: with ?alertegg=true in the URL and at least one
// unread notification, the top-bar bell shakes, swells until it bursts,
// and showers the screen with small bells. Self-contained (its own
// styles, a portal, no global CSS) so removing it is deleting this file
// and its two lines in DigestBell.

import { useEffect, useMemo, useState, type RefObject } from "react";
import { createPortal } from "react-dom";

const SWELL_MS = 2600;
const SHOWER_MS = 3200;
const BELLS = 140;

type Phase = "idle" | "swell" | "shower" | "done";

interface Piece {
  dx: number;
  rise: number;
  fall: number;
  rot: number;
  size: number;
  delay: number;
  duration: number;
  hue: string;
}

const HUES = ["var(--primary)", "var(--warn, #f59e0b)", "var(--ok, #16a34a)", "var(--err, #ef4444)", "var(--ink-2)"];

function BellSvg({ size, color }: { size: number; color: string }) {
  return (
    <svg viewBox="0 0 16 16" width={size} height={size} fill={color} stroke={color} strokeWidth="1" strokeLinejoin="round" aria-hidden>
      <path d="M8 2a3.5 3.5 0 00-3.5 3.5c0 3-1.5 4-1.5 4h10s-1.5-1-1.5-4A3.5 3.5 0 008 2z" />
      <path d="M6.5 13a1.5 1.5 0 003 0" fill="none" />
    </svg>
  );
}

export default function AlertEgg({ anchor, play }: { anchor: RefObject<HTMLElement | null>; play: boolean }) {
  const [phase, setPhase] = useState<Phase>("idle");
  const [origin, setOrigin] = useState<{ x: number; y: number } | null>(null);

  useEffect(() => {
    if (!play || phase !== "idle" || !anchor.current) return;
    const r = anchor.current.getBoundingClientRect();
    setOrigin({ x: r.left + r.width / 2, y: r.top + r.height / 2 });
    setPhase("swell");
  }, [play, phase, anchor]);

  useEffect(() => {
    const el = anchor.current;
    if (phase === "idle" || phase === "done" || !el) return;
    // The real bell steps aside while its stand-in performs.
    el.style.visibility = "hidden";
    const t = window.setTimeout(
      () => setPhase(phase === "swell" ? "shower" : "done"),
      phase === "swell" ? SWELL_MS : SHOWER_MS,
    );
    return () => window.clearTimeout(t);
  }, [phase, anchor]);

  useEffect(() => {
    if (phase === "done" && anchor.current) anchor.current.style.visibility = "";
  }, [phase, anchor]);

  // Where it bursts: part of the way from the bell to the middle of the
  // screen, so a corner does not clip it as it swells.
  const burst = useMemo(
    () =>
      origin && {
        x: origin.x + (window.innerWidth / 2 - origin.x) * 0.45,
        y: origin.y + (window.innerHeight / 2 - origin.y) * 0.35,
      },
    [origin],
  );

  const pieces = useMemo<Piece[]>(() => {
    if (!burst) return [];
    const w = window.innerWidth;
    const h = window.innerHeight;
    return Array.from({ length: BELLS }, (_, i) => ({
      dx: (Math.random() * 2 - 1) * w * 0.75,
      rise: -(80 + Math.random() * h * 0.35),
      fall: h - burst.y + 40 + Math.random() * 120,
      rot: (Math.random() * 2 - 1) * 900,
      size: 10 + Math.random() * 16,
      delay: Math.random() * 250,
      duration: 2200 + Math.random() * 900,
      hue: HUES[i % HUES.length],
    }));
  }, [burst]);

  if (!origin || !burst || phase === "idle" || phase === "done") return null;

  return createPortal(
    <div aria-hidden style={{ position: "fixed", inset: 0, pointerEvents: "none", zIndex: 9999, overflow: "hidden" }}>
      <style>{`
        @keyframes alertegg-swell {
          0%   { transform: translate(calc(-50% + var(--tx) * 0), calc(-50% + var(--ty) * 0)) scale(1) rotate(0deg); }
          10%  { transform: translate(calc(-50% + var(--tx) * 0.1), calc(-50% + var(--ty) * 0.1)) scale(1.3) rotate(-14deg); }
          20%  { transform: translate(calc(-50% + var(--tx) * 0.2), calc(-50% + var(--ty) * 0.2)) scale(1.7) rotate(14deg); }
          30%  { transform: translate(calc(-50% + var(--tx) * 0.3), calc(-50% + var(--ty) * 0.3)) scale(2.2) rotate(-18deg); }
          40%  { transform: translate(calc(-50% + var(--tx) * 0.4), calc(-50% + var(--ty) * 0.4)) scale(2.8) rotate(18deg); }
          50%  { transform: translate(calc(-50% + var(--tx) * 0.5), calc(-50% + var(--ty) * 0.5)) scale(3.5) rotate(-22deg); }
          60%  { transform: translate(calc(-50% + var(--tx) * 0.6), calc(-50% + var(--ty) * 0.6)) scale(4.3) rotate(22deg); }
          70%  { transform: translate(calc(-50% + var(--tx) * 0.7), calc(-50% + var(--ty) * 0.7)) scale(5.2) rotate(-26deg); }
          80%  { transform: translate(calc(-50% + var(--tx) * 0.8), calc(-50% + var(--ty) * 0.8)) scale(6.2) rotate(26deg); }
          90%  { transform: translate(calc(-50% + var(--tx) * 0.9), calc(-50% + var(--ty) * 0.9)) scale(7.2) rotate(-30deg); }
          100% { transform: translate(calc(-50% + var(--tx) * 1), calc(-50% + var(--ty) * 1)) scale(8.5) rotate(0deg); }
        }
        @keyframes alertegg-flash {
          0%   { transform: translate(-50%, -50%) scale(0.2); opacity: 0.9; }
          100% { transform: translate(-50%, -50%) scale(14); opacity: 0; }
        }
        @keyframes alertegg-x { from { transform: translateX(0); } to { transform: translateX(var(--dx)); } }
        @keyframes alertegg-y {
          0%   { transform: translateY(0) rotate(0deg); opacity: 1; animation-timing-function: cubic-bezier(.2,.7,.4,1); }
          30%  { transform: translateY(var(--rise)) rotate(calc(var(--rot) * .3)); opacity: 1; animation-timing-function: cubic-bezier(.5,0,.9,.5); }
          85%  { opacity: 1; }
          100% { transform: translateY(var(--fall)) rotate(var(--rot)); opacity: 0; }
        }
      `}</style>
      {phase === "swell" && (
        <div
          style={{
            position: "absolute",
            left: origin.x,
            top: origin.y,
            transformOrigin: "50% 15%",
            // Drifts toward the middle as it swells, so it is not
            // clipped by the corner of the screen when it bursts.
            ...({ "--tx": `${burst.x - origin.x}px`, "--ty": `${burst.y - origin.y}px` } as React.CSSProperties),
            animation: `alertegg-swell ${SWELL_MS}ms cubic-bezier(.4,0,1,1) forwards`,
            filter: "drop-shadow(0 2px 6px rgba(0,0,0,.25))",
          }}
        >
          <BellSvg size={18} color="var(--primary)" />
        </div>
      )}
      {phase === "shower" && (
        <>
          <div
            style={{
              position: "absolute",
              left: burst.x,
              top: burst.y,
              width: 40,
              height: 40,
              borderRadius: "50%",
              background: "radial-gradient(circle, var(--primary) 0%, transparent 70%)",
              animation: "alertegg-flash 600ms ease-out forwards",
            }}
          />
          {pieces.map((p, i) => (
            <div
              key={i}
              style={
                {
                  position: "absolute",
                  left: burst.x - p.size / 2,
                  top: burst.y - p.size / 2,
                  "--dx": `${p.dx}px`,
                  animation: `alertegg-x ${p.duration}ms ease-out ${p.delay}ms both`,
                } as React.CSSProperties
              }
            >
              <div
                style={
                  {
                    "--rise": `${p.rise}px`,
                    "--fall": `${p.fall}px`,
                    "--rot": `${p.rot}deg`,
                    animation: `alertegg-y ${p.duration}ms linear ${p.delay}ms both`,
                  } as React.CSSProperties
                }
              >
                <BellSvg size={p.size} color={p.hue} />
              </div>
            </div>
          ))}
        </>
      )}
    </div>,
    document.body,
  );
}
