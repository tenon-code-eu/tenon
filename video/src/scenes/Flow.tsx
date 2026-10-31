// Scene C: "Your data stays in Europe." Your project, three shield-shaped gates (masking, approval,
// audit log), then the local model, EU providers, and outside the EU stopped by a barrier.
// Same geometry as the site (docs/assets/site/3d/flow.js); horizontal, or vertical in 9:16.
import React, { useMemo } from "react";
import * as THREE from "three";
import { AbsoluteFill, useCurrentFrame, useVideoConfig } from "remotion";
import { T, type Lang } from "../i18n";
import { C, FONT, color3 } from "../theme";
import { glowTexture, hexRing, seeded, starGeometry } from "../three/shapes";
import { Stage, makeCamera, project, unitsPerPx } from "../three/Stage";
import { Dip, Kicker, Pin, Rise, Title, eo, useFormat } from "../ui";

const A = { project: -8, gates: [-4.6, -2.2, 0.2], split: 2.4, dest: 7.2 };
const B = { local: 2.8, eu: 0, out: -2.8 };
const BAR_T = 0.5, N = 320, FOV = 35;
const cl = (x: number, a = 0, b = 1) => Math.max(a, Math.min(b, x));
const sm = (a: number, b: number, t: number) => { const x = cl((t - a) / (b - a)); return x * x * (3 - 2 * x); };
const ease = (x: number) => eo(cl(x));

function build(vertical: boolean, square: boolean) {
  const P = (a: number, b: number) => (vertical ? new THREE.Vector3(b * 0.62, -a * 0.95, 0) : new THREE.Vector3(a, b, 0));
  const DA = (k: string) => (vertical ? (k === "eu" ? A.dest + 2.6 : A.dest - 1.0) : A.dest);
  const DS = vertical ? 0.78 : 1;
  const root = new THREE.Group();
  root.add(new THREE.AmbientLight(0xffffff, 1.1));
  const key = new THREE.DirectionalLight(0xffffff, 2.2);
  key.position.set(3, 6, 8);
  root.add(key);
  const pl = new THREE.PointLight(color3("gold"), 25, 30);
  pl.position.set(0, 3, 4);
  root.add(pl);
  const std = (c: THREE.Color, rough = 0.5, metal = 0.2) => new THREE.MeshStandardMaterial({ color: c, roughness: rough, metalness: metal });
  const edges = (geo: THREE.BufferGeometry, mat: THREE.Material, ec: THREE.Color) => {
    const g = new THREE.Group();
    g.add(new THREE.Mesh(geo, mat), new THREE.LineSegments(new THREE.EdgesGeometry(geo), new THREE.LineBasicMaterial({ color: ec })));
    return g;
  };
  const gold = new THREE.MeshStandardMaterial({ color: color3("gold"), emissive: color3("gold"), emissiveIntensity: 0.4, roughness: 0.3, metalness: 0.5 });

  const project_ = new THREE.Group();
  for (let i = 0; i < 4; i++) { const s = edges(new THREE.BoxGeometry(2, 0.18, 1.4), std(color3("rail"), 0.6, 0.05), color3("edge")); s.position.set((i % 2) * 0.08, -0.55 + i * 0.34, 0); project_.add(s); }
  [0.9, 1.3, 0.7].forEach((wd, i) => { const l = new THREE.Mesh(new THREE.BoxGeometry(wd, 0.03, 0.08), gold); l.position.set(-0.85 + wd / 2 + 0.05, 0.56, -0.35 + i * 0.3); project_.add(l); });
  project_.position.copy(P(A.project, 0));
  project_.rotation.set(0.35, vertical ? 0.4 : -0.5, 0);
  root.add(project_);

  const ring = vertical ? hexRing(0.045, 3.4, 0.16) : hexRing(0.055, 3.2, 0.18);
  const gates = A.gates.map((a) => {
    const mat = new THREE.MeshStandardMaterial({ color: color3("edge"), emissive: color3("gold"), emissiveIntensity: 0, roughness: 0.35, metalness: 0.4 });
    const g = new THREE.Mesh(ring, mat);
    g.position.copy(P(a, 0));
    g.rotation.set(vertical ? Math.PI / 2 : 0, vertical ? 0 : Math.PI / 2, 0);
    root.add(g);
    return { g, mat };
  });

  const local = edges(new THREE.BoxGeometry(1.7, 1.1, 1.3), std(color3("post")), color3("edge"));
  const screen = new THREE.Mesh(new THREE.PlaneGeometry(1.3, 0.7), new THREE.MeshBasicMaterial({ color: color3("night") }));
  screen.position.set(0, 0.05, 0.651);
  local.add(screen);
  local.position.copy(P(DA("local"), B.local));
  local.rotation.set(0.25, vertical ? 0.3 : -0.45, 0);
  const eu = new THREE.Group();
  const disc = edges(new THREE.CylinderGeometry(1, 1, 0.16, 48), std(color3("accent"), 0.4, 0.3), color3("edge"));
  disc.rotation.x = Math.PI / 2;
  eu.add(disc);
  const euStars = new THREE.Group();
  const sg = starGeometry(0.15, 0.05);
  for (let i = 0; i < 12; i++) { const a = Math.PI / 2 - (i * Math.PI) / 6, s = new THREE.Mesh(sg, gold); s.position.set(Math.cos(a) * 1.35, Math.sin(a) * 1.35, 0.1); euStars.add(s); }
  eu.add(euStars);
  eu.position.copy(P(DA("eu"), B.eu));
  eu.rotation.y = vertical ? 0 : -0.35;
  const out = edges(new THREE.BoxGeometry(1.5, 1.5, 1.5), std(color3("night2"), 0.6, 0.1), color3("rule"));
  out.position.copy(P(DA("out"), B.out));
  out.rotation.set(0.25, vertical ? 0.3 : -0.45, 0);
  [local, eu, out].forEach((o) => o.scale.setScalar(DS));
  root.add(local, eu, out);

  const S = P(A.split, 0);
  const trunk = new THREE.LineCurve3(P(A.project + 1.2, 0), S);
  const branches = ([["local", B.local], ["eu", B.eu], ["out", B.out]] as const).map(([k, b], i) =>
    new THREE.QuadraticBezierCurve3(S, P(A.split + 2.4, b), P(DA(k) - (i === 1 ? 1.4 : 1.1) * DS, b)));
  const tubeMat = new THREE.MeshBasicMaterial({ color: color3("edge"), transparent: true, opacity: 0.45 });
  const tubeOut = new THREE.MeshBasicMaterial({ color: color3("danger"), transparent: true, opacity: 0.5 });
  const tubes = [trunk, ...branches].map((c, i) => { const m = new THREE.Mesh(new THREE.TubeGeometry(c, 64, 0.035, 6), i === 3 ? tubeOut : tubeMat); root.add(m); return m; });

  const barrierMat = new THREE.MeshBasicMaterial({ color: color3("danger"), transparent: true, opacity: 0.22, side: THREE.DoubleSide, depthWrite: false });
  const barrier = new THREE.Group();
  const bGeo = new THREE.BoxGeometry(0.06, 2.2, 2.2);
  barrier.add(new THREE.Mesh(bGeo, barrierMat), new THREE.LineSegments(new THREE.EdgesGeometry(bGeo), new THREE.LineBasicMaterial({ color: color3("danger") })));
  const bt = branches[2].getTangent(BAR_T);
  barrier.position.copy(branches[2].getPoint(BAR_T));
  barrier.rotation.set(0, 0, Math.atan2(bt.y, bt.x));
  root.add(barrier);

  const r = seeded(11);
  const PT = Array.from({ length: N }, () => { const x = r(); return { route: x < 0.45 ? 0 : x < 0.9 ? 1 : 2, phase: r(), jit: (r() - 0.5) * 0.18, jit2: (r() - 0.5) * 0.18 }; });
  const pos = new Float32Array(N * 3), col = new Float32Array(N * 3);
  const pGeo = new THREE.BufferGeometry();
  pGeo.setAttribute("position", new THREE.BufferAttribute(pos, 3));
  pGeo.setAttribute("color", new THREE.BufferAttribute(col, 3));
  const points = new THREE.Points(pGeo, new THREE.PointsMaterial({ size: 0.5, map: glowTexture(), vertexColors: true, transparent: true, depthWrite: false, blending: THREE.AdditiveBlending }));
  points.frustumCulled = false;
  root.add(points);

  const anchors = {
    project: P(A.project, 0).add(new THREE.Vector3(vertical ? 1.45 : 0, vertical ? 0 : -1.2, 0)),
    mask: P(A.gates[0], 0).add(new THREE.Vector3(vertical ? 1.3 : 0, vertical ? 0 : 1.85, 0)),
    perm: P(A.gates[1], 0).add(new THREE.Vector3(vertical ? 1.3 : 0, vertical ? 0 : -1.9, 0)),
    log: P(A.gates[2], 0).add(new THREE.Vector3(vertical ? 1.3 : 0, vertical ? 0 : 1.85, 0)),
    local: P(DA("local"), B.local).add(new THREE.Vector3(vertical ? -0.4 : 0, vertical ? -0.95 : 1.0, 0)),
    eu: P(DA("eu"), B.eu).add(new THREE.Vector3(vertical ? 0 : 1.95, vertical ? -1.35 : 0, 0)),
    out: P(DA("out"), B.out).add(new THREE.Vector3(vertical ? 0.4 : 0, vertical ? -0.95 : -1.05, 0)),
  };
  return { root, P, project_, gates, local, eu, out, euStars, trunk, branches, tubes, barrier, barrierMat, PT, pos, col, pGeo, anchors, DS };
}

export const Flow: React.FC<{ lang: Lang; durationInFrames: number }> = ({ lang, durationInFrames }) => {
  const frame = useCurrentFrame();
  const { fps } = useVideoConfig();
  const { w, h, kind, size } = useFormat();
  const vertical = kind === "vertical";
  const square = kind === "square";
  const W = useMemo(() => build(vertical, square), [vertical, square]);
  const tx = T(lang), t = frame / fps;
  const p = sm(0.3, 5.6, t);                                          /* the story, 0 → 1 */

  /* the flow: project appears, front advances, gates light up, branches, barrier */
  const appear = ease(sm(0, 0.1, p));
  W.project_.scale.setScalar(0.6 + 0.4 * appear);
  W.project_.rotation.y = (vertical ? 0.4 : -0.5) + t * 0.15;
  const front = sm(0.08, 0.62, p), tFront = cl(front / 0.5), bFront = cl((front - 0.5) / 0.5);
  const goldC = color3("gold"), edgeC = color3("edge"), dangerC = color3("danger");
  W.gates.forEach((g, i) => {
    const at = (A.gates[i] - (A.project + 1.2)) / (A.split - (A.project + 1.2));
    const lit = sm(at, at + 0.06, tFront);
    g.mat.emissiveIntensity = lit * (0.55 + 0.15 * Math.sin(t * 3 + i));
    g.mat.color.copy(edgeC).lerp(goldC, lit);
  });
  const ti = W.tubes[0].geometry.index!.count;
  W.tubes[0].geometry.setDrawRange(0, Math.floor((ti * tFront) / 6) * 6);
  for (let i = 1; i < 4; i++) { const c = W.tubes[i].geometry.index!.count; W.tubes[i].geometry.setDrawRange(0, Math.floor((c * bFront) / 6) * 6); }
  const dests = ease(sm(0.42, 0.6, p));
  [W.local, W.eu, W.out].forEach((o) => o.scale.setScalar((0.4 + 0.6 * dests) * W.DS));
  W.euStars.rotation.z = -t * 0.3;
  const bar = ease(sm(0.55, 0.68, p));
  W.barrier.scale.set(1, Math.max(0.001, bar), Math.max(0.001, bar));
  W.barrierMat.opacity = 0.18 + 0.12 * bar + 0.06 * Math.sin(t * 4);
  const v = new THREE.Vector3();
  W.PT.forEach((q, i) => {
    const u = (q.phase + t * 0.13) % 1, s = u * 2;
    let vis = s <= front * 2 && appear > 0.5, c = goldC;
    if (s < 1) W.trunk.getPoint(s, v);
    else {
      let bs = s - 1;
      if (q.route === 2) {
        if (bs > BAR_T) { vis = vis && bs < BAR_T + 0.12 && bar > 0.5; bs = BAR_T - (bs - BAR_T) * 0.3; c = dangerC; }
        else if (bs > BAR_T * 0.6) c = dangerC;
      }
      W.branches[q.route].getPoint(bs, v);
    }
    if (!vis) v.set(1e4, 1e4, 1e4);
    else { v.x += vertical ? q.jit : 0; v.y += vertical ? 0 : q.jit; v.z += q.jit2; }
    W.pos.set([v.x, v.y, v.z], i * 3);
    W.col.set([c.r, c.g, c.b], i * 3);
  });
  W.pGeo.attributes.position.needsUpdate = true;
  W.pGeo.attributes.color.needsUpdate = true;

  /* camera: from the project to the whole route, framed in the area under the title */
  const top = kind === "landscape" ? 260 : kind === "square" ? 300 : 430, bottom = kind === "vertical" ? 120 : 60;
  const areaH = h - top - bottom, cyArea = top + areaH / 2;
  const tan = Math.tan((FOV * Math.PI) / 360);
  const ex = vertical ? 3.5 : square ? 13.8 : 11.8, ey = vertical ? 11.6 : 5.6;
  /* distance at which ±ex fits the width and ±ey fits the area under the title */
  const dEnd = Math.max((ex * h) / (w * tan), (ey * h) / (areaH * tan)) * 1.02;
  const k = ease(cl(p / 0.7));
  const aEnd = vertical ? 1.3 : 0.6;
  const target = W.P(A.project + 3 + k * (aEnd - (A.project + 3)), 0);
  if (vertical) target.x = -0.1;
  const d = dEnd * (0.62 + 0.38 * k);
  const yaw = vertical ? 0.25 - 0.15 * k : -0.55 + 0.27 * k, pitch = vertical ? 0.3 - 0.2 * k : 0.42 - 0.2 * k;
  const shiftY = (cyArea - h / 2) * unitsPerPx(FOV, d, h);
  const eye = new THREE.Vector3(target.x + d * Math.sin(yaw) * Math.cos(pitch), target.y + d * Math.sin(pitch) + shiftY, target.z + d * Math.cos(yaw) * Math.cos(pitch));
  const cam = makeCamera(FOV, w, h, eye, target.clone().add(new THREE.Vector3(0, shiftY, 0)));

  const show: Record<string, number> = { project: 0.02, mask: 0.15, perm: 0.25, log: 0.35, local: 0.55, eu: 0.55, out: 0.68 };
  const L = (key: keyof typeof W.anchors, label: readonly string[], color: string, anchor: string, align: "center" | "left" = "center") => {
    const s = project(W.anchors[key], cam, w, h);
    return (
      <Pin key={key} x={s.x} y={s.y} anchor={anchor} align={align} opacity={sm(show[key], show[key] + 0.08, p)}>
        <div style={{ fontFamily: FONT.mono, fontWeight: 500, fontSize: square ? 24 : size.label, color }}>{label[0]}</div>
        {/* square: the gates and EU labels keep their name only, the frame is too tight for both lines */}
        {!(square && ["mask", "perm", "log", "eu"].includes(key)) && (
          <div style={{ fontFamily: FONT.body, fontSize: square ? 18 : size.meta, color: key === "out" ? C.danger : C.muted, marginTop: 4, whiteSpace: vertical ? "normal" : "nowrap", width: vertical ? 250 : undefined }}>{label[1]}</div>
        )}
      </Pin>
    );
  };
  const side = vertical ? "0% -50%" : "-50% -100%";
  return (
    <AbsoluteFill>
      <Stage root={W.root} cam={cam}>
        {L("project", tx.c_project, C.on, vertical ? side : "-50% 0%", vertical ? "left" : "center")}
        {L("mask", tx.c_mask, C.on, side, vertical ? "left" : "center")}
        {L("perm", tx.c_perm, C.on, vertical ? side : "-50% 0%", vertical ? "left" : "center")}
        {L("log", tx.c_log, C.on, side, vertical ? "left" : "center")}
        {L("local", tx.c_local, C.on, vertical ? "0% 0%" : "-50% -100%", vertical ? "left" : "center")}
        {L("eu", tx.c_eu, C.gold, vertical ? "-50% 0%" : "0% -50%", vertical ? "center" : "left")}
        {L("out", tx.c_out, C.danger, vertical ? "-100% 0%" : "-50% 0%", vertical ? "left" : "center")}
      </Stage>
      <div style={{ position: "absolute", left: size.pad, right: size.pad, top: kind === "landscape" ? 80 : 100, textAlign: "center" }}>
        <Rise at={0.3}>
          <Kicker size={size.kicker}>{tx.c_kicker}</Kicker>
          <Title size={kind === "landscape" ? 78 : size.title} style={{ marginTop: 16 }}>{tx.c_title}</Title>
        </Rise>
      </div>
      <Dip durationInFrames={durationInFrames} />
    </AbsoluteFill>
  );
};
