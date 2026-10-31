// Scene B: "Nothing changes until you say yes." A terminal where the write waits for approval.
import React from "react";
import { AbsoluteFill, interpolate, useCurrentFrame, useVideoConfig } from "remotion";
import { T, type Lang } from "../i18n";
import { C, FONT } from "../theme";
import { Dip, Kicker, Rise, Title, clampOpts, eo, useFormat } from "../ui";

const Line: React.FC<{ at: number; children: React.ReactNode }> = ({ at, children }) => {
  const frame = useCurrentFrame();
  const { fps } = useVideoConfig();
  return <div style={{ opacity: interpolate(frame, [at * fps, (at + 0.25) * fps], [0, 1], clampOpts), whiteSpace: "pre" }}>{children}</div>;
};

export const Control: React.FC<{ lang: Lang; durationInFrames: number }> = ({ lang, durationInFrames }) => {
  const frame = useCurrentFrame();
  const { fps } = useVideoConfig();
  const { w, kind, size } = useFormat();
  const tx = T(lang), l = frame / fps;
  const land = kind === "landscape";
  const term = land ? { left: 840, top: 250, width: 960, height: 580 } : kind === "square"
    ? { left: size.pad, top: 470, width: w - 2 * size.pad, height: 540 }
    : { left: size.pad, top: 700, width: w - 2 * size.pad, height: 900 };
  const ck = interpolate(frame, [0.1 * fps, 1.4 * fps], [0, 1], { ...clampOpts, easing: eo });
  const card = interpolate(frame, [3.2 * fps, 3.6 * fps], [0, 1], { ...clampOpts, easing: eo });
  const yes = l > 4.6;
  const fs = kind === "vertical" ? 32 : size.mono;

  return (
    <AbsoluteFill>
      <div style={{ position: "absolute", left: land ? 140 : size.pad, top: land ? 300 : kind === "square" ? 100 : 200, width: land ? 620 : w - 2 * size.pad }}>
        <Rise at={0.1} dy={0}>
          <Kicker size={size.kicker}>{tx.b_kicker}</Kicker>
          <Title size={land ? 84 : size.title} style={{ margin: "22px 0 28px" }}>{tx.b_title}</Title>
          <div style={{ fontFamily: FONT.body, fontSize: land ? 30 : size.text * 0.82, lineHeight: 1.4, color: C.muted }}>{tx.b_text}</div>
        </Rise>
      </div>
      <div style={{ position: "absolute", ...term, perspective: 1800 }}>
        <div
          style={{
            width: "100%", height: "100%", borderRadius: 18, overflow: "hidden", opacity: ck,
            background: "oklch(14% 0.04 264 / .94)", border: `1px solid ${C.rule}`,
            boxShadow: "0 60px 140px oklch(5% 0.03 264 / .8)",
            transform: `rotateY(${interpolate(ck, [0, 1], [land ? -28 : -12, land ? -9 : -3]) + Math.sin(l * 0.6) * 1.2}deg) rotateX(${interpolate(ck, [0, 1], [8, 3])}deg) translateZ(${interpolate(ck, [0, 1], [-200, 0])}px)`,
          }}
        >
          <div style={{ height: 54, borderBottom: `1px solid ${C.rule}`, display: "flex", alignItems: "center", gap: 10, padding: "0 22px", fontFamily: FONT.mono, fontSize: 18, color: C.muted }}>
            {[0, 1, 2].map((i) => <span key={i} style={{ width: 14, height: 14, borderRadius: 7, background: C.rule }} />)}&nbsp; ~/api · tenon
          </div>
          <div style={{ padding: "28px 32px", fontFamily: FONT.mono, fontSize: fs, lineHeight: 1.6, color: C.on }}>
            <Line at={0.9}><span style={{ color: C.gold }}>›</span> {tx.b_prompt}</Line>
            <Line at={1.7}><span style={{ color: C.muted }}>  read </span> billing/invoice.go</Line>
            <Line at={2.1}><span style={{ color: C.muted }}>  read </span> billing/invoice_test.go</Line>
            <Line at={2.7}><span style={{ color: C.markTrace }}>  edit </span> billing/invoice.go  <span style={{ color: "oklch(80% 0.15 150)" }}>+4</span> <span style={{ color: C.danger }}>−2</span></Line>
            <div style={{ margin: "12px 0", border: `1px solid ${C.gold}`, borderRadius: 12, padding: "16px 20px", background: "oklch(86% 0.17 92 / .06)", opacity: card, scale: String(0.96 + 0.04 * card), whiteSpace: "normal" }}>
              <div style={{ fontFamily: FONT.body, fontWeight: 600, fontSize: fs * 1.04 }}>{tx.b_perm}</div>
              <div style={{ fontSize: fs * 0.82, margin: "6px 0 12px", color: C.muted }}>{tx.b_perm_d}</div>
              {tx.b_keys.map((k, i) => (
                <span key={k} style={{ display: "inline-block", padding: "4px 12px", borderRadius: 8, marginRight: 10, marginTop: 6, fontSize: fs * 0.8,
                  border: `1px solid ${i === 0 ? C.gold : C.rule}`, color: i === 0 ? (yes ? C.night : C.gold) : C.on, background: i === 0 && yes ? C.gold : "transparent" }}>{k}</span>
              ))}
            </div>
            <Line at={5.0}><span style={{ color: C.markTrace }}>  run  </span> go test ./billing  <span style={{ color: C.muted }}>[{tx.b_sandboxed}]</span></Line>
            <Line at={5.7}><span style={{ color: "oklch(80% 0.15 150)" }}>  ok   </span> billing  0.41s</Line>
            <div style={{ display: "inline-block", marginTop: 12, padding: "6px 14px", borderRadius: 999, border: "1px solid oklch(62% 0.17 264)", fontSize: fs * 0.82,
              opacity: interpolate(frame, [6.0 * fps, 6.4 * fps], [0, 1], { ...clampOpts, easing: eo }) }}>{tx.b_badge}</div>
          </div>
        </div>
      </div>
      <Dip durationInFrames={durationInFrames} />
    </AbsoluteFill>
  );
};
