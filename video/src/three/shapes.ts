// Shared 3D shapes, ported from the site (docs/assets/site/3d/core.js and hero.js):
// the tenon shield of mark.svg in relief, EU stars, the shield outline used as a gate.
import * as THREE from "three";
import { color3 } from "../theme";

export const HEX: [number, number][] = [[32, 2.5], [57.5, 17.2], [57.5, 46.8], [32, 61.5], [6.5, 46.8], [6.5, 17.2]];

export function hexShape(u: number, grow = 0) {
  const s = new THREE.Shape(), k = 1 + grow / 29.5;
  HEX.forEach(([x, y], i) => {
    const X = (x - 32) * u * k, Y = -(y - 32) * u * k;
    if (i === 0) s.moveTo(X, Y); else s.lineTo(X, Y);
  });
  s.closePath();
  return s;
}

export function hexRing(u: number, w: number, depth: number) {
  const outer = hexShape(u, w / 2), inner = hexShape(u, -w / 2);
  outer.holes.push(new THREE.Path(inner.getPoints().reverse()));
  const g = new THREE.ExtrudeGeometry(outer, { depth, bevelEnabled: false });
  g.translate(0, 0, -depth / 2);
  return g;
}

export function starGeometry(R: number, depth: number) {
  const s = new THREE.Shape(), r = R * 0.382;
  for (let i = 0; i < 10; i++) {
    const a = Math.PI / 2 + (i * Math.PI) / 5, rad = i % 2 ? r : R;
    if (i === 0) s.moveTo(Math.cos(a) * rad, Math.sin(a) * rad); else s.lineTo(Math.cos(a) * rad, Math.sin(a) * rad);
  }
  s.closePath();
  const g = new THREE.ExtrudeGeometry(s, { depth, bevelEnabled: true, bevelThickness: depth * 0.45, bevelSize: R * 0.06, bevelSegments: 2 });
  g.center();
  return g;
}

let glow: THREE.CanvasTexture | null = null;
export function glowTexture() {
  if (glow) return glow;
  const c = document.createElement("canvas");
  c.width = c.height = 64;
  const g = c.getContext("2d")!, grd = g.createRadialGradient(32, 32, 0, 32, 32, 32);
  grd.addColorStop(0, "rgba(255,255,255,1)");
  grd.addColorStop(0.25, "rgba(255,255,255,0.85)");
  grd.addColorStop(0.6, "rgba(255,255,255,0.18)");
  grd.addColorStop(1, "rgba(255,255,255,0)");
  g.fillStyle = grd;
  g.fillRect(0, 0, 64, 64);
  glow = new THREE.CanvasTexture(c);
  glow.colorSpace = THREE.SRGBColorSpace;
  return glow;
}

/* The shield: hexagon with the logo gradient, glowing traces, solder rings and the central node.
   Unit U = 0.1 world per SVG unit: about 5.1 × 5.9 world units. */
export function buildShield() {
  const U = 0.1, DEPTH = 0.7, BEV = 0.12, FRONT = DEPTH / 2 + BEV;
  const shield = new THREE.Group();
  const body = new THREE.ExtrudeGeometry(hexShape(U), { depth: DEPTH, bevelEnabled: true, bevelThickness: BEV, bevelSize: 0.1, bevelSegments: 3 });
  body.translate(0, 0, -DEPTH / 2);
  const p = body.attributes.position, cols = new Float32Array(p.count * 3), tmp = new THREE.Color();
  const top = color3("markTop"), bottom = color3("markBottom");
  for (let i = 0; i < p.count; i++) {
    tmp.copy(bottom).lerp(top, Math.max(0, Math.min(1, (p.getY(i) + 3) / 6)));
    cols.set([tmp.r, tmp.g, tmp.b], i * 3);
  }
  body.setAttribute("color", new THREE.BufferAttribute(cols, 3));
  shield.add(new THREE.Mesh(body, new THREE.MeshStandardMaterial({ vertexColors: true, metalness: 0.35, roughness: 0.42 })));

  const trace = color3("markTrace");
  const glowMat = new THREE.MeshStandardMaterial({ color: trace, emissive: trace, emissiveIntensity: 0.45, metalness: 0.1, roughness: 0.35 });
  const ring = new THREE.Mesh(hexRing(U, 2, 0.1), glowMat);
  ring.position.z = FRONT + 0.02;
  shield.add(ring);

  const LEFT: [number, number][][] = [
    [[30, 21.5], [21.5, 21.5], [18.5, 18.5]], [[29, 26], [18.5, 26]], [[28, 30.5], [23, 30.5], [23, 42.5], [27, 46.5]],
    [[19.5, 31.5], [19.5, 44], [25, 49.5]], [[15.5, 33], [15.5, 42.5]],
  ];
  const LINES = LEFT.concat(LEFT.map((l) => l.map(([x, y]) => [64 - x, y] as [number, number])), [
    [[21, 14.5], [32, 8.6], [43, 14.5]], [[24.5, 16.5], [32, 22.5], [39.5, 16.5]], [[32, 22.5], [32, 32]], [[32, 40.5], [32, 56]],
  ]);
  const segs: [[number, number], [number, number]][] = [];
  LINES.forEach((l) => { for (let i = 1; i < l.length; i++) segs.push([l[i - 1], l[i]]); });
  const W = 1.9 * U;
  const traces = new THREE.InstancedMesh(new THREE.BoxGeometry(1, W, 0.08), glowMat, segs.length);
  const m4 = new THREE.Matrix4(), q = new THREE.Quaternion(), zAxis = new THREE.Vector3(0, 0, 1);
  const P = ([x, y]: [number, number]) => new THREE.Vector3((x - 32) * U, -(y - 32) * U, FRONT + 0.05);
  segs.forEach(([a, b], i) => {
    const A = P(a), B = P(b);
    q.setFromAxisAngle(zAxis, Math.atan2(B.y - A.y, B.x - A.x));
    m4.compose(A.clone().add(B).multiplyScalar(0.5), q, new THREE.Vector3(A.distanceTo(B) + W, 1, 1));
    traces.setMatrixAt(i, m4);
  });
  shield.add(traces);

  const dark = new THREE.MeshStandardMaterial({ color: bottom, metalness: 0.2, roughness: 0.5 });
  ([[17.3, 17.3, 1.7], [46.7, 17.3, 1.7], [17, 26, 1.7], [47, 26, 1.7], [23, 28.8, 1.5], [41, 28.8, 1.5]] as const).forEach(([x, y, r]) => {
    const o = new THREE.Mesh(new THREE.TorusGeometry(r * U, 0.06, 8, 24), glowMat);
    o.position.copy(P([x, y]));
    shield.add(o);
    const d = new THREE.Mesh(new THREE.CircleGeometry(r * U, 20), dark);
    d.position.copy(P([x, y]));
    d.position.z += 0.01;
    shield.add(d);
  });
  const cap = new THREE.Shape(), x0 = (28.6 - 32) * U, w = 6.8 * U, h = 8.5 * U, r = 3.4 * U;
  cap.absarc(x0 + r, -r, r, Math.PI, 0, true);
  cap.lineTo(x0 + w, -h + r);
  cap.absarc(x0 + r, -h + r, r, 0, Math.PI, true);
  cap.closePath();
  const capMesh = new THREE.Mesh(new THREE.ExtrudeGeometry(cap, { depth: 0.14, bevelEnabled: false }), dark);
  capMesh.position.z = FRONT;
  shield.add(capMesh);
  const node = color3("markNode");
  const line = new THREE.LineLoop(new THREE.BufferGeometry().setFromPoints(cap.getPoints(24)), new THREE.LineBasicMaterial({ color: node }));
  line.position.z = FRONT + 0.15;
  shield.add(line);
  const dot = new THREE.Mesh(new THREE.CircleGeometry(1.5 * U, 20), new THREE.MeshBasicMaterial({ color: node }));
  dot.position.copy(P([32, 36.25]));
  dot.position.z = FRONT + 0.16;
  shield.add(dot);
  return { shield, glowMat };
}

/* Deterministic pseudo-random numbers (every frame renders the same). */
export const seeded = (seed: number) => {
  let s = seed;
  return () => (s = (s * 16807) % 2147483647) / 2147483647;
};
