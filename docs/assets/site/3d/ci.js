/* tenon · « Un agent de code dans votre CI » : l'animation d'un pipeline, en boucle.
 * Une merge request glisse sur un rail et traverse quatre stations : installer (sceau minisign),
 * exécuter (git push refusé, code 3), prouver (la chaîne d'audit passe à l'or), archiver (trois
 * artefacts). Jouée quand elle est à l'écran ; en mouvement réduit, l'image finale, fixe. */
export function mount(fig, THREE, C) {
  fig.classList.add("is-3d");
  const canvas = fig.querySelector("canvas");
  const col = C.colors(THREE);

  const scene = new THREE.Scene();
  const camera = new THREE.PerspectiveCamera(32, 1, 0.1, 300);
  scene.add(new THREE.AmbientLight(0xffffff, 1.0));
  const key = new THREE.DirectionalLight(0xffffff, 2.2); key.position.set(4, 9, 7); scene.add(key);
  const goldL = new THREE.PointLight(col.gold, 0, 9); scene.add(goldL);

  const mat = (c, extra) => new THREE.MeshStandardMaterial(Object.assign({ color: c, roughness: 0.55, metalness: 0.15 }, extra || {}));
  const edged = (geo, m) => {
    const g = new THREE.Group(), e = new THREE.LineBasicMaterial({ color: col.edge, transparent: true, opacity: 0.9 });
    g.add(new THREE.Mesh(geo, m), new THREE.LineSegments(new THREE.EdgesGeometry(geo), e));
    g.userData.edge = e; return g;
  };

  /* ── Les quatre stations ─────────────────────────────────── */
  const SPAN = [-7.5, -2.5, 2.5, 7.5];
  const stations = SPAN.map(() => {
    const g = new THREE.Group(); scene.add(g);
    const base = edged(new THREE.BoxGeometry(3, 0.34, 3), mat(col.night2)); base.position.y = -0.17; g.add(base);
    const gateMat = mat(col.edge, { emissive: col.gold, emissiveIntensity: 0, metalness: 0.4, roughness: 0.35 });
    const gate = new THREE.Mesh(C.hexRing(THREE, 0.045, 3, 0.15), gateMat); gate.position.y = 1.35; g.add(gate);
    return { g, base, gateMat };
  });
  /* installer : le paquet et son sceau */
  const pkg = edged(new THREE.BoxGeometry(1, 1, 1), mat(col.rail)); pkg.position.set(0, 0.5, 0.55); stations[0].g.add(pkg);
  const sealMat = mat(col.edge, { emissive: col.gold, emissiveIntensity: 0, metalness: 0.5 });
  const seal = new THREE.Mesh(C.hexRing(THREE, 0.016, 6, 0.1), sealMat); seal.rotation.x = -Math.PI / 2; stations[0].g.add(seal);
  /* exécuter : le poste et la barrière de git push */
  const runner = edged(new THREE.BoxGeometry(1.1, 0.85, 0.95), mat(col.rail)); runner.position.set(0, 0.42, 0.55); stations[1].g.add(runner);
  const barMat = new THREE.MeshBasicMaterial({ color: col.danger, transparent: true, opacity: 0, side: THREE.DoubleSide, depthWrite: false });
  const barEdge = new THREE.LineBasicMaterial({ color: col.danger, transparent: true, opacity: 0 });
  const bGeo = new THREE.BoxGeometry(0.05, 1, 1), barrier = new THREE.Group();
  barrier.add(new THREE.Mesh(bGeo, barMat), new THREE.LineSegments(new THREE.EdgesGeometry(bGeo), barEdge));
  barrier.position.set(1.25, 0.5, 0.55); stations[1].g.add(barrier);
  /* prouver : cinq blocs chaînés */
  const proof = [0, 1, 2, 3, 4].map((i) => {
    const m = mat(col.night2, { emissive: col.gold, emissiveIntensity: 0 });
    const b = edged(new THREE.BoxGeometry(0.4, 0.4, 0.4), m); b.position.set(-1 + i * 0.5, 0.2, 0.85); stations[2].g.add(b);
    return { b, m };
  });
  /* archiver : la caisse et ses trois artefacts */
  const cm = mat(col.post);
  [[0, 0.05, 0, 1.5, 0.1, 1.2], [0, 0.4, 0.6, 1.5, 0.7, 0.06], [0, 0.4, -0.6, 1.5, 0.7, 0.06], [0.75, 0.4, 0, 0.06, 0.7, 1.2], [-0.75, 0.4, 0, 0.06, 0.7, 1.2]].forEach((b) => {
    const m = edged(new THREE.BoxGeometry(b[3], b[4], b[5]), cm); m.position.set(b[0], b[1], b[2] + 0.3); stations[3].g.add(m);
  });
  const tiles = [0, 1, 2].map(() => { const t = edged(new THREE.BoxGeometry(0.85, 0.08, 0.55), mat(col.rail)); stations[3].g.add(t); return t; });

  /* rail, pointillés dorés, merge request */
  const rail = new THREE.Mesh(new THREE.BoxGeometry(22, 0.1, 0.5), mat(col.post)); rail.position.y = -0.05; scene.add(rail);
  const dashMat = new THREE.MeshBasicMaterial({ color: col.gold }), dashes = [];
  for (let i = 0; i < 24; i++) { const d = new THREE.Mesh(new THREE.BoxGeometry(0.35, 0.02, 0.06), dashMat); scene.add(d); dashes.push(d); }
  const token = new THREE.Group();
  const tk = edged(new THREE.BoxGeometry(1, 0.22, 0.7), mat(col.accent, { emissive: col.accent, emissiveIntensity: 0.5 }));
  tk.userData.edge.color.copy(col.gold); token.add(tk);
  const glow = new THREE.Sprite(new THREE.SpriteMaterial({ map: C.glowTexture(THREE), color: col.gold, transparent: true, opacity: 0.55, depthWrite: false, blending: THREE.AdditiveBlending }));
  glow.scale.set(2.2, 2.2, 1); token.add(glow); scene.add(token);

  /* ── Mise en page : en ligne, ou en profondeur sur écran haut ── */
  let portrait = false;
  const at = (u, y, out) => (portrait ? out.set(0, y, u) : out.set(u, y, 0));
  function layout() {
    portrait = camera.aspect < 0.85;
    stations.forEach((s, i) => { at(SPAN[i], 0, s.g.position); s.g.rotation.y = portrait ? 0 : -Math.PI / 2; });
    rail.rotation.y = portrait ? Math.PI / 2 : 0;
    dashes.forEach((d, i) => { at(-11 + i * 0.95, 0.01, d.position); d.rotation.y = portrait ? Math.PI / 2 : 0; });
    token.rotation.y = portrait ? Math.PI / 2 : 0;
  }

  /* ── Boucle de 10 s : arrivée à la station i à ARR[i] ── */
  const D = 10, ARR = [1, 3.1, 5.2, 7.3], END = 9.2;
  const L = {};
  fig.querySelectorAll(".h-ci3d__labels [data-k]").forEach((el) => { L[el.getAttribute("data-k")] = el; });
  const tan = Math.tan(camera.fov * Math.PI / 360);
  let t = C.reduce ? END - 0.7 : 0;
  const v = new THREE.Vector3(), tmp = new THREE.Vector3();

  function render(dt) {
    if (!C.reduce) t = (t + dt) % D;
    const live = C.reduce || t < D - 0.5;                              /* tout s'éteint avant de reboucler */
    /* la merge request : glisse vers chaque station, s'y arrête, puis sort à droite */
    let u = -10.5;
    for (let i = 0; i < 4; i++) if (t >= ARR[i] - 0.8) u = C.lerp(i ? SPAN[i - 1] : -10.5, SPAN[i], C.easeIO((t - (ARR[i] - 0.8)) / 0.8));
    if (t >= END - 0.6) u = C.lerp(SPAN[3], 10.5, C.easeIO((t - (END - 0.6)) / 0.8));
    at(u, 0.33, token.position);
    token.visible = live && u < 10;

    const reached = (i) => t >= ARR[i] && live;
    stations.forEach((s, i) => {
      const on = reached(i), failed = i === 1 && t >= ARR[1] + 0.6 && live;
      s.base.userData.edge.color.copy(failed ? col.danger : on ? col.gold : col.edge);
      s.gateMat.color.copy(on ? col.gold : col.edge);
      s.gateMat.emissiveIntensity = on ? 0.5 : 0;
    });
    const stamp = live ? C.easeOut(C.clamp((t - ARR[0] - 0.2) / 0.5, 0, 1)) : 0;
    seal.position.set(0, C.lerp(3, 1.02, stamp), 0.55); seal.scale.setScalar(C.lerp(1.6, 1, stamp));
    const sealed = stamp > 0.98;
    sealMat.color.copy(sealed ? col.gold : col.edge); sealMat.emissiveIntensity = sealed ? 0.6 : 0;
    seal.visible = stamp > 0;
    pkg.userData.edge.color.copy(sealed ? col.gold : col.edge);
    const deny = live ? C.smooth(ARR[1] + 0.4, ARR[1] + 0.6, t) : 0;
    barMat.opacity = 0.25 * deny; barEdge.opacity = deny;
    proof.forEach((b, i) => {
      const on = live && t >= ARR[2] + 0.2 + i * 0.15;
      b.m.color.copy(on ? col.gold : col.night2); b.m.emissiveIntensity = on ? 0.5 : 0;
      b.b.userData.edge.color.copy(on ? col.gold : col.edge);
    });
    tiles.forEach((tile, i) => {
      const k = live ? C.easeOut(C.clamp((t - ARR[3] - 0.2 - i * 0.2) / 0.4, 0, 1)) : 0;
      tile.visible = k > 0; tile.position.set(0, C.lerp(3, 0.16 + i * 0.11, k), 0.3);
      tile.userData.edge.color.copy(k > 0.98 ? col.gold : col.edge);
    });
    let cur = -1; for (let i = 0; i < 4; i++) if (t >= ARR[i] && t < END) cur = i;
    if (cur >= 0 && live) { stations[cur].g.getWorldPosition(goldL.position); goldL.position.y += 2.4; goldL.intensity = 18; } else goldL.intensity = 0;

    /* caméra fixe, de trois quarts, avec une légère respiration */
    const asp = camera.aspect;
    const d = portrait ? Math.max(3.6 / (tan * asp), 11.5 / tan) : Math.max(12.2 / (tan * asp), 4.2 / tan) * 1.18;
    const sway = C.reduce ? 0 : Math.sin(t / D * Math.PI * 2) * 0.06;
    const yaw = (portrait ? 0.35 : -0.3) + sway, pitch = portrait ? 0.9 : 0.5;
    /* visée décalée : sur grand écran le pipeline se centre, sur mobile la colonne laisse la place aux libellés */
    const ox = portrait ? 2.3 : 1.2;
    camera.position.set(ox + d * Math.sin(yaw) * Math.cos(pitch), 0.6 + d * Math.sin(pitch), d * Math.cos(yaw) * Math.cos(pitch));
    camera.lookAt(ox, portrait ? 0.6 : 0.2, 0);
    camera.updateMatrixWorld();
    st.renderer.render(scene, camera);

    /* étiquettes : à côté de chaque station, la merge request, et le code 3 */
    const items = ["install", "run", "prove", "archive"].map((k, i) => {
      const p = new THREE.Vector3(); at(SPAN[i], 0, p);
      if (portrait) p.x += 1.8; else p.z += 2.3;
      L[k].classList.toggle("is-on", reached(i));
      return { el: L[k], at: p, anchor: portrait ? "translate(0, -50%)" : "translate(-50%, 0)" };
    });
    v.copy(token.position); v.y += 1;
    items.push({ el: L.mr, at: v.clone(), hide: !token.visible || u > 9.5 });
    at(SPAN[1], 2.4, tmp); items.push({ el: L.exit, at: tmp.clone(), hide: deny < 0.5 });
    C.placeLabels(THREE, camera, st.width, st.height, items);
    return true;
  }

  const st = C.stage(THREE, canvas, {
    render,
    resize(w, h) { camera.aspect = w / h; camera.updateProjectionMatrix(); layout(); },
    lost() { fig.classList.remove("is-3d"); },
  });
  st.request();
}
