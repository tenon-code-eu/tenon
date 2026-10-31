/* tenon · « Où partent vos données » : votre projet, trois portes (masquage, accord, journal),
 * puis trois destinations : modèle local, fournisseurs européens, et hors UE, arrêté par une
 * barrière sans votre consentement. Des particules d'or suivent le trajet ; le défilement
 * fait avancer le récit. Horizontal sur grand écran, vertical sur mobile. */
export function mount(fig, THREE, C) {
  fig.classList.add("is-3d");
  const canvas = fig.querySelector("canvas");
  const track = fig.querySelector(".h-flow3d__track");
  const col = C.colors(THREE);
  const scroll = !C.reduce;
  if (scroll) fig.classList.add("is-scroll");

  const scene = new THREE.Scene();
  const camera = new THREE.PerspectiveCamera(35, 1, 0.1, 200);
  scene.add(new THREE.AmbientLight(0xffffff, 1.1));
  const key = new THREE.DirectionalLight(0xffffff, 2.2); key.position.set(3, 6, 8); scene.add(key);
  const goldL = new THREE.PointLight(col.gold, 25, 30); goldL.position.set(0, 3, 4); scene.add(goldL);
  const world = new THREE.Group(); scene.add(world);

  /* Coordonnées du récit : a le long du trajet, b en travers. */
  const A = { project: -8, gates: [-4.6, -2.2, 0.2], split: 2.4, dest: 7.2 };
  const B = { local: 2.8, eu: 0, out: -2.8 };
  let portrait = false;
  const P = (a, b, z) => (portrait ? new THREE.Vector3(b * 0.6 - 1.0, -a * 0.95, z || 0) : new THREE.Vector3(a, b, z || 0));

  /* Matières */
  const mRail = new THREE.MeshStandardMaterial({ color: col.rail, roughness: 0.6, metalness: 0.05 });
  const mPost = new THREE.MeshStandardMaterial({ color: col.post, roughness: 0.5, metalness: 0.2 });
  const mNight = new THREE.MeshStandardMaterial({ color: col.night2, roughness: 0.6, metalness: 0.1 });
  const mGold = new THREE.MeshStandardMaterial({ color: col.gold, emissive: col.gold, emissiveIntensity: 0.4, roughness: 0.3, metalness: 0.5 });
  const edgeMat = (c, o) => new THREE.LineBasicMaterial({ color: c, transparent: true, opacity: o == null ? 0.9 : o });
  const withEdges = (geo, mat, ec) => { const g = new THREE.Group(); g.add(new THREE.Mesh(geo, mat), new THREE.LineSegments(new THREE.EdgesGeometry(geo), edgeMat(ec || col.edge))); return g; };

  /* Votre projet : une pile de fichiers, le premier porte quelques lignes de code. */
  const project = new THREE.Group();
  for (let i = 0; i < 4; i++) { const s = withEdges(new THREE.BoxGeometry(2, 0.18, 1.4), mRail); s.position.y = -0.55 + i * 0.34; s.position.x = (i % 2) * 0.08; project.add(s); }
  [0.9, 1.3, 0.7].forEach((w, i) => { const l = new THREE.Mesh(new THREE.BoxGeometry(w, 0.03, 0.08), mGold); l.position.set(-0.85 + w / 2 + 0.05, 0.56, -0.35 + i * 0.3); project.add(l); });
  world.add(project);

  /* Les trois portes : le contour du bouclier tenon, traversé par le flux. */
  const ringBig = C.hexRing(THREE, 0.055, 3.2, 0.18), ringSmall = C.hexRing(THREE, 0.04, 3.6, 0.14);
  const gates = A.gates.map(() => {
    const mat = new THREE.MeshStandardMaterial({ color: col.edge, emissive: col.gold, emissiveIntensity: 0, roughness: 0.35, metalness: 0.4 });
    const g = new THREE.Mesh(ringBig, mat); world.add(g); return { g, mat };
  });

  /* Destinations */
  const local = withEdges(new THREE.BoxGeometry(1.7, 1.1, 1.3), mPost);
  const screen = new THREE.Mesh(new THREE.PlaneGeometry(1.3, 0.7), new THREE.MeshBasicMaterial({ color: col.night }));
  screen.position.set(0, 0.05, 0.651); local.add(screen);
  const eu = new THREE.Group();
  eu.add(withEdges(new THREE.CylinderGeometry(1.0, 1.0, 0.16, 48), new THREE.MeshStandardMaterial({ color: col.accent, roughness: 0.4, metalness: 0.3 })));
  eu.children[0].rotation.x = Math.PI / 2;
  const euStars = new THREE.Group(); eu.add(euStars);
  const sg = C.starGeometry(THREE, 0.15, 0.05);
  for (let i = 0; i < 12; i++) { const a = Math.PI / 2 - i * Math.PI / 6, s = new THREE.Mesh(sg, mGold); s.position.set(Math.cos(a) * 1.35, Math.sin(a) * 1.35, 0.1); euStars.add(s); }
  const out = withEdges(new THREE.BoxGeometry(1.5, 1.5, 1.5), mNight, col.rule);
  const barrierMat = new THREE.MeshBasicMaterial({ color: col.danger, transparent: true, opacity: 0.22, side: THREE.DoubleSide, depthWrite: false });
  const barrier = new THREE.Group();
  const bGeo = new THREE.BoxGeometry(0.06, 2.2, 2.2);
  barrier.add(new THREE.Mesh(bGeo, barrierMat), new THREE.LineSegments(new THREE.EdgesGeometry(bGeo), edgeMat(col.danger, 1)));
  { const xg = new THREE.BufferGeometry().setFromPoints([new THREE.Vector3(0, -0.7, -0.7), new THREE.Vector3(0, 0.7, 0.7), new THREE.Vector3(0, 0.7, -0.7), new THREE.Vector3(0, -0.7, 0.7)]);
    barrier.add(new THREE.LineSegments(xg, edgeMat(col.danger, 1))); }
  world.add(local, eu, out, barrier);

  /* Trajets : un tronc, puis trois branches. */
  let trunk, branches, tubes = [];
  const tubeMat = new THREE.MeshBasicMaterial({ color: col.edge, transparent: true, opacity: 0.45 });
  const tubeOut = new THREE.MeshBasicMaterial({ color: col.danger, transparent: true, opacity: 0.5 });
  const BAR_T = 0.5;   /* la barrière, à mi-chemin de la branche hors UE */

  /* Particules : 45 % local, 45 % UE, 10 % tentent de sortir de l'UE. */
  const N = 320, pos = new Float32Array(N * 3), colr = new Float32Array(N * 3), PT = [];
  const rnd = (() => { let s = 11; return () => (s = (s * 16807) % 2147483647) / 2147483647; })();
  for (let i = 0; i < N; i++) { const r = rnd(); PT.push({ route: r < 0.45 ? 0 : r < 0.9 ? 1 : 2, phase: rnd(), jit: (rnd() - 0.5) * 0.18, jit2: (rnd() - 0.5) * 0.18 }); }
  const pGeo = new THREE.BufferGeometry();
  pGeo.setAttribute("position", new THREE.BufferAttribute(pos, 3)); pGeo.setAttribute("color", new THREE.BufferAttribute(colr, 3));
  const points = new THREE.Points(pGeo, new THREE.PointsMaterial({ size: 0.46, map: C.glowTexture(THREE), vertexColors: true, transparent: true, depthWrite: false, blending: THREE.AdditiveBlending }));
  points.frustumCulled = false; world.add(points);

  /* Étiquettes HTML (textes du dictionnaire T de home.html). */
  const L = {};
  fig.querySelectorAll(".h-flow3d__labels [data-k]").forEach((el) => { L[el.getAttribute("data-k")] = { el, at: new THREE.Vector3() }; });

  /* Sur mobile, les destinations s'étagent : l'UE au centre, plus bas, local et hors UE de part et d'autre. */
  const DA = (k) => (portrait ? (k === "eu" ? A.dest + 2.8 : A.dest - 1.2) : A.dest);
  let DS = 1;
  function build() {
    portrait = canvas.clientWidth / Math.max(1, canvas.clientHeight) < 0.85;
    DS = portrait ? 0.72 : 1;
    project.position.copy(P(A.project, 0)); project.rotation.set(0.35, portrait ? 0.4 : -0.5, 0);
    gates.forEach((g, i) => { g.g.geometry = portrait ? ringSmall : ringBig; g.g.position.copy(P(A.gates[i], 0)); g.g.rotation.set(portrait ? Math.PI / 2 : 0, portrait ? 0 : Math.PI / 2, 0); });
    local.position.copy(P(DA("local"), B.local)); local.rotation.set(0.25, portrait ? 0.3 : -0.45, 0);
    eu.position.copy(P(DA("eu"), B.eu)); eu.rotation.set(0, portrait ? 0 : -0.35, 0);
    out.position.copy(P(DA("out"), B.out)); out.rotation.set(0.25, portrait ? 0.3 : -0.45, 0);
    tubes.forEach((t) => { world.remove(t); t.geometry.dispose(); });
    const S = P(A.split, 0);
    trunk = new THREE.LineCurve3(P(A.project + 1.2, 0), S);
    branches = [["local", B.local], ["eu", B.eu], ["out", B.out]].map(([k, b], i) => new THREE.QuadraticBezierCurve3(S, P(A.split + 2.4, b), P(DA(k) - (i === 1 ? 1.4 : 1.1) * DS, b)));
    tubes = [trunk].concat(branches).map((c, i) => { const m = new THREE.Mesh(new THREE.TubeGeometry(c, 64, 0.035, 6), i === 3 ? tubeOut : tubeMat); world.add(m); return m; });
    const bp = branches[2].getPoint(BAR_T), bt = branches[2].getTangent(BAR_T);
    barrier.position.copy(bp); barrier.rotation.set(0, 0, Math.atan2(bt.y, bt.x));
    /* ancres des étiquettes */
    const set = (k, v) => { if (L[k]) L[k].at.copy(v); };
    set("project", P(A.project, 0).add(new THREE.Vector3(portrait ? 1.35 : 0, portrait ? 0 : 1.2, 0)));
    /* grand écran : libellés des portes alternés au-dessus et au-dessous ; mobile : à droite */
    ["mask", "perm", "log"].forEach((k, i) => set(k, P(A.gates[i], 0).add(new THREE.Vector3(portrait ? 1.45 : 0, portrait ? 0 : (i === 1 ? -1.85 : 1.8), 0))));
    set("route", S.clone().add(new THREE.Vector3(portrait ? 1.0 : -1.3, portrait ? 0.25 : -1.0, 0)));
    set("local", P(DA("local"), B.local).add(new THREE.Vector3(0, portrait ? -0.95 : 1.0, 0)));
    set("eu", P(DA("eu"), B.eu).add(new THREE.Vector3(portrait ? 0 : 1.95, portrait ? -1.3 : 0, 0)));
    set("out", P(DA("out"), B.out).add(new THREE.Vector3(0, portrait ? -0.95 : -1.05, 0)));
    fig.classList.toggle("is-portrait", portrait);
  }

  const tan = Math.tan(camera.fov * Math.PI / 360);
  function frameCam(p) {
    const asp = camera.aspect, ex = portrait ? 3.6 : 11.6, ey = portrait ? 12.4 : 5.4;
    const dEnd = Math.max(ex / (tan * asp), ey / tan) * 1.04;
    const k = C.easeIO(C.clamp(p / 0.7, 0, 1));
    const target = P(C.lerp(A.project + 3, portrait ? 1.5 : 0.6, k), 0); if (portrait) target.x = -0.35;
    const d = C.lerp(dEnd * 0.62, dEnd, k);
    const yaw = portrait ? C.lerp(0.25, 0.1, k) : C.lerp(-0.55, -0.28, k), pitch = portrait ? C.lerp(0.3, 0.1, k) : C.lerp(0.42, 0.22, k);
    camera.position.set(target.x + d * Math.sin(yaw) * Math.cos(pitch), target.y + d * Math.sin(pitch), target.z + d * Math.cos(yaw) * Math.cos(pitch));
    camera.lookAt(target);
  }

  const gold = col.gold, danger = col.danger;
  const far = 1e4, tmpV = new THREE.Vector3();
  let prog = scroll ? 0 : 1, built = false, time = scroll ? 0 : 3.7;
  function render(dt) {
    if (!built) { build(); built = true; }
    const target = scroll ? C.pinProgress(track) : 1;
    prog = scroll ? C.damp(prog, target, 0.18, dt) : 1;
    if (scroll) time += dt;
    const p = prog;
    frameCam(p);

    /* le récit : apparition du projet, front du flux, portes, branches, barrière */
    const appear = C.easeOut(C.smooth(0, 0.1, p));
    project.scale.setScalar(0.6 + 0.4 * appear);
    const front = C.smooth(0.08, 0.62, p);                       /* 0 → 1 : tout le trajet */
    const tFront = C.clamp(front / 0.5, 0, 1), bFront = C.clamp((front - 0.5) / 0.5, 0, 1);
    gates.forEach((g, i) => {
      const at = (A.gates[i] - (A.project + 1.2)) / (A.split - (A.project + 1.2));
      const lit = tFront >= at ? 1 : 0;
      g.mat.emissiveIntensity = C.damp(g.mat.emissiveIntensity, lit * (0.55 + 0.15 * Math.sin(time * 3 + i)), 0.15, dt || 0.016);
      g.mat.color.copy(lit ? gold : col.edge);
    });
    const ti = tubes[0].geometry.index.count;
    tubes[0].geometry.setDrawRange(0, Math.floor(ti * tFront / 6) * 6);
    for (let i = 1; i < 4; i++) { const c = tubes[i].geometry.index.count; tubes[i].geometry.setDrawRange(0, Math.floor(c * bFront / 6) * 6); }
    const dests = C.smooth(0.42, 0.6, p);
    [local, eu, out].forEach((o) => o.scale.setScalar((0.4 + 0.6 * C.easeOut(dests)) * DS));
    euStars.rotation.z = scroll ? -time * 0.15 : 0;
    const bar = C.easeOut(C.smooth(0.55, 0.68, p));
    barrier.scale.set(1, Math.max(0.001, bar), Math.max(0.001, bar));
    barrierMat.opacity = 0.18 + 0.12 * bar + (scroll ? 0.06 * Math.sin(time * 4) : 0);

    /* particules */
    for (let i = 0; i < N; i++) {
      const q = PT[i], u = (q.phase + time * 0.11) % 1;
      const s = u * 2;                                           /* 0–1 tronc, 1–2 branche */
      let vis = s <= front * 2 && appear > 0.5, c = gold;
      if (s < 1) trunk.getPoint(s, tmpV);
      else {
        let bs = s - 1;
        if (q.route === 2) {
          if (bs > BAR_T) { vis = vis && bs < BAR_T + 0.12 && bar > 0.5; bs = BAR_T - (bs - BAR_T) * 0.3; c = danger; }
          else if (bs > BAR_T * 0.6) c = danger;
          if (bar <= 0.5 && bs > BAR_T) vis = false;
        }
        branches[q.route].getPoint(bs, tmpV);
      }
      if (!vis) tmpV.set(far, far, far);
      else { tmpV.x += portrait ? q.jit : 0; tmpV.y += portrait ? 0 : q.jit; tmpV.z += q.jit2; }
      pos.set([tmpV.x, tmpV.y, tmpV.z], i * 3); colr.set([c.r, c.g, c.b], i * 3);
    }
    pGeo.attributes.position.needsUpdate = true; pGeo.attributes.color.needsUpdate = true;

    st.renderer.render(scene, camera);

    /* étiquettes */
    const show = { project: 0.02, mask: 0, perm: 0, log: 0, route: 0.5, local: 0.55, eu: 0.55, out: 0.66 };
    A.gates.forEach((ga, i) => { show[["mask", "perm", "log"][i]] = 0.08 + 0.54 * 0.5 * ((ga - (A.project + 1.2)) / (A.split - (A.project + 1.2))); });
    const items = Object.keys(L).map((k) => ({ el: L[k].el, at: L[k].at, hide: p < show[k],
      anchor: portrait ? (/^(project|mask|perm|log|route)$/.test(k) ? "translate(0, -50%)" : "translate(-50%, 0)")
        : k === "eu" ? "translate(0, -50%)" : (k === "out" || k === "route" || k === "perm") ? "translate(-50%, 0)" : "translate(-50%, -100%)" }));
    C.placeLabels(THREE, camera, st.width, st.height, items);
    return true;
  }

  const st = C.stage(THREE, canvas, {
    render,
    resize(w, h) { camera.aspect = w / h; camera.updateProjectionMatrix(); build(); built = true; },
    lost() { fig.classList.remove("is-3d", "is-scroll"); },
  });
  if (scroll) addEventListener("scroll", () => st.request(), { passive: true });
  st.request();
}
