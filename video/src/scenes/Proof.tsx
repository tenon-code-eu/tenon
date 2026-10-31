// Scene D: "Every step, on the record." The audit chain builds block by block with tenon's real
// event types; each block shows the previous hash and its own (computed like internal/audit),
// then tenon audit verify walks the chain and every block turns gold.
import React, { useMemo } from "react";
import * as THREE from "three";
import { AbsoluteFill, interpolate, useCurrentFrame, useVideoConfig } from "remotion";
import { EVENTS, T, type Lang } from "../i18n";
import { C, FONT, color3, hex } from "../theme";
import { auditChain } from "../sha256";
import { Stage, makeCamera, unitsPerPx } from "../three/Stage";
import { Dip, Kicker, Rise, Title, clampOpts, eo, useFormat, type Kind } from "../ui";

const FOV = 35, BW = 2.4, BH = 1.25;
const cl = (x: number, a = 0, b = 1) => Math.max(a, Math.min(b, x));
const short = (h: string) => (h === "sha256:0" ? "sha256:0" : h.slice(7, 15));

function layoutOf(kind: Kind, n: number) {
  const cols = kind === "landscape" ? 4 : kind === "square" ? 3 : 2, rows = Math.ceil(n / cols), dx = 2.85, dy = 1.85;
  return {
    cols,
    pos: (i: number) => { const r = Math.floor(i / cols), c = r % 2 ? cols - 1 - (i % cols) : i % cols; return new THREE.Vector3((c - (cols - 1) / 2) * dx, ((rows - 1) / 2 - r) * dy, Math.sin(i * 0.9) * 0.4); },
    ex: ((cols - 1) * dx) / 2 + 1.5, ey: ((rows - 1) * dy) / 2 + 0.95,
  };
}

function drawFace(c: HTMLCanvasElement, seq: number, type: string, prev: string, hash: string, verified: boolean) {
  const g = c.getContext("2d")!;
  g.fillStyle = hex("night2"); g.fillRect(0, 0, 512, 268);
  g.font = `500 26px "${FONT.mono}"`; g.fillStyle = hex("muted"); g.fillText("#" + String(seq).padStart(2, "0"), 26, 48);
  g.font = `500 40px "${FONT.mono}"`; g.fillStyle = verified ? hex("gold") : hex("on");
  let t = type; while (g.measureText(t).width > 460 && t.length > 4) t = t.slice(0, -2) + "…";
  g.fillText(t, 26, 110);
  g.font = `400 26px "${FONT.mono}"`; g.fillStyle = hex("muted"); g.fillText("prev " + short(prev), 26, 170);
  g.fillStyle = hex("gold"); g.fillText("hash " + short(hash), 26, 216);
  if (verified) { g.font = `500 40px "${FONT.mono}"`; g.fillText("✓", 456, 214); }
}

function build(kind: Kind, lang: Lang) {
  const lines = auditChain(EVENTS, EVENTS.map((e) => `${e} · ${lang}`));
  const L = layoutOf(kind, lines.length);
  const root = new THREE.Group();
  root.add(new THREE.AmbientLight(0xffffff, 1.0));
  const key = new THREE.DirectionalLight(0xffffff, 2.0);
  key.position.set(4, 8, 10);
  root.add(key);
  const pulse = new THREE.PointLight(color3("gold"), 0, 9);
  root.add(pulse);
  const rr = new THREE.Shape(), r = 0.16;
  rr.moveTo(-BW / 2 + r, -BH / 2); rr.lineTo(BW / 2 - r, -BH / 2); rr.quadraticCurveTo(BW / 2, -BH / 2, BW / 2, -BH / 2 + r);
  rr.lineTo(BW / 2, BH / 2 - r); rr.quadraticCurveTo(BW / 2, BH / 2, BW / 2 - r, BH / 2); rr.lineTo(-BW / 2 + r, BH / 2);
  rr.quadraticCurveTo(-BW / 2, BH / 2, -BW / 2, BH / 2 - r); rr.lineTo(-BW / 2, -BH / 2 + r); rr.quadraticCurveTo(-BW / 2, -BH / 2, -BW / 2 + r, -BH / 2);
  const geo = new THREE.ExtrudeGeometry(rr, { depth: 0.7, bevelEnabled: true, bevelThickness: 0.06, bevelSize: 0.06, bevelSegments: 2, curveSegments: 6 });
  geo.translate(0, 0, -0.35);
  const edgesGeo = new THREE.EdgesGeometry(geo, 30), faceGeo = new THREE.PlaneGeometry(BW - 0.16, BH - 0.14);
  const blocks = lines.map((l, i) => {
    const g = new THREE.Group();
    const body = new THREE.MeshStandardMaterial({ color: color3("night2"), roughness: 0.55, metalness: 0.25, emissive: color3("gold"), emissiveIntensity: 0 });
    const edge = new THREE.LineBasicMaterial({ color: color3("edge") });
    const faces = [false, true].map((ver) => {
      const c = document.createElement("canvas"); c.width = 512; c.height = 268;
      drawFace(c, i + 1, l.type, l.prev, l.hash, ver);
      const tex = new THREE.CanvasTexture(c); tex.colorSpace = THREE.SRGBColorSpace; tex.anisotropy = 4;
      return tex;
    });
    const face = new THREE.Mesh(faceGeo, new THREE.MeshBasicMaterial({ map: faces[0] }));
    face.position.z = 0.42;
    g.add(new THREE.Mesh(geo, body), new THREE.LineSegments(edgesGeo, edge), face);
    g.rotation.set(0.04 * Math.sin(i * 1.3), -0.18 + 0.1 * Math.cos(i * 0.7), 0);
    root.add(g);
    return { g, body, edge, face, faces, home: L.pos(i) };
  });
  const linkGeo = new THREE.TorusGeometry(0.2, 0.055, 8, 20);
  const linkMat = new THREE.MeshStandardMaterial({ color: color3("edge"), roughness: 0.35, metalness: 0.6, emissive: color3("gold"), emissiveIntensity: 0 });
  const X = new THREE.Vector3(1, 0, 0), qx = new THREE.Quaternion().setFromAxisAngle(X, Math.PI / 2);
  const links = blocks.slice(0, -1).map((b, i) => {
    const a = b.home, c = blocks[i + 1].home, dir = c.clone().sub(a).normalize(), m = a.clone().add(c).multiplyScalar(0.5);
    const q = new THREE.Quaternion().setFromUnitVectors(X, dir);
    const l1 = new THREE.Mesh(linkGeo, linkMat), l2 = new THREE.Mesh(linkGeo, linkMat);
    l1.position.copy(m).addScaledVector(dir, -0.17); l1.quaternion.copy(q);
    l2.position.copy(m).addScaledVector(dir, 0.17); l2.quaternion.copy(q).multiply(qx);
    const grp = new THREE.Group(); grp.add(l1, l2); root.add(grp);
    return grp;
  });
  return { root, blocks, links, linkMat, pulse, L, lines };
}

export const Proof: React.FC<{ lang: Lang; durationInFrames: number }> = ({ lang, durationInFrames }) => {
  const frame = useCurrentFrame();
  const { fps } = useVideoConfig();
  const { w, h, kind, size } = useFormat();
  const W = useMemo(() => build(kind, lang), [kind, lang]);
  const tx = T(lang), t = frame / fps, n = W.blocks.length;
  const built = cl((t - 0.5) / 3.9) * n;                 /* blocks laid (fractional) */
  const ver = cl((t - 4.9) / 1.1) * n;                   /* blocks verified */
  const goldC = color3("gold"), edgeC = color3("edge");

  W.blocks.forEach((b, i) => {
    const k = eo(cl(built - i));
    b.g.visible = k > 0;
    b.g.position.copy(b.home);
    b.g.position.y += (1 - k) * 2.2;
    b.g.scale.setScalar(0.5 + 0.5 * k);
    const isVer = ver > i + 0.5;
    (b.face.material as THREE.MeshBasicMaterial).map = b.faces[isVer ? 1 : 0];
    b.edge.color.copy(isVer ? goldC : edgeC);
    b.body.emissiveIntensity = ver > 0 && ver < n ? Math.max(0, 1 - Math.abs(ver - i - 0.5)) * 0.35 : 0;
  });
  W.links.forEach((l, i) => { l.visible = built > i + 1.4; });
  W.linkMat.emissiveIntensity = ver >= n ? 0.35 : 0;
  W.linkMat.color.copy(ver >= n ? goldC : edgeC);
  if (ver > 0 && ver < n) { W.pulse.position.copy(W.blocks[Math.min(n - 1, Math.floor(ver))].home).add(new THREE.Vector3(0, 0.6, 2)); W.pulse.intensity = 40; } else W.pulse.intensity = 0;

  /* camera: follows the last laid block, then pulls back over the whole chain */
  const top = kind === "landscape" ? 330 : kind === "square" ? 450 : 560, bottom = kind === "vertical" ? 260 : 150;
  const areaH = h - top - bottom, cyArea = top + areaH / 2, tan = Math.tan((FOV * Math.PI) / 360);
  const whole = Math.max((W.L.ex * h) / (w * tan), (W.L.ey * h) / (areaH * tan)) * 1.05;
  const back = eo(cl((t - 4.2) / 0.9));
  const cur = W.blocks[Math.round(cl(built - 0.6, 0, n - 1))].home;
  const near = whole * 0.8;                 /* close on the growing end of the chain, then the whole chain */
  const follow = 0.45 * (1 - back);
  const tgt = new THREE.Vector3(cur.x * follow, 0, cur.z * follow);   /* follow sideways only: the chain stays under the title */
  const d = near + (whole - near) * back, yaw = -0.42 + 0.26 * back + Math.sin(t * 0.7) * 0.04;
  const shiftY = (cyArea - h / 2) * unitsPerPx(FOV, d, h);
  const eye = new THREE.Vector3(tgt.x + Math.sin(yaw) * d, tgt.y + 1.0 + shiftY, tgt.z + Math.cos(yaw) * d);
  const cam = makeCamera(FOV, w, h, eye, tgt.clone().add(new THREE.Vector3(0, shiftY, 0)));

  return (
    <AbsoluteFill>
      <Stage root={W.root} cam={cam} />
      <div style={{ position: "absolute", left: size.pad, right: size.pad, top: kind === "landscape" ? 90 : 100, textAlign: kind === "landscape" ? "left" : "center" }}>
        <Rise at={0.2}>
          <Kicker size={size.kicker}>{tx.d_kicker}</Kicker>
          <Title size={kind === "landscape" ? 84 : size.title} style={{ marginTop: 16 }}>{tx.d_title}</Title>
          <div style={{ fontFamily: FONT.body, fontSize: kind === "landscape" ? 30 : size.text * 0.8, color: C.muted, marginTop: 16 }}>{tx.d_text}</div>
        </Rise>
      </div>
      <div
        style={{
          position: "absolute", left: 0, right: 0, bottom: kind === "vertical" ? 150 : kind === "square" ? 70 : 60, textAlign: "center",
          fontFamily: FONT.mono, fontWeight: 500, fontSize: size.label * 1.15, color: C.gold,
          opacity: interpolate(frame, [6.0 * fps, 6.4 * fps], [0, 1], { ...clampOpts, easing: eo }),
        }}
      >
        ✓ tenon audit verify · {tx.d_ok}
      </div>
      <Dip durationInFrames={durationInFrames} />
    </AbsoluteFill>
  );
};
