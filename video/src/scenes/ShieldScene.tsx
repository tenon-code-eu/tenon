// Scenes A (intro) and E (outro): the tenon shield in relief inside the circle of twelve EU stars.
import React, { useMemo } from "react";
import * as THREE from "three";
import { AbsoluteFill, interpolate, useCurrentFrame, useVideoConfig } from "remotion";
import { T, type Lang } from "../i18n";
import { C, FONT, color3 } from "../theme";
import { buildShield, glowTexture, seeded, starGeometry } from "../three/shapes";
import { Stage, makeCamera, unitsPerPx } from "../three/Stage";
import { Dip, Rise, eo, useFormat } from "../ui";

const RING_R = 4.6, FOV = 35, DIST = 20;
const ease = (x: number) => eo(Math.max(0, Math.min(1, x)));

function useWorld() {
  return useMemo(() => {
    const root = new THREE.Group();
    root.add(new THREE.AmbientLight(0xffffff, 0.9));
    const key = new THREE.DirectionalLight(0xffffff, 2.4);
    key.position.set(4, 7, 10);
    root.add(key);
    const goldL = new THREE.PointLight(color3("gold"), 30, 30);
    goldL.position.set(-5, -3, 6);
    root.add(goldL);
    const rim = new THREE.PointLight(color3("markTrace"), 40, 30);
    rim.position.set(6, 5, -4);
    root.add(rim);
    const group = new THREE.Group();
    root.add(group);
    const { shield, glowMat } = buildShield();
    group.add(shield);
    const halo = new THREE.Sprite(new THREE.SpriteMaterial({ map: glowTexture(), color: color3("markTrace"), transparent: true, opacity: 0.35, blending: THREE.AdditiveBlending, depthWrite: false }));
    halo.scale.set(11, 11, 1);
    halo.position.z = -1;
    shield.add(halo);
    const geo = starGeometry(0.34, 0.1);
    const mat = new THREE.MeshStandardMaterial({ color: color3("gold"), emissive: color3("gold"), emissiveIntensity: 0.4, metalness: 0.55, roughness: 0.3 });
    const r = seeded(3);
    const stars = Array.from({ length: 12 }, (_, i) => {
      const g = new THREE.Group();
      g.add(new THREE.Mesh(geo, mat));
      const glow = new THREE.Sprite(new THREE.SpriteMaterial({ map: glowTexture(), color: color3("gold"), transparent: true, opacity: 0.55, blending: THREE.AdditiveBlending, depthWrite: false }));
      glow.scale.set(1.6, 1.6, 1);
      g.add(glow);
      group.add(g);
      const a = Math.PI / 2 - (i * Math.PI) / 6;
      return { g, end: new THREE.Vector3(Math.cos(a) * RING_R, Math.sin(a) * RING_R, 0), from: new THREE.Vector3(Math.cos(a) * 30, Math.sin(a) * 18, -40), spin: 6 + r() * 6 };
    });
    return { root, group, shield, glowMat, stars };
  }, []);
}

export const ShieldScene: React.FC<{ lang: Lang; mode: "intro" | "outro"; durationInFrames: number }> = ({ lang, mode, durationInFrames }) => {
  const frame = useCurrentFrame();
  const { fps } = useVideoConfig();
  const { w, h, kind, size } = useFormat();
  const W = useWorld();
  const t = frame / fps, intro = mode === "intro", tx = T(lang);

  /* layout: final centre and radius of the star circle, in pixels */
  const rpx = kind === "landscape" ? h * 0.25 : kind === "square" ? w * 0.25 : w * 0.33;
  const cyEnd = kind === "landscape" ? h * 0.37 : kind === "square" ? h * 0.36 : h * 0.34;
  const up = intro ? ease((t - 3.2) / 1.4) : 1;
  const cy = interpolate(up, [0, 1], [h / 2, cyEnd]);
  const k = unitsPerPx(FOV, DIST, h);
  W.group.position.set(0, -(cy - h / 2) * k, 0);
  W.group.scale.setScalar(((rpx * k) / RING_R) * interpolate(up, [0, 1], [1.12, 1]));

  /* stars fly in from depth (intro), or are already there and breathe (outro) */
  W.stars.forEach((s, i) => {
    const kk = intro ? ease((t - 0.2 - i * 0.07) / 1.4) : 1;
    s.g.position.lerpVectors(s.from, s.end, kk);
    s.g.visible = kk > 0;
    s.g.children[0].rotation.y = (1 - kk) * s.spin + (intro ? 0 : Math.sin(t * 1.4 + i) * 0.25);
    const wave = intro ? 0 : Math.exp(-Math.pow((t - 0.6 - i * 0.08) / 0.18, 2));
    s.g.scale.setScalar(1 + 0.4 * wave);
  });
  const sk = intro ? ease((t - 1.4) / 1.6) : 1;
  W.shield.scale.setScalar(0.2 + 0.8 * sk);
  W.shield.rotation.y = (1 - sk) * Math.PI * 1.5 + Math.sin(t * 0.8) * 0.16;
  W.shield.rotation.x = Math.sin(t * 0.6) * 0.06;
  W.glowMat.emissiveIntensity = 0.3 + 0.2 * sk + 0.1 * Math.sin(t * 1.7);

  const cam = makeCamera(FOV, w, h, new THREE.Vector3(Math.sin(t * 0.25) * (intro ? 0 : 0.8), 0, DIST), new THREE.Vector3(0, 0, 0));
  const textTop = cyEnd + rpx * 1.12;

  return (
    <AbsoluteFill>
      <Stage root={W.root} cam={cam} />
      <div style={{ position: "absolute", left: size.pad, right: size.pad, top: textTop, textAlign: "center" }}>
        <Rise at={intro ? 3.8 : 1.4}>
          <div style={{ fontFamily: FONT.display, fontWeight: 700, fontSize: size.title * 1.55, lineHeight: 1, letterSpacing: "-0.03em", color: C.on }}>tenon</div>
          <div style={{ fontFamily: FONT.body, fontSize: size.text, color: C.muted, marginTop: 18 }}>{intro ? tx.a_sub : tx.e_tag}</div>
        </Rise>
        {!intro && (
          <Rise at={2.2}>
            <div style={{ display: "inline-block", marginTop: 34, fontFamily: FONT.mono, fontSize: size.mono * 1.25, padding: "16px 30px", border: `1px solid ${C.rule}`, borderRadius: 12, background: "oklch(14% 0.04 264 / .8)", color: C.on }}>
              <span style={{ color: C.gold }}>$</span> brew install tenon-code-eu/tap/tenon
            </div>
            <div style={{ marginTop: 30, fontFamily: FONT.mono, fontSize: size.meta * 1.1, color: C.muted, letterSpacing: "0.04em", lineHeight: 1.6 }}>
              {tx.e_foot[0]}
              {kind === "landscape" ? "  ·  " : <br />}
              {tx.e_foot[1]} · <span style={{ color: C.gold }}>{tx.e_foot[2]}</span>
            </div>
          </Rise>
        )}
      </div>
      <Dip durationInFrames={durationInFrames} out={intro} />
    </AbsoluteFill>
  );
};
