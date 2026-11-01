/* tenon · « Un agent de code dans votre CI » : le pipeline en 3D, piloté par le défilement.
 * Une merge request glisse sur un rail et traverse quatre stations : installer (signature
 * minisign), exécuter (go test autorisé, git push refusé, code 3), prouver (audit verify, export),
 * archiver (trois artefacts). Le journal du job se remplit à côté. En mouvement réduit, la
 * scène ne se monte pas : l'animation HTML existante (live.js) reste, avec son image finale. */
export function mount(fig, THREE, C) {
  if (C.reduce) return;
  fig.classList.add("is-3d");
  const canvas = fig.querySelector("canvas");
  const track = fig.querySelector(".h-ci3d__track");
  const chapters = Array.prototype.slice.call(fig.querySelectorAll(".h-ci3d__chapters li"));
  const col = C.colors(THREE);

  /* journal du job : les lignes de l'animation HTML, révélées au fil du récit */
  const linesBox = fig.querySelector(".h-ci3d__lines");
  const src = document.querySelectorAll("#h-pipe .h-pipe__lines li");
  const LINES = Array.prototype.map.call(src, (li) => {
    const c = li.cloneNode(true);
    c.removeAttribute("data-at"); c.classList.remove("is-on");
    linesBox.appendChild(c);
    return { el: c, at: 0.1 + (parseFloat(li.getAttribute("data-at")) / 13.3) * 0.8 };
  });

  const scene = new THREE.Scene();
  const camera = new THREE.PerspectiveCamera(34, 1, 0.1, 300);
  scene.add(new THREE.AmbientLight(0xffffff, 1.0));
  const key = new THREE.DirectionalLight(0xffffff, 2.2); key.position.set(4, 9, 7); scene.add(key);
  const goldL = new THREE.PointLight(col.gold, 0, 10); scene.add(goldL);
  const world = new THREE.Group(); scene.add(world);

  const edged = (geo, mat, ec, op) => {
    const g = new THREE.Group(), e = new THREE.LineBasicMaterial({ color: ec || col.edge, transparent: true, opacity: op == null ? 0.9 : op });
    g.add(new THREE.Mesh(geo, mat), new THREE.LineSegments(new THREE.EdgesGeometry(geo), e));
    g.userData.edge = e; return g;
  };
  const lam = (c, extra) => new THREE.MeshStandardMaterial(Object.assign({ color: c, roughness: 0.55, metalness: 0.15 }, extra || {}));
  const mGold = lam(col.gold, { emissive: col.gold, emissiveIntensity: 0.4, metalness: 0.5, roughness: 0.3 });

  /* ── Stations ─────────────────────────────────────────────── */
  const KEYS = ["install", "run", "prove", "archive"];
  const stations = KEYS.map(() => {
    const g = new THREE.Group(); world.add(g);
    const base = edged(new THREE.BoxGeometry(3.2, 0.36, 3.2), lam(col.night2)); base.position.y = -0.18; g.add(base);
    const gateMat = lam(col.edge, { emissive: col.gold, emissiveIntensity: 0, metalness: 0.4, roughness: 0.35 });
    const gate = new THREE.Mesh(C.hexRing(THREE, 0.05, 3, 0.16), gateMat); gate.position.y = 1.5; g.add(gate);
    return { g, base, gate, gateMat };
  });
  /* 1 · installer : le paquet et le sceau minisign qui vient le tamponner */
  const pkg = edged(new THREE.BoxGeometry(1.1, 1.1, 1.1), lam(col.rail)); pkg.position.set(0, 0.55, 0.6); stations[0].g.add(pkg);
  const sealMat = lam(col.edge, { emissive: col.gold, emissiveIntensity: 0, metalness: 0.5 });
  const seal = new THREE.Mesh(C.hexRing(THREE, 0.018, 6, 0.12), sealMat); seal.rotation.x = -Math.PI / 2; stations[0].g.add(seal);
  /* 2 · exécuter : le poste, une voie autorisée (go test), une voie refusée (git push) */
  const runner = edged(new THREE.BoxGeometry(1.2, 0.9, 1.0), lam(col.rail)); runner.position.set(0, 0.45, 0.6); stations[1].g.add(runner);
  const barMat = new THREE.MeshBasicMaterial({ color: col.danger, transparent: true, opacity: 0, side: THREE.DoubleSide, depthWrite: false });
  const barrier = new THREE.Group();
  const bGeo = new THREE.BoxGeometry(0.05, 1.1, 1.1);
  const barEdge = new THREE.LineBasicMaterial({ color: col.danger, transparent: true, opacity: 0 });
  barrier.add(new THREE.Mesh(bGeo, barMat), new THREE.LineSegments(new THREE.EdgesGeometry(bGeo), barEdge));
  stations[1].g.add(barrier);
  /* 3 · prouver : une chaîne de cinq blocs qui passe à l'or, et le rapport qui se lève */
  const proofBlocks = [0, 1, 2, 3, 4].map((i) => {
    const m = lam(col.night2, { emissive: col.gold, emissiveIntensity: 0 });
    const b = edged(new THREE.BoxGeometry(0.42, 0.42, 0.42), m); b.position.set(-1 + i * 0.5, 0.21, 0.9); stations[2].g.add(b);
    return { b, m };
  });
  const report = edged(new THREE.BoxGeometry(1.0, 1.3, 0.05), lam(col.rail)); report.position.set(0, 0.65, -0.4); stations[2].g.add(report);
  /* 4 · archiver : la caisse ouverte et les trois artefacts qui y tombent */
  const crate = new THREE.Group();
  const cm = lam(col.post);
  [[0, 0.05, 0, 1.6, 0.1, 1.3], [0, 0.45, 0.65, 1.6, 0.8, 0.06], [0, 0.45, -0.65, 1.6, 0.8, 0.06], [0.8, 0.45, 0, 0.06, 0.8, 1.3], [-0.8, 0.45, 0, 0.06, 0.8, 1.3]].forEach((b) => {
    const m = edged(new THREE.BoxGeometry(b[3], b[4], b[5]), cm); m.position.set(b[0], b[1], b[2]); crate.add(m);
  });
  crate.position.z = 0.3; stations[3].g.add(crate);
  const tiles = [0, 1, 2].map((i) => { const t = edged(new THREE.BoxGeometry(0.9, 0.08, 0.6), lam(col.rail)); stations[3].g.add(t); return { t, i }; });

  /* rail et merge request */
  const railMat = lam(col.post), rail = new THREE.Mesh(new THREE.BoxGeometry(1, 0.1, 0.5), railMat); world.add(rail);
  const dashMat = new THREE.MeshBasicMaterial({ color: col.gold }), dashes = [];
  for (let i = 0; i < 30; i++) { const d = new THREE.Mesh(new THREE.BoxGeometry(0.35, 0.02, 0.06), dashMat); world.add(d); dashes.push(d); }
  const token = new THREE.Group();
  token.add(edged(new THREE.BoxGeometry(1.1, 0.22, 0.75), lam(col.accent, { emissive: col.accent, emissiveIntensity: 0.5 }), col.gold, 1));
  const glow = new THREE.Sprite(new THREE.SpriteMaterial({ map: C.glowTexture(THREE), color: col.gold, transparent: true, opacity: 0.55, depthWrite: false, blending: THREE.AdditiveBlending }));
  glow.scale.set(2.4, 2.4, 1); token.add(glow); world.add(token);
  /* paquets de la station « exécuter » */
  const pk = [0, 1].map((i) => { const s = new THREE.Sprite(new THREE.SpriteMaterial({ map: C.glowTexture(THREE), color: i ? col.danger : col.gold, transparent: true, depthWrite: false, blending: THREE.AdditiveBlending })); s.scale.set(1, 1, 1); world.add(s); return s; });

  /* ── Mise en page : en ligne (grand écran) ou en profondeur (écran haut) ── */
  let portrait = false;
  const SPAN = [-9, -3, 3, 9];
  const at = (u, y, out) => (portrait ? out.set(0, y, u) : out.set(u, y, 0));
  function layout() {
    portrait = camera.aspect < 0.85;
    /* les stations font face au trajet : la porte est traversée, la barrière reste sur le côté */
    stations.forEach((s, i) => { at(SPAN[i], 0, s.g.position); s.g.rotation.y = portrait ? 0 : -Math.PI / 2; });
    rail.scale.set(26, 1, 1); rail.rotation.y = portrait ? Math.PI / 2 : 0; rail.position.set(0, -0.05, 0);
    dashes.forEach((d, i) => { at(-14 + i * 0.95, 0.01, d.position); d.rotation.y = portrait ? Math.PI / 2 : 0; });
    barrier.position.set(1.35, 0.55, 0.6);
  }

  const tmp = new THREE.Vector3(), tmp2 = new THREE.Vector3();
  const L = {};
  fig.querySelectorAll(".h-ci3d__labels [data-k]").forEach((el) => { L[el.getAttribute("data-k")] = el; });
  const tan = Math.tan(camera.fov * Math.PI / 360);
  let prog = 0, chapter = -2, viewX = 0, viewY = 0, camT = null, camE = null, lastLines = -1;
  const CH = [0.1, 0.3, 0.5, 0.7, 0.9];

  function render(dt, time) {
    prog = C.damp(prog, C.pinProgress(track), 0.15, dt);
    const p = prog;
    let c = -1;
    for (let i = 0; i < 4; i++) if (p >= CH[i] && p < CH[i + 1]) c = i;
    if (p >= 0.9) c = 4;
    if (c !== chapter) { chapter = c; chapters.forEach((li, i) => li.classList.toggle("is-on", i === c)); }
    const local = (i) => C.clamp((p - CH[i]) / 0.2, 0, 1);       /* avancement dans le chapitre i */

    /* la merge request rejoint la station du chapitre en cours */
    const stop = c < 0 ? -1 : Math.min(3, c);
    const prevU = stop <= 0 ? -13 : SPAN[stop - 1], targetU = stop < 0 ? -13 : SPAN[stop];
    const move = stop < 0 ? 0 : C.easeIO(C.clamp(local(stop) / 0.3, 0, 1));
    const u = c === 4 ? SPAN[3] + 2.6 * C.easeIO(C.clamp((p - 0.9) / 0.06, 0, 1)) : C.lerp(prevU, targetU, stop < 0 ? 1 : move);
    at(u, 0.35 + Math.sin(time * 2) * 0.04, token.position);
    token.rotation.y = portrait ? Math.PI / 2 : 0;

    /* stations : portes, sceau, voies, preuves, artefacts */
    stations.forEach((s, i) => {
      const reached = stop > i || (stop === i && move > 0.95) || c === 4;
      const e = s.base.userData.edge;
      e.color.copy(i === 1 && reached && local(1) > 0.55 || i === 1 && stop > 1 ? col.danger : reached ? col.gold : col.edge);
      e.opacity = reached ? 1 : 0.6;
      s.gateMat.emissiveIntensity = C.damp(s.gateMat.emissiveIntensity, reached ? 0.55 : 0, 0.2, dt);
      s.gateMat.color.copy(reached ? col.gold : col.edge);
    });
    const l0 = local(0), stamp = C.easeOut(C.clamp((l0 - 0.35) / 0.3, 0, 1));
    seal.position.set(0, C.lerp(3.2, 1.12, stamp), 0.6); seal.scale.setScalar(C.lerp(1.6, 1, stamp));
    sealMat.color.copy(stamp > 0.98 ? col.gold : col.edge); sealMat.emissiveIntensity = stamp > 0.98 ? 0.6 : 0;
    pkg.userData.edge.color.copy(stamp > 0.98 ? col.gold : col.edge);

    const l1 = local(1), runOn = stop === 1 && move > 0.9;
    const deny = C.smooth(0.5, 0.62, l1) * (stop >= 1 ? 1 : 0);
    barMat.opacity = 0.25 * (stop > 1 || c === 4 ? 1 : deny); barEdge.opacity = stop > 1 || c === 4 ? 1 : deny;
    pk.forEach((s, i) => {
      s.visible = runOn;
      if (!runOn) return;
      const k = (time * 0.6 + i * 0.5) % 1;
      /* go test passe tout droit ; git push part sur le côté et bute sur la barrière */
      tmp.set(i ? Math.min(1.3, k * 2.2) : 0, 1.15, 0.6 + (i ? 0 : -k * 1.8));
      stations[1].g.localToWorld(tmp); s.position.copy(tmp);
      s.material.opacity = i && k * 2.2 > 1.3 ? Math.max(0, 1 - (k * 2.2 - 1.3) * 3) : 1;
    });

    const l2 = local(2);
    proofBlocks.forEach((b, i) => {
      const on = stop > 2 || c === 4 || (stop === 2 && l2 > 0.35 + i * 0.08);
      b.m.emissiveIntensity = on ? 0.5 : 0; b.m.color.copy(on ? col.gold : col.night2);
      b.b.userData.edge.color.copy(on ? col.gold : col.edge);
    });
    const rise = stop > 2 || c === 4 ? 1 : stop === 2 ? C.easeOut(C.clamp((l2 - 0.65) / 0.25, 0, 1)) : 0;
    report.position.y = C.lerp(-0.6, 0.75, rise); report.visible = rise > 0;
    report.userData.edge.color.copy(rise > 0.98 ? col.gold : col.edge);

    const l3 = stop > 3 || c === 4 ? 1 : stop === 3 ? local(3) : 0;
    tiles.forEach(({ t, i }) => {
      const k = C.easeOut(C.clamp((l3 - 0.3 - i * 0.12) / 0.25, 0, 1));
      t.visible = k > 0; t.position.set(0, C.lerp(3.4, 0.16 + i * 0.12, k), 0.3); t.rotation.z = (1 - k) * 0.8 * (i - 1);
      t.userData.edge.color.copy(k > 0.98 ? col.gold : col.edge);
    });
    if (stop >= 0 && c !== 4) { stations[stop].g.getWorldPosition(goldL.position); goldL.position.y += 2.5; goldL.intensity = 20; } else goldL.intensity = 0;

    /* caméra : elle suit la merge request, puis recule sur tout le pipeline */
    const fin = C.easeIO(C.clamp((p - 0.88) / 0.08, 0, 1)) || (p < 0.08 ? 1 - C.smooth(0.02, 0.1, p) : 0);
    const asp = camera.aspect;
    const whole = portrait ? Math.max(4.2 / (tan * asp), 13.5 / tan) * 1.05 : Math.max(13.5 / (tan * asp), 4.5 / tan) * 1.05;
    const follow = portrait ? Math.max(9, whole * 0.48) : Math.max(13, whole * 0.72);
    at(C.lerp(c < 0 ? 0 : u, 0, fin), 0.6, tmp);
    const d = C.lerp(follow, whole, fin);
    const yaw = portrait ? C.lerp(0.5, 0.25, fin) : C.lerp(-0.38, -0.2, fin), pitch = portrait ? C.lerp(0.55, 0.95, fin) : C.lerp(0.42, 0.55, fin);
    tmp2.set(tmp.x + d * Math.sin(yaw) * Math.cos(pitch), tmp.y + d * Math.sin(pitch), tmp.z + d * Math.cos(yaw) * Math.cos(pitch));
    if (!camT) { camT = tmp.clone(); camE = tmp2.clone(); }
    camT.lerp(tmp, 1 - Math.exp(-dt / 0.25)); camE.lerp(tmp2, 1 - Math.exp(-dt / 0.25));
    camera.position.copy(camE); camera.lookAt(camT);
    const CW = canvas.clientWidth, CHh = canvas.clientHeight, wide = CW >= 960;
    viewX = C.damp(viewX, wide ? (c < 0 ? 0.08 : 0.17) : 0, 0.35, dt); viewY = C.damp(viewY, wide ? 0 : 0.16, 0.35, dt);
    if (CW && CHh) camera.setViewOffset(CW, CHh, -viewX * CW, viewY * CHh, CW, CHh);
    camera.updateMatrixWorld();
    st.renderer.render(scene, camera);

    /* étiquettes */
    const items = KEYS.map((k, i) => {
      const v = new THREE.Vector3(); at(SPAN[i], 3.3, v);
      return { el: L[k], at: v, hide: !(stop === i || c === 4) };   /* la station en cours, ou toutes à la fin */
    });
    const tv = token.position.clone(); tv.y += 1.1;
    items.push({ el: L.mr, at: tv, hide: c < 0 || c === 4 });
    const ev = new THREE.Vector3(); at(SPAN[1], 0, ev); ev.add(new THREE.Vector3(portrait ? 1.6 : 0.6, 2.2, portrait ? 0.6 : -1.6));
    items.push({ el: L.exit, at: ev, hide: !(deny > 0.5 || stop > 1 || c === 4) });
    C.placeLabels(THREE, camera, st.width, st.height, items);

    /* journal du job */
    let n = 0; LINES.forEach((l) => { if (p >= l.at) n++; });
    if (n !== lastLines) {
      lastLines = n;
      LINES.forEach((l, i) => { l.el.style.display = i < n && i >= n - 8 ? "" : "none"; l.el.classList.toggle("is-new", i === n - 1); });
    }
    return true;
  }

  const st = C.stage(THREE, canvas, {
    render,
    resize(w, h) { camera.aspect = w / h; camera.updateProjectionMatrix(); layout(); camT = null; },
    lost() { fig.classList.remove("is-3d"); },
  });
  addEventListener("scroll", () => st.request(), { passive: true });
  st.request();
}
