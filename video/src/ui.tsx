// Layout per format, the backdrop, scene dips and the shared text blocks.
import React from "react";
import { AbsoluteFill, Easing, interpolate, useCurrentFrame, useVideoConfig } from "remotion";
import { C, FONT } from "./theme";
import { seeded } from "./three/shapes";

export type Kind = "landscape" | "square" | "vertical";

export function useFormat() {
  const { width: w, height: h } = useVideoConfig();
  const kind: Kind = w > h * 1.2 ? "landscape" : h > w * 1.2 ? "vertical" : "square";
  // Text sizes: 1920×1080 base, or 1080 wide (main headline ≥ 84 px, support ≥ 44 px).
  const size =
    kind === "landscape"
      ? { kicker: 24, title: 96, text: 34, label: 26, meta: 20, mono: 23, pad: 140 }
      : kind === "square"
        ? { kicker: 28, title: 88, text: 40, label: 30, meta: 22, mono: 26, pad: 84 }
        : { kicker: 30, title: 96, text: 44, label: 32, meta: 24, mono: 28, pad: 84 };
  return { w, h, kind, size };
}

export const eo = Easing.bezier(0.16, 1, 0.3, 1);
export const clampOpts = { extrapolateLeft: "clamp", extrapolateRight: "clamp" } as const;

/* Night sky: radial glow and a slowly drifting starfield (deterministic). */
const STARS = (() => {
  const r = seeded(7);
  return Array.from({ length: 220 }, () => ({ x: r() * 100, y: r() * 100, s: 0.6 + r() * 1.8, o: 0.25 + r() * 0.55 }));
})();
export const Backdrop: React.FC = () => {
  const frame = useCurrentFrame();
  const { w, h } = useFormat();
  return (
    <AbsoluteFill style={{ background: `radial-gradient(${w * 0.7}px ${h * 0.75}px at 50% 45%, oklch(26% 0.08 264), ${C.night} 70%)` }}>
      <svg width={w} height={h} style={{ position: "absolute", inset: 0, translate: `${-frame * 0.08}px 0px` }}>
        {STARS.map((s, i) => (
          <circle key={i} cx={(s.x / 100) * (w + 200)} cy={(s.y / 100) * h} r={s.s} fill="oklch(85% 0.04 264)" opacity={s.o} />
        ))}
      </svg>
    </AbsoluteFill>
  );
};

/* Dip to night at the start and end of a scene (0.5 s), as in the storyboard. */
export const Dip: React.FC<{ durationInFrames: number; out?: boolean }> = ({ durationInFrames, out = true }) => {
  const frame = useCurrentFrame();
  const { fps } = useVideoConfig();
  const o = Math.max(
    interpolate(frame, [0, 0.5 * fps], [1, 0], clampOpts),
    out ? interpolate(frame, [durationInFrames - 0.5 * fps, durationInFrames], [0, 1], clampOpts) : 0,
  );
  return <AbsoluteFill style={{ background: C.night, opacity: o, pointerEvents: "none" }} />;
};

export const Kicker: React.FC<{ children: React.ReactNode; size: number }> = ({ children, size }) => (
  <div style={{ fontFamily: FONT.mono, fontWeight: 500, fontSize: size, letterSpacing: "0.18em", textTransform: "uppercase", color: C.gold }}>{children}</div>
);

export const Title: React.FC<{ children: React.ReactNode; size: number; style?: React.CSSProperties }> = ({ children, size, style }) => (
  <div style={{ fontFamily: FONT.display, fontWeight: 700, fontSize: size, lineHeight: 1.02, letterSpacing: "-0.025em", color: C.on, ...style }}>{children}</div>
);

/* A text block that rises in: opacity and offset driven by the frame. */
export const Rise: React.FC<{ at: number; children: React.ReactNode; style?: React.CSSProperties; dy?: number }> = ({ at, children, style, dy = 30 }) => {
  const frame = useCurrentFrame();
  const { fps } = useVideoConfig();
  return (
    <div
      style={{
        ...style,
        opacity: interpolate(frame, [at * fps, (at + 0.9) * fps], [0, 1], { ...clampOpts, easing: eo }),
        translate: interpolate(frame, [at * fps, (at + 0.9) * fps], [`0px ${dy}px`, "0px 0px"], { ...clampOpts, easing: eo }),
      }}
    >
      {children}
    </div>
  );
};

/* A label pinned to a projected 3D point. */
export const Pin: React.FC<{ x: number; y: number; anchor?: string; opacity: number; children: React.ReactNode; align?: "center" | "left" }> = ({
  x, y, anchor = "-50% 0%", opacity, children, align = "center",
}) => (
  <div style={{ position: "absolute", left: x, top: y, translate: anchor, opacity, textAlign: align, whiteSpace: "nowrap", textShadow: `0 2px 12px ${C.night}` }}>{children}</div>
);
