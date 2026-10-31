/* tenon · le harness, pièce par pièce : la carte des vingt-deux composants vue de haut,
 * pistes de données et paquets qui circulent, visite composant par composant.
 * Portage en three.js de l'ancienne maquette WebGL écrite à la main : mêmes formes, mêmes
 * pistes, mêmes contrôles (clic, glisser pour tourner, ← → Échap Entrée). */
export function mount(stageEl, THREE, C) {
  const canvas = stageEl.querySelector("canvas");
  const pinsBox = stageEl.querySelector(".h-pins");
  const dataEl = document.getElementById("h-parts");
  const PARTS_TXT = JSON.parse(dataEl.textContent);
  const col = C.colors(THREE);
  const reduce = C.reduce;
  /* Le canvas doit avoir une taille pour être observé : la version HTML se retire dès le montage
   * (main.js la remet si le montage échoue). */
  stageEl.classList.remove("is-fallback");

  /* Géométrie : pavés posés sur la carte (y = 0). B(cx, y0, cz, sx, sy, sz). */
  function B(cx, y0, cz, sx, sy, sz) { return [cx - sx / 2, cx + sx / 2, y0, y0 + sy, cz - sz / 2, cz + sz / 2]; }
  function mesh(list) {
    const tri = [], nor = [], lines = [];
    list.forEach((b) => {
      const [x0, x1, y0, y1, z0, z1] = b;
      [[[x1,y0,z0],[x1,y1,z0],[x1,y1,z1],[x1,y0,z1],[1,0,0]], [[x0,y0,z1],[x0,y1,z1],[x0,y1,z0],[x0,y0,z0],[-1,0,0]],
       [[x0,y1,z0],[x0,y1,z1],[x1,y1,z1],[x1,y1,z0],[0,1,0]], [[x0,y0,z1],[x0,y0,z0],[x1,y0,z0],[x1,y0,z1],[0,-1,0]],
       [[x0,y0,z1],[x1,y0,z1],[x1,y1,z1],[x0,y1,z1],[0,0,1]], [[x1,y0,z0],[x0,y0,z0],[x0,y1,z0],[x1,y1,z0],[0,0,-1]]
      ].forEach((f) => { [0, 1, 2, 0, 2, 3].forEach((i) => { tri.push(...f[i]); nor.push(...f[4]); }); });
      const c = [[x0,y0,z0],[x1,y0,z0],[x1,y1,z0],[x0,y1,z0],[x0,y0,z1],[x1,y0,z1],[x1,y1,z1],[x0,y1,z1]];
      [[0,1],[1,2],[2,3],[3,0],[4,5],[5,6],[6,7],[7,4],[0,4],[1,5],[2,6],[3,7]].forEach((e) => lines.push(...c[e[0]], ...c[e[1]]));
    });
    const g = new THREE.BufferGeometry();
    g.setAttribute("position", new THREE.Float32BufferAttribute(tri, 3));
    g.setAttribute("normal", new THREE.Float32BufferAttribute(nor, 3));
    const l = new THREE.BufferGeometry(); l.setAttribute("position", new THREE.Float32BufferAttribute(lines, 3));
    return { solid: g, edges: l };
  }

  /* Les composants : formes, point d'ancrage de l'étiquette, cadrage du zoom. */
  const SHAPES = {
    tools: { at: [-8, 1.3, 0], d: 9, boxes: [B(-8, 0, -1.4, 1.6, 0.5, 1), B(-8, 0, 0, 1.6, 0.9, 1), B(-8, 0, 1.4, 1.6, 0.7, 1)] },
    sandbox: { at: [-6.9, 1.7, -2.3], d: 10, boxes: [B(-9.1, 0, -2.3, 0.14, 1.4, 0.14), B(-6.9, 0, -2.3, 0.14, 1.4, 0.14), B(-9.1, 0, 2.3, 0.14, 1.4, 0.14),
      B(-6.9, 0, 2.3, 0.14, 1.4, 0.14), B(-8, 1.4, -2.3, 2.34, 0.12, 0.14), B(-8, 1.4, 2.3, 2.34, 0.12, 0.14), B(-9.1, 1.4, 0, 0.14, 0.12, 4.74), B(-6.9, 1.4, 0, 0.14, 0.12, 4.74)] },
    permissions: { at: [-4, 2.2, 0], d: 8, boxes: [B(-4, 0, -0.9, 0.4, 1.6, 0.4), B(-4, 0, 0.9, 0.4, 1.6, 0.4), B(-4, 1.6, 0, 0.5, 0.3, 2.3)] },
    engine: { at: [0, 3, 0], d: 10, boxes: [B(0, 0, 0, 3, 1.6, 3), B(0, 1.6, 0, 2, 0.7, 2), B(0, 2.3, 0, 0.9, 0.4, 0.9)] },
    redaction: { at: [4, 1.8, 0], d: 8, boxes: [0, 1, 2, 3, 4, 5].map((i) => B(4 + (i - 2.5) * 0.34, 0, 0, 0.18, 1.3, 2.2)) },
    routing: { at: [6.4, 1.4, 0], d: 8, boxes: [B(6.4, 0, 0, 0.9, 0.55, 0.9), B(6.4, 0.55, 0, 0.5, 0.35, 0.5), B(6.4, 0, -1.2, 0.3, 0.3, 0.6), B(6.4, 0, 1.2, 0.3, 0.3, 0.6)] },
    profile: { at: [8.4, 1.3, -2.8], d: 8, boxes: [B(7.7, 0, -2.8, 0.25, 1.0, 0.25), B(9.1, 0, -2.8, 0.25, 1.0, 0.25), B(8.4, 0.8, -2.8, 1.9, 0.22, 0.22)] },
    timestamp: { at: [8.4, 1.7, 4.8], d: 8, boxes: [B(8.4, 0, 4.8, 1.4, 0.3, 1.4), B(8.4, 0.3, 4.8, 0.9, 0.9, 0.9), B(8.4, 1.2, 4.8, 0.3, 0.25, 0.3)] },
    export: { at: [4.8, 1.3, 4.8], d: 8, boxes: [B(4.4, 0, 4.8, 0.9, 1.0, 0.12), B(5.2, 0, 4.9, 0.9, 0.8, 0.12), B(4.8, 0, 4.8, 2.2, 0.1, 1.2)] },
    cra: { at: [-8, 1.3, -5], d: 8, boxes: [B(-8, 0, -5, 2.2, 0.4, 1.4), B(-8.5, 0.4, -5, 0.5, 0.6, 0.5), B(-7.8, 0.4, -5, 0.5, 0.4, 0.5), B(-7.1, 0.4, -5, 0.5, 0.8, 0.5)] },
    providers: { at: [8.4, 3.7, 0], d: 11, boxes: [B(8.4, 0, 0, 1.6, 2.6, 1.6), B(8.4, 2.6, 0, 0.6, 0.8, 0.6)] },
    audit: { at: [0, 1.2, 4.8], d: 9, boxes: (() => { const l = []; for (let i = 0; i < 6; i++) { const x = -2.5 + i; l.push(B(x, 0, 4.8, 0.7, 0.7, 0.7)); if (i < 5) l.push(B(x + 0.5, 0.29, 4.8, 0.32, 0.12, 0.12)); } return l; })() },
    skills: { at: [0, 1.4, -4.8], d: 8, boxes: [0, 1, 2, 3, 4].map((i) => B(i * 0.09 - 0.18, i * 0.22, -4.8, 2.2, 0.16, 1.5)) },
    subagents: { at: [4.8, 1.5, -4.4], d: 9, boxes: [3.5, 4.8, 6.1].reduce((l, x) => l.concat([B(x, 0, -4.4, 1, 0.7, 1), B(x, 0.7, -4.4, 0.6, 0.35, 0.6)]), []) },
    hooks: { at: [-4.7, 1.9, -4.2], d: 7, boxes: [B(-5.1, 0, -4.2, 0.4, 1.5, 0.4), B(-4.6, 1.15, -4.2, 1.4, 0.35, 0.4), B(-4.1, 0.6, -4.2, 0.4, 0.9, 0.4)] },
    mcp: { at: [-7.5, 1.2, 4.5], d: 8, boxes: [B(-7.5, 0, 4.5, 2.2, 0.8, 1.6), B(-6.2, 0.25, 4.1, 0.5, 0.3, 0.3), B(-6.2, 0.25, 4.9, 0.5, 0.3, 0.3)] },
    checkpoints: { at: [-3.4, 1.6, 5.6], d: 8, boxes: [0, 1, 2, 3].map((i) => B(-4.3 + i * 0.6, 0, 5.6, 0.55, 0.3 + i * 0.25, 1.2)) },
    /* Mémoire : trois fiches empilées, la plus haute décalée (une note lue). */
    memory: { at: [-2.3, 1.2, -2.9], d: 7, boxes: [B(-2.3, 0, -2.9, 1.3, 0.14, 0.9), B(-2.25, 0.14, -2.9, 1.3, 0.14, 0.9), B(-2.15, 0.28, -2.95, 1.3, 0.14, 0.9), B(-2.0, 0.42, -3.05, 1.3, 0.14, 0.9)] },
    /* Sobriété : une jauge, trois cellules pleines sur dix. */
    sobriety: { at: [3.2, 1.1, 2.7], d: 7, boxes: [B(3.2, 0, 2.7, 2.2, 0.18, 0.8)].concat([0, 1, 2, 3, 4, 5, 6, 7, 8, 9].map((i) => B(2.33 + i * 0.193, 0.18, 2.7, 0.15, i < 3 ? 0.5 : 0.14, 0.5))) },
    /* Éditeurs : un écran sur son pied. */
    editors: { at: [-5.3, 1.8, 2.9], d: 7, boxes: [B(-5.3, 0, 2.9, 0.9, 0.06, 0.55), B(-5.3, 0.06, 2.9, 0.18, 0.4, 0.18), B(-5.3, 0.46, 2.9, 1.7, 1.0, 0.12)] },
    /* Pipelines : quatre étapes de plus en plus hautes sur un conduit. */
    ci: { at: [-5.6, 1.4, -2.6], d: 7, boxes: [B(-5.6, 0, -2.6, 0.16, 0.16, 2.4)].concat([0, 1, 2, 3].map((i) => B(-5.6, 0, -3.5 + i * 0.6, 0.7, 0.35 + i * 0.18, 0.42))) },
    policy: { at: [9.6, 1.1, -6.1], d: 13, boxes: [B(0, 0, -7.2, 21.6, 0.3, 0.12), B(0, 0, 7.4, 21.6, 0.3, 0.12), B(-10.8, 0, 0.1, 0.12, 0.3, 14.6),
      B(10.8, 0, 0.1, 0.12, 0.3, 14.6), B(9.6, 0, -6.1, 1, 0.5, 1)] },
  };
  /* Pistes de données (x, z), avec les composants qu'elles relient. */
  const TRACES = [
    { ends: ["tools", "permissions"], pts: [[-7.2, 0], [-4.25, 0]] },
    { ends: ["permissions", "engine"], pts: [[-3.75, 0], [-1.5, 0]] },
    { ends: ["engine", "redaction"], pts: [[1.5, 0], [3.0, 0]] },
    { ends: ["redaction", "routing"], pts: [[5.0, 0], [5.95, 0]] },
    { ends: ["routing", "providers"], pts: [[6.85, 0], [7.6, 0]] },
    { ends: ["profile", "providers"], pts: [[8.4, -2.6], [8.4, -0.8]] },
    { ends: ["audit", "export"], pts: [[2.6, 4.8], [3.7, 4.8]] },
    { ends: ["audit", "timestamp"], pts: [[1.5, 5.2], [1.5, 6.4], [8.4, 6.4], [8.4, 5.5]] },
    { ends: ["cra", "skills"], pts: [[-8, -5.7], [-8, -6.4], [0, -6.4], [0, -5.6]] },
    { ends: ["sandbox", "tools"], pts: [[-6.9, -2.3], [-6.9, 2.3]] },
    { ends: ["sandbox", "mcp"], pts: [[-6.9, 2.3], [-6.9, 3.7]] },
    { ends: ["engine", "audit"], pts: [[0, 1.5], [0, 4.4]] },
    { ends: ["engine", "skills"], pts: [[0, -1.5], [0, -4.0]] },
    { ends: ["engine", "subagents"], pts: [[1.0, -1.5], [1.0, -2.7], [4.8, -2.7], [4.8, -3.9]] },
    { ends: ["hooks", "permissions"], pts: [[-4.6, -4.0], [-4.6, -2.2], [-4.0, -2.2], [-4.0, -1.1]] },
    { ends: ["mcp", "permissions"], pts: [[-6.4, 4.5], [-4.0, 4.5], [-4.0, 1.1]] },
    { ends: ["checkpoints", "engine"], pts: [[-3.1, 5.0], [-3.1, 2.4], [-1.0, 2.4], [-1.0, 1.5]] },
    { ends: ["memory", "engine"], pts: [[-2.3, -2.45], [-2.3, -1.0], [-1.5, -1.0]] },
    { ends: ["sobriety", "engine"], pts: [[2.1, 2.7], [1.0, 2.7], [1.0, 1.5]] },
    { ends: ["sobriety", "routing"], pts: [[4.3, 2.7], [6.4, 2.7], [6.4, 1.5]] },
    { ends: ["editors", "permissions"], pts: [[-4.45, 2.9], [-4.0, 2.9]] },
    { ends: ["ci", "permissions"], pts: [[-5.25, -1.4], [-5.25, -0.9], [-4.2, -0.9]] },
  ];

  /* ── Scène ──────────────────────────────────────────────────── */
  const scene = new THREE.Scene();
  const camera = new THREE.PerspectiveCamera(0.6 * 180 / Math.PI, 1, 0.5, 200);
  /* Éclairage repris de l'ancien shader : ambiance 0,28, lumière principale 0,62, contre-jour 0,18. */
  scene.add(new THREE.AmbientLight(0xffffff, Math.PI * 0.28));
  const key = new THREE.DirectionalLight(0xffffff, Math.PI * 0.62); key.position.set(0.4, 0.85, 0.5); scene.add(key);
  const fill = new THREE.DirectionalLight(0xffffff, Math.PI * 0.18); fill.position.set(-0.6, 0.3, -0.5); scene.add(fill);

  /* ── Quatre étages (vue éclatée) : du bas vers le haut, contrôle, moteur, données, preuves ── */
  const LAYERS = JSON.parse(document.getElementById("h-layers").textContent);
  const layerOf = {};
  LAYERS.forEach((ids, li) => ids.forEach((id) => { layerOf[id] = li; }));
  const floors = LAYERS.map(() => { const g = new THREE.Group(); scene.add(g); return g; });
  const GAP = 3.4;

  const board = mesh([B(0, -0.3, 0.1, 22, 0.3, 15)]);
  scene.add(new THREE.Mesh(board.solid, new THREE.MeshLambertMaterial({ color: col.night2 })));
  { const g = [];
    for (let x = -10; x <= 10; x++) g.push(x, 0.004, -7, x, 0.004, 7.3);
    for (let z = -7; z <= 7; z++) g.push(-10.8, 0.004, z, 10.8, 0.004, z);
    const geo = new THREE.BufferGeometry(); geo.setAttribute("position", new THREE.Float32BufferAttribute(g, 3));
    scene.add(new THREE.LineSegments(geo, new THREE.LineBasicMaterial({ color: col.rule, transparent: true, opacity: 0.55 }))); }
  /* une plaque de verre sous chaque étage soulevé */
  const plateGeo = mesh([B(0, -0.06, 0.1, 22, 0.06, 15)]);
  const plates = floors.slice(1).map((g) => {
    const m = new THREE.MeshLambertMaterial({ color: col.night2, transparent: true, opacity: 0, depthWrite: false });
    const e = new THREE.LineBasicMaterial({ color: col.edge, transparent: true, opacity: 0 });
    g.add(new THREE.Mesh(plateGeo.solid, m), new THREE.LineSegments(plateGeo.edges, e));
    return { m, e };
  });

  TRACES.forEach((t) => {
    let len = 0; t.segs = [];
    for (let i = 1; i < t.pts.length; i++) {
      const a = t.pts[i - 1], b = t.pts[i], l = Math.hypot(b[0] - a[0], b[1] - a[1]);
      t.segs.push({ a, b, l, o: len }); len += l;
    }
    t.len = len;
    /* une piste interne à un étage monte avec lui ; une piste entre étages s'efface à l'ouverture */
    t.layer = layerOf[t.ends[0]] === layerOf[t.ends[1]] ? layerOf[t.ends[0]] : -1;
    t.mat = new THREE.LineBasicMaterial({ color: col.edge, transparent: true, opacity: 0.7 });
    const line = new THREE.Line(new THREE.BufferGeometry().setFromPoints(t.pts.map(([x, z]) => new THREE.Vector3(x, 0.02, z))), t.mat);
    (t.layer >= 0 ? floors[t.layer] : scene).add(line);
  });

  const PARTS = PARTS_TXT.map((p, i) => {
    const sh = SHAPES[p.id], m = mesh(sh.boxes), layer = layerOf[p.id];
    const solid = new THREE.MeshLambertMaterial({ color: col.rail, polygonOffset: true, polygonOffsetFactor: 1, polygonOffsetUnits: 1 });
    const edge = new THREE.LineBasicMaterial({ color: col.edge, transparent: true, opacity: 0.8 });
    floors[layer].add(new THREE.Mesh(m.solid, solid), new THREE.LineSegments(m.edges, edge));
    return { id: p.id, name: p.name, desc: p.desc, num: (i + 1 < 10 ? "0" : "") + (i + 1), at: sh.at, d: sh.d, solid, edge, layer,
      home: new THREE.Vector3(...sh.at), anchor: new THREE.Vector3(...sh.at) };
  });

  /* Les douze étoiles autour des fournisseurs : le cercle européen, à plat sur l'étage des données. */
  const starGeo = C.starGeometry(THREE, 0.32, 0.06), starMat = new THREE.MeshBasicMaterial({ color: col.gold });
  for (let i = 0; i < 12; i++) {
    const a = i / 12 * Math.PI * 2, s = new THREE.Mesh(starGeo, starMat);
    s.position.set(8.4 + 2.3 * Math.cos(a), 1.6, 2.3 * Math.sin(a)); s.rotation.x = -Math.PI / 2; floors[layerOf.providers].add(s);
  }

  /* Paquets de données : sur les pistes du composant visé, ou de l'étage raconté. */
  const MAXP = 96, pkPos = new Float32Array(MAXP * 3), pkGeo = new THREE.BufferGeometry();
  pkGeo.setAttribute("position", new THREE.BufferAttribute(pkPos, 3)); pkGeo.setDrawRange(0, 0);
  const packets = new THREE.Points(pkGeo, new THREE.PointsMaterial({ color: col.gold, size: 0.7, map: C.glowTexture(THREE), transparent: true, depthWrite: false }));
  packets.frustumCulled = false; scene.add(packets);

  /* ── Récit au défilement (absent en mouvement réduit) ── */
  const track = stageEl.querySelector(".h-stage__track");
  const chapters = Array.prototype.slice.call(stageEl.querySelectorAll(".h-stage__chapters li"));
  const story = !reduce && !!track;
  if (story) stageEl.classList.add("is-scroll"); else stageEl.classList.add("is-explore");
  let prog = 0, chapter = -1, explode = 0, exploring = !story, viewX = 0, viewY = 0;
  const CH = [0.2, 0.37, 0.54, 0.71, 0.86];                     /* bornes des quatre chapitres */

  /* ── Caméra : cible, distance, lacet, tangage ; « goal » est l'état visé ── */
  const OVER = { t: [0, 0, 0.2], d: 35, yaw: 0, pitch: 1.12 };
  const cam = { t: [0, 0, 0.2], d: 38, yaw: -0.5, pitch: 1.3 };
  const goal = { t: OVER.t.slice(), d: OVER.d, yaw: OVER.yaw, pitch: OVER.pitch };
  let focus = -1, playing = false, timer = 0, dragging = false, moved = 0, lx = 0, ly = 0, t0 = 0, shown = false;

  const numEl = document.getElementById("h-part-num"), nameEl = document.getElementById("h-part-name"), descEl = document.getElementById("h-part-desc");
  const tourBtn = document.getElementById("h-tour");
  const pins = PARTS.map((p, i) => {
    const b = document.createElement("button");
    b.type = "button"; b.className = "h-pin";
    b.innerHTML = '<span class="h-pin__num"></span><span class="h-pin__name"></span>';
    b.querySelector(".h-pin__num").textContent = p.num;
    b.querySelector(".h-pin__name").textContent = p.name;
    b.setAttribute("aria-label", p.num + " " + p.name);
    b.addEventListener("click", () => { stopTour(); select(i); });
    pinsBox.appendChild(b);
    return b;
  });

  /* recul selon le cadre : la carte (22 × 15) doit tenir en largeur sur un écran haut */
  const fitFactor = () => { const asp = canvas.clientWidth / Math.max(1, canvas.clientHeight); return Math.max(1, Math.min(2.8, 1.2 / asp)) * (story && asp > 1.3 ? 1.12 : 1); };
  function select(i) {
    focus = i;
    if (i < 0) {
      goal.t = OVER.t.slice(); goal.d = OVER.d * fitFactor(); goal.yaw = OVER.yaw; goal.pitch = OVER.pitch;
      numEl.textContent = "00"; nameEl.textContent = dataEl.getAttribute("data-overview-name"); descEl.textContent = dataEl.getAttribute("data-overview-desc");
    } else {
      const p = PARTS[i];
      goal.t = [p.at[0], p.at[1] * 0.45, p.at[2]]; goal.d = p.d * fitFactor();
      goal.yaw = -0.55 + (i % 4) * 0.36; goal.pitch = 0.62;
      numEl.textContent = p.num; nameEl.textContent = p.name; descEl.textContent = p.desc;
    }
    pins.forEach((b, k) => {
      b.classList.toggle("is-active", k === i);
      b.classList.toggle("is-dim", i >= 0 && k !== i);
      b.setAttribute("aria-pressed", k === i ? "true" : "false");
    });
    const active = i >= 0 ? PARTS[i].id : null;
    TRACES.forEach((t) => {
      const on = active && (t.ends.indexOf(active) >= 0 || active === "policy");
      t.mat.color.copy(on ? col.gold : col.edge); t.mat.opacity = on ? 1 : 0.7;
    });
    PARTS.forEach((p, k) => {
      const on = k === i, dim = i >= 0 && !on;
      p.solid.color.copy(dim ? col.post : col.rail);
      p.edge.color.copy(on ? col.gold : col.edge); p.edge.opacity = on ? 1 : 0.8;
    });
    if (reduce) { cam.t = goal.t.slice(); cam.d = goal.d; cam.yaw = goal.yaw; cam.pitch = goal.pitch; }
    st.request();
  }
  function step(dir) { const n = PARTS.length; select(focus < 0 ? (dir > 0 ? 0 : n - 1) : (focus + dir + n) % n); }

  /* Visite : un composant toutes les 3,6 s, une seule fois, puis retour à la vue d'ensemble. */
  function tick() {
    if (!playing) return;
    if (focus >= PARTS.length - 1) { stopTour(); select(-1); return; }
    select(focus + 1);
    timer = setTimeout(tick, 3600);
  }
  function startTour() {
    playing = true; tourBtn.textContent = tourBtn.getAttribute("data-pause"); tourBtn.setAttribute("aria-pressed", "true");
    if (focus >= PARTS.length - 1) focus = -1;
    clearTimeout(timer); tick();
  }
  function stopTour() {
    playing = false; clearTimeout(timer);
    tourBtn.textContent = tourBtn.getAttribute("data-play"); tourBtn.setAttribute("aria-pressed", "false");
  }
  tourBtn.addEventListener("click", () => { if (playing) stopTour(); else startTour(); });
  document.getElementById("h-prev").addEventListener("click", () => { stopTour(); step(-1); });
  document.getElementById("h-next").addEventListener("click", () => { stopTour(); step(1); });
  document.getElementById("h-overview").addEventListener("click", () => { stopTour(); select(-1); });

  const v = new THREE.Vector3();
  function project(p) {
    v.copy(p).project(camera);
    return [(v.x * 0.5 + 0.5) * canvas.clientWidth, (1 - (v.y * 0.5 + 0.5)) * canvas.clientHeight, v.z];
  }

  function storyFrame(dt) {
    prog = C.damp(prog, C.pinProgress(track), 0.15, dt);
    const p = prog;
    explode = C.smooth(0.05, 0.17, p) * (1 - C.smooth(0.86, 0.94, p));
    let c = -1;
    for (let i = 0; i < 4; i++) if (p >= CH[i] && p < CH[i + 1]) c = i;
    const ex = p >= 0.94;
    if (ex !== exploring) {
      exploring = ex; stageEl.classList.toggle("is-explore", ex);
      if (ex) { goal.t = OVER.t.slice(); goal.d = OVER.d * fitFactor(); goal.yaw = OVER.yaw; goal.pitch = OVER.pitch; }
      else { stopTour(); if (focus >= 0) select(-1); }
    }
    if (c !== chapter) {
      chapter = c;
      chapters.forEach((li, i) => li.classList.toggle("is-on", i === c));
      PARTS.forEach((pt, k) => {
        const on = c < 0 || pt.layer === c;
        pt.solid.color.copy(on ? col.rail : col.post);
        pt.edge.color.copy(c >= 0 && on ? col.gold : col.edge); pt.edge.opacity = on ? (c >= 0 ? 1 : 0.8) : 0.3;
        pins[k].classList.toggle("is-named", c >= 0 && on);
      });
      TRACES.forEach((t) => { const on = c >= 0 && t.layer === c; t.mat.color.copy(on ? col.gold : col.edge); });
    }
    if (exploring) return;
    /* caméra du récit : vue de trois quarts qui tourne lentement autour de la pile */
    const fit = fitFactor(), lift = explode * GAP;
    const midY = lift * 1.5, aimY = c >= 0 ? C.lerp(midY, lift * c + 0.6, 0.35) : midY;
    cam.t = [0, aimY, 0.2];
    const tall = canvas.clientWidth / Math.max(1, canvas.clientHeight) < 0.8;   /* mobile : la pile ouverte se regarde de plus près */
    cam.d = C.lerp(OVER.d * fit, 46 * fit * (tall ? 0.68 : 1), explode);
    cam.yaw = C.lerp(OVER.yaw, C.lerp(-0.7, 0.55, C.smooth(0.12, 0.86, p)), explode);
    cam.pitch = C.lerp(OVER.pitch, 0.42, explode);
    goal.t = cam.t.slice(); goal.d = cam.d; goal.yaw = cam.yaw; goal.pitch = cam.pitch;
  }

  function render(dt, time) {
    if (story) storyFrame(dt);
    floors.forEach((g, i) => { g.position.y = explode * GAP * i; });
    plates.forEach((pl) => { pl.m.opacity = 0.55 * explode; pl.e.opacity = 0.5 * explode; });
    TRACES.forEach((t) => { if (t.layer < 0) t.mat.opacity = 0.7 * (1 - explode); });
    PARTS.forEach((pt) => { pt.anchor.copy(pt.home); pt.anchor.y += explode * GAP * pt.layer; });

    const k = reduce ? 1 : 1 - Math.exp(-dt / 0.32);
    let settled = true;
    ["d", "yaw", "pitch"].forEach((f) => { cam[f] = C.lerp(cam[f], goal[f], k); if (Math.abs(cam[f] - goal[f]) > 1e-3) settled = false; else cam[f] = goal[f]; });
    for (let i = 0; i < 3; i++) { cam.t[i] = C.lerp(cam.t[i], goal.t[i], k); if (Math.abs(cam.t[i] - goal.t[i]) > 1e-3) settled = false; else cam.t[i] = goal.t[i]; }
    const cp = Math.cos(cam.pitch);
    camera.position.set(cam.t[0] + cam.d * cp * Math.sin(cam.yaw), cam.t[1] + cam.d * Math.sin(cam.pitch), cam.t[2] + cam.d * cp * Math.cos(cam.yaw));
    camera.lookAt(cam.t[0], cam.t[1], cam.t[2]);
    /* décalage de cadrage : la maquette laisse la place au texte (chapitres à gauche, panneau à
     * droite sur grand écran ; cartes en bas sur mobile) */
    const CW = canvas.clientWidth, CH_ = canvas.clientHeight, wide = CW >= 960;
    const sx = !story ? 0 : wide ? (exploring ? -0.17 : 0.16) : 0, sy = !story ? 0 : wide ? 0 : 0.14;
    viewX = reduce ? sx : C.damp(viewX, sx, 0.35, dt); viewY = reduce ? sy : C.damp(viewY, sy, 0.35, dt);
    if (Math.abs(viewX - sx) > 1e-3 || Math.abs(viewY - sy) > 1e-3) settled = false;
    if (CW && CH_) camera.setViewOffset(CW, CH_, -viewX * CW, viewY * CH_, CW, CH_);
    camera.updateMatrixWorld();

    /* paquets : l'étage raconté, ou les pistes du composant visé */
    let n = 0;
    const active = exploring && focus >= 0 ? PARTS[focus].id : null;
    const runs = (t) => (active ? t.ends.indexOf(active) >= 0 || active === "policy" : !exploring && chapter >= 0 && t.layer === chapter);
    if (!reduce && (active || (!exploring && chapter >= 0))) {
      if (!t0) t0 = time;
      const tt = time - t0;
      TRACES.forEach((t) => {
        if (!runs(t)) return;
        const y = 0.08 + (t.layer >= 0 ? explode * GAP * t.layer : 0);
        for (let j = 0; j < 3 && n < MAXP; j++) {
          const u = ((tt * 2.2 / t.len) + j / 3) % 1 * t.len;
          for (const sg of t.segs) {
            if (u <= sg.o + sg.l) {
              const f = (u - sg.o) / sg.l;
              pkPos.set([sg.a[0] + (sg.b[0] - sg.a[0]) * f, y, sg.a[1] + (sg.b[1] - sg.a[1]) * f], n * 3);
              n++; break;
            }
          }
        }
      });
      pkGeo.attributes.position.needsUpdate = true;
    }
    pkGeo.setDrawRange(0, n);
    st.renderer.render(scene, camera);

    /* Étiquettes HTML calées sur la projection des ancrages. */
    const W = canvas.clientWidth, H = canvas.clientHeight;
    PARTS.forEach((pt, i) => {
      const s = project(pt.anchor), b = pins[i];
      const off = s[2] > 1 || s[0] < 0 || s[1] < 0 || s[0] > W || s[1] > H || (!exploring && !(chapter >= 0 && pt.layer === chapter));
      b.classList.toggle("is-hidden", off);
      b.style.transform = "translate(" + s[0].toFixed(1) + "px," + s[1].toFixed(1) + "px) translate(-50%, calc(-100% - 10px))";
      b.style.zIndex = i === focus ? 2 : 1;
    });
    if (!shown) { shown = true; stageEl.classList.add("is-3d"); }
    return story || dragging || !settled || n > 0;
  }

  /* Orbite à la souris ou au doigt ; un clic sans glisser vise le composant le plus proche. */
  canvas.addEventListener("pointerdown", (e) => { if (!exploring) return; dragging = true; moved = 0; lx = e.clientX; ly = e.clientY; canvas.setPointerCapture(e.pointerId); });
  canvas.addEventListener("pointermove", (e) => {
    if (!dragging) return;
    const dx = e.clientX - lx, dy = e.clientY - ly; lx = e.clientX; ly = e.clientY;
    moved += Math.abs(dx) + Math.abs(dy);
    if (moved > 4) stopTour();
    goal.yaw -= dx * 0.008; goal.pitch = C.clamp(goal.pitch + dy * 0.006, 0.25, 1.45);
    cam.yaw = goal.yaw; cam.pitch = goal.pitch;
    st.request();
  });
  const end = (e) => {
    if (!dragging) return;
    dragging = false;
    try { canvas.releasePointerCapture(e.pointerId); } catch (x) {}
    if (moved < 6) {
      const r = canvas.getBoundingClientRect(), mx = e.clientX - r.left, my = e.clientY - r.top;
      let best = -1, bd = 70;
      PARTS.forEach((p, i) => { const s = project(p.anchor), dd = Math.hypot(s[0] - mx, s[1] - my + 20); if (dd < bd) { bd = dd; best = i; } });
      stopTour(); select(best);
    }
    st.request();
  };
  canvas.addEventListener("pointerup", end);
  canvas.addEventListener("pointercancel", end);
  canvas.addEventListener("keydown", (e) => {
    if (!exploring) return;
    if (e.key === "ArrowRight") { stopTour(); step(1); }
    else if (e.key === "ArrowLeft") { stopTour(); step(-1); }
    else if (e.key === "Escape") { stopTour(); select(-1); }
    else if (e.key === "Enter" || e.key === " ") { if (playing) stopTour(); else startTour(); }
    else return;
    e.preventDefault();
  });

  const st = C.stage(THREE, canvas, {
    render,
    resize(w, h) { camera.aspect = w / h; camera.updateProjectionMatrix(); goal.d = (focus < 0 ? OVER.d : PARTS[focus].d) * fitFactor(); },
    lost() { stopTour(); stageEl.classList.remove("is-3d"); stageEl.classList.add("is-fallback"); },
  });
  select(-1);
  if (story) addEventListener("scroll", () => st.request(), { passive: true });
}
