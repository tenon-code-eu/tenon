/* tenon · héros 3D : le bouclier hexagonal de mark.svg en relief, ses pistes allumées, et les
 * douze étoiles de l'Union européenne en cercle. Les étoiles s'assemblent d'abord en CSS 3D sur
 * l'image fixe (aucun script nécessaire) ; la scène se cale exactement sur cette image, la
 * remplace en fondu, puis prend vie : le bouclier pivote, une onde parcourt les étoiles,
 * léger parallaxe à la souris. En mouvement réduit : une image fixe. */
export function mount(fig, THREE, C) {
  const canvas = fig.querySelector("canvas");
  const col = C.colors(THREE);
  const scene = new THREE.Scene();
  const camera = new THREE.PerspectiveCamera(30, 1, 0.1, 100);
  const DIST = 20;
  camera.position.set(0, 0, DIST);

  scene.add(new THREE.AmbientLight(0xffffff, 0.9));
  const key = new THREE.DirectionalLight(0xffffff, 2.4); key.position.set(4, 7, 10); scene.add(key);
  const gold = new THREE.PointLight(col.gold, 30, 30); gold.position.set(-5, -3, 6); scene.add(gold);
  const rim = new THREE.PointLight(col.markTrace, 40, 30); rim.position.set(6, 5, -4); scene.add(rim);

  const root = new THREE.Group(); scene.add(root);          /* placé et mis à l'échelle selon la mise en page */
  const tilt = new THREE.Group(); root.add(tilt);           /* parallaxe */

  /* ── Bouclier ─────────────────────────────────────────────── */
  const U = 0.1, DEPTH = 0.7, BEV = 0.12, FRONT = DEPTH / 2 + BEV;
  const shield = new THREE.Group(); tilt.add(shield);
  const body = new THREE.ExtrudeGeometry(C.hexShape(THREE, U), { depth: DEPTH, bevelEnabled: true, bevelThickness: BEV, bevelSize: 0.1, bevelSegments: 3 });
  body.translate(0, 0, -DEPTH / 2);
  { /* dégradé vertical du logo : #1f4bb0 en haut, #0c1f52 en bas */
    const p = body.attributes.position, c = new Float32Array(p.count * 3), tmp = new THREE.Color();
    for (let i = 0; i < p.count; i++) { tmp.copy(col.markBottom).lerp(col.markTop, C.clamp((p.getY(i) + 3) / 6, 0, 1)); c.set([tmp.r, tmp.g, tmp.b], i * 3); }
    body.setAttribute("color", new THREE.BufferAttribute(c, 3));
  }
  shield.add(new THREE.Mesh(body, new THREE.MeshStandardMaterial({ vertexColors: true, metalness: 0.35, roughness: 0.42 })));

  const glow = new THREE.MeshStandardMaterial({ color: col.markTrace, emissive: col.markTrace, emissiveIntensity: 0, metalness: 0.1, roughness: 0.35 });
  const ring = new THREE.Mesh(C.hexRing(THREE, U, 2, 0.1), glow);
  ring.position.z = FRONT + 0.02; shield.add(ring);

  /* Pistes : les segments de mark.svg (moitié gauche + miroir, puis le tronc central). */
  const LEFT = [
    [[30, 21.5], [21.5, 21.5], [18.5, 18.5]], [[29, 26], [18.5, 26]], [[28, 30.5], [23, 30.5], [23, 42.5], [27, 46.5]],
    [[19.5, 31.5], [19.5, 44], [25, 49.5]], [[15.5, 33], [15.5, 42.5]],
  ];
  const LINES = LEFT.concat(LEFT.map((l) => l.map(([x, y]) => [64 - x, y])), [
    [[21, 14.5], [32, 8.6], [43, 14.5]], [[24.5, 16.5], [32, 22.5], [39.5, 16.5]], [[32, 22.5], [32, 32]], [[32, 40.5], [32, 56]],
  ]);
  const segs = [];
  LINES.forEach((l) => { for (let i = 1; i < l.length; i++) segs.push([l[i - 1], l[i]]); });
  const W = 1.9 * U, unit = new THREE.BoxGeometry(1, W, 0.08);
  const traces = new THREE.InstancedMesh(unit, glow, segs.length);
  const m4 = new THREE.Matrix4(), q = new THREE.Quaternion(), zAxis = new THREE.Vector3(0, 0, 1);
  const P = ([x, y]) => new THREE.Vector3((x - 32) * U, -(y - 32) * U, FRONT + 0.05);
  segs.forEach(([a, b], i) => {
    const A = P(a), B = P(b), len = A.distanceTo(B) + W;
    q.setFromAxisAngle(zAxis, Math.atan2(B.y - A.y, B.x - A.x));
    m4.compose(A.clone().add(B).multiplyScalar(0.5), q, new THREE.Vector3(len, 1, 1));
    traces.setMatrixAt(i, m4);
  });
  shield.add(traces);

  /* Nœuds : anneaux autour des points de soudure, et le nœud central (capsule). */
  const dark = new THREE.MeshStandardMaterial({ color: col.markBottom, metalness: 0.2, roughness: 0.5 });
  [[17.3, 17.3, 1.7], [46.7, 17.3, 1.7], [17, 26, 1.7], [47, 26, 1.7], [23, 28.8, 1.5], [41, 28.8, 1.5]].forEach(([x, y, r]) => {
    const o = new THREE.Mesh(new THREE.TorusGeometry(r * U, 0.06, 8, 24), glow); o.position.copy(P([x, y])); shield.add(o);
    const d = new THREE.Mesh(new THREE.CircleGeometry(r * U, 20), dark); d.position.copy(P([x, y])); d.position.z += 0.01; shield.add(d);
  });
  { const cap = new THREE.Shape(), x0 = (28.6 - 32) * U, y0 = -(32 - 32) * U, w = 6.8 * U, h = 8.5 * U, r = 3.4 * U;
    cap.absarc(x0 + r, y0 - r, r, Math.PI, 0, true); cap.lineTo(x0 + w, y0 - h + r);
    cap.absarc(x0 + r, y0 - h + r, r, 0, Math.PI, true); cap.closePath();
    const m = new THREE.Mesh(new THREE.ExtrudeGeometry(cap, { depth: 0.14, bevelEnabled: false }), dark); m.position.z = FRONT; shield.add(m);
    const line = new THREE.LineLoop(new THREE.BufferGeometry().setFromPoints(cap.getPoints(24)), new THREE.LineBasicMaterial({ color: col.markNode }));
    line.position.z = FRONT + 0.15; shield.add(line);
    const dot = new THREE.Mesh(new THREE.CircleGeometry(1.5 * U, 20), new THREE.MeshBasicMaterial({ color: col.markNode }));
    dot.position.copy(P([32, 36.25])); dot.position.z = FRONT + 0.16; shield.add(dot); }

  /* ── Les douze étoiles ────────────────────────────────────── */
  const RING_R = 4.6;
  const starGeo = C.starGeometry(THREE, 0.34, 0.1);
  const starMat = new THREE.MeshStandardMaterial({ color: col.gold, emissive: col.gold, emissiveIntensity: 0.35, metalness: 0.55, roughness: 0.3 });
  const rnd = (() => { let s = 7; return () => (s = (s * 16807) % 2147483647) / 2147483647; })();   /* aléa stable */
  const stars = [];
  for (let i = 0; i < 12; i++) {
    const a = Math.PI / 2 - i * Math.PI / 6, m = new THREE.Mesh(starGeo, starMat);
    m.position.set(Math.cos(a) * RING_R, Math.sin(a) * RING_R, 0.25 * Math.sin(i * 1.7));   /* légère profondeur */
    stars.push(m); tilt.add(m);
  }

  /* Poussière d'étoiles, hors du groupe : elle donne la profondeur au parallaxe. */
  const N = 240, dp = new Float32Array(N * 3);
  for (let i = 0; i < N; i++) dp.set([(rnd() - 0.5) * 40, (rnd() - 0.5) * 22, -14 + rnd() * 14], i * 3);
  const dustGeo = new THREE.BufferGeometry(); dustGeo.setAttribute("position", new THREE.BufferAttribute(dp, 3));
  const dust = new THREE.Points(dustGeo, new THREE.PointsMaterial({ color: col.muted, size: 0.09, map: C.glowTexture(THREE), transparent: true, opacity: 0.5, depthWrite: false }));
  scene.add(dust);

  /* ── Mise en page : à droite du titre sur grand écran, au-dessus sur mobile ── */
  /* La scène se cale sur l'image fixe (.h-hero__still) : même centre, même rayon du cercle
   * (39/92 du cadre), pour un fondu sans saut. */
  const still = fig.querySelector(".h-hero__still");
  let wide = true;
  function layout(w, h) {
    camera.aspect = w / h; camera.updateProjectionMatrix();
    const k = 2 * DIST * Math.tan(camera.fov * Math.PI / 360) / h;   /* unités monde par pixel à z = 0 */
    wide = w >= 960;
    const c = canvas.getBoundingClientRect(), r = still.getBoundingClientRect();
    const cx = r.left - c.left + r.width / 2, cy = r.top - c.top + r.height / 2;
    root.scale.setScalar(r.width * 39 / 92 * k / RING_R);
    root.position.set((cx - w / 2) * k, -(cy - h / 2) * k, 0);
  }

  /* ── Parallaxe ──────────────────────────────────────────── */
  let tx = 0, ty = 0, px = 0, py = 0;
  const hero = fig.closest(".h-hero") || fig;
  if (!C.reduce && window.matchMedia("(pointer: fine)").matches) {
    hero.addEventListener("pointermove", (e) => {
      tx = (e.clientX / window.innerWidth) * 2 - 1; ty = (e.clientY / window.innerHeight) * 2 - 1; st.request();
    }, { passive: true });
    hero.addEventListener("pointerleave", () => { tx = 0; ty = 0; st.request(); });
  }

  let t = C.reduce ? 10 : 0, shown = false;
  function render(dt) {
    t += dt;
    const life = C.easeIO(t / 1.8);                                /* 0 → 1 : la scène prend vie */
    glow.emissiveIntensity = C.lerp(0.18, 0.42, life) + (C.reduce ? 0 : 0.14 * life * Math.sin(t * 1.7));
    /* une onde d'or fait le tour des étoiles, puis elles scintillent doucement */
    stars.forEach((m, i) => {
      const wave = C.reduce ? 0 : Math.exp(-Math.pow((t - 0.4 - i * 0.09) / 0.16, 2));
      m.scale.setScalar(1 + 0.45 * wave + (C.reduce ? 0 : 0.06 * Math.sin(t * 2.2 + i) * life));
      m.rotation.y = wave * Math.PI;
    });
    /* parallaxe amorti et légère respiration, à partir de la vue de face de l'image fixe */
    px = C.damp(px, tx, 0.35, dt); py = C.damp(py, ty, 0.35, dt);
    const idle = C.reduce ? 0 : Math.sin(t * 0.45) * 0.06 * life;
    tilt.rotation.y = life * (wide ? -0.24 : -0.12) + px * 0.32 + idle; tilt.rotation.x = life * 0.06 + py * 0.2 + idle * 0.4;
    dust.position.x = -px * 0.8; dust.position.y = py * 0.5;
    dust.rotation.z = C.reduce ? 0 : t * 0.004;
    st.renderer.render(scene, camera);
    if (!shown) { shown = true; fig.classList.add("is-3d"); }
    return true;   /* scène vivante : la boucle s'arrête d'elle-même hors écran ou onglet caché */
  }

  const st = C.stage(THREE, canvas, { render, resize: layout, lost: () => fig.classList.remove("is-3d") });
  st.request();
}
