/* tenon · la preuve : la chaîne du journal d'audit se construit au défilement.
 * Douze vrais types d'événements de tenon (internal/event), une ligne par bloc. Chaque bloc
 * porte le hash de la ligne précédente et le sien, calculés ici comme tenon les calcule
 * (internal/audit : sha256(prev + ligne JSON), genèse « sha256:0 »). Puis tenon audit verify
 * parcourt la chaîne : chaque bloc vérifié passe à l'or. Le journal HTML à côté suit le récit. */
export function mount(fig, THREE, C) {
  fig.classList.add("is-3d");
  const canvas = fig.querySelector("canvas");
  const track = fig.querySelector(".h-chain__track");
  const items = Array.prototype.slice.call(fig.querySelectorAll(".h-chain__events li[data-type]"));
  const proofs = Array.prototype.slice.call(fig.querySelectorAll(".h-chain__proofs li"));
  const verifyEl = fig.querySelector(".h-chain__verify");
  const col = C.colors(THREE), F = C.fonts();
  const scroll = !C.reduce;
  if (scroll) fig.classList.add("is-scroll");
  const N = items.length;

  /* ── Les lignes et leurs hash ─────────────────────────────── */
  const LINES = items.map((li, i) => ({ seq: i + 1, ts: "2026-10-31T09:14:" + String(10 + i * 3).padStart(2, "0") + "Z", session: "s-7f3a91",
    type: li.getAttribute("data-type"), payload: { detail: li.getAttribute("data-detail") }, prev: "", hash: "" }));
  async function sha256(str) {
    if (window.crypto && crypto.subtle && window.isSecureContext) {
      const buf = await crypto.subtle.digest("SHA-256", new TextEncoder().encode(str));
      return Array.from(new Uint8Array(buf)).map((b) => b.toString(16).padStart(2, "0")).join("");
    }
    let h = 2166136261; for (let j = 0; j < str.length; j++) { h ^= str.charCodeAt(j); h = Math.imul(h, 16777619) >>> 0; }   /* repli hors HTTPS */
    return ("00000000" + h.toString(16)).slice(-8).repeat(8);
  }
  async function chainHashes() {
    let prev = "sha256:0";
    for (const l of LINES) {
      l.prev = prev;
      const json = JSON.stringify({ seq: l.seq, ts: l.ts, session: l.session, type: l.type, payload: l.payload, prev: l.prev });
      l.hash = "sha256:" + (await sha256(l.prev + json));
      prev = l.hash;
    }
  }
  const short = (h) => (h === "sha256:0" ? "sha256:0" : h.slice(7, 15));

  /* ── Scène ─────────────────────────────────────────────────── */
  const scene = new THREE.Scene();
  const camera = new THREE.PerspectiveCamera(35, 1, 0.1, 200);
  scene.add(new THREE.AmbientLight(0xffffff, 1.0));
  const key = new THREE.DirectionalLight(0xffffff, 2.0); key.position.set(4, 8, 10); scene.add(key);
  const pulse = new THREE.PointLight(col.gold, 0, 9); scene.add(pulse);
  const helix = new THREE.Group(); scene.add(helix);

  /* La chaîne se lit comme un texte : un serpentin de trois blocs par rangée, faces vers vous,
   * avec une ondulation en profondeur ; sur un cadre très large, une diagonale montante. */
  let wide = false;
  const mid = (N - 1) / 2, COLS = 3, ROWS = Math.ceil(N / COLS);
  const posOf = (i, out) => {
    if (wide) return out.set((i - mid) * 2.05, (i - mid) * 0.62, Math.sin(i * 0.9) * 0.9);
    const r = Math.floor(i / COLS), c = r % 2 ? COLS - 1 - (i % COLS) : i % COLS;
    return out.set((c - (COLS - 1) / 2) * 2.85, ((ROWS - 1) / 2 - r) * 1.8, Math.sin(i * 0.9) * 0.5);
  };

  const rr = new THREE.Shape(), bw = 2.4, bh = 1.25, r = 0.16;
  rr.moveTo(-bw / 2 + r, -bh / 2); rr.lineTo(bw / 2 - r, -bh / 2); rr.quadraticCurveTo(bw / 2, -bh / 2, bw / 2, -bh / 2 + r);
  rr.lineTo(bw / 2, bh / 2 - r); rr.quadraticCurveTo(bw / 2, bh / 2, bw / 2 - r, bh / 2); rr.lineTo(-bw / 2 + r, bh / 2);
  rr.quadraticCurveTo(-bw / 2, bh / 2, -bw / 2, bh / 2 - r); rr.lineTo(-bw / 2, -bh / 2 + r); rr.quadraticCurveTo(-bw / 2, -bh / 2, -bw / 2 + r, -bh / 2);
  const blockGeo = new THREE.ExtrudeGeometry(rr, { depth: 0.7, bevelEnabled: true, bevelThickness: 0.06, bevelSize: 0.06, bevelSegments: 2, curveSegments: 6 });
  blockGeo.translate(0, 0, -0.35);
  const edgesGeo = new THREE.EdgesGeometry(blockGeo, 30);
  const faceGeo = new THREE.PlaneGeometry(bw - 0.16, bh - 0.14);

  /* Face d'un bloc : type, hash précédent, hash — dessinés sur un canvas (polices du site). */
  function face(i) {
    const c = document.createElement("canvas"); c.width = 512; c.height = 268;
    const tex = new THREE.CanvasTexture(c); tex.colorSpace = THREE.SRGBColorSpace; tex.anisotropy = 4;
    return { c, tex };
  }
  const css = (k) => "#" + col[k].getHexString(THREE.SRGBColorSpace);
  function drawFace(b, verified) {
    const g = b.face.c.getContext("2d"), l = LINES[b.i];
    g.clearRect(0, 0, 512, 268);
    g.fillStyle = css("night2"); g.fillRect(0, 0, 512, 268);
    g.textBaseline = "alphabetic";
    g.font = "500 26px " + F.mono; g.fillStyle = css("muted"); g.fillText("#" + String(l.seq).padStart(2, "0"), 26, 48);
    g.font = "500 40px " + F.mono; g.fillStyle = css(verified ? "gold" : "on");
    let t = l.type; while (g.measureText(t).width > 460 && t.length > 4) t = t.slice(0, -2) + "…";
    g.fillText(t, 26, 110);
    g.font = "400 26px " + F.mono; g.fillStyle = css("muted"); g.fillText("prev " + (l.prev ? short(l.prev) : "…"), 26, 170);
    g.fillStyle = css("gold"); g.fillText("hash " + (l.hash ? short(l.hash) : "…"), 26, 216);
    if (verified) { g.font = "500 40px " + F.mono; g.fillStyle = css("gold"); g.fillText("✓", 456, 214); }
    b.face.tex.needsUpdate = true;
  }

  const blocks = [];
  const v = new THREE.Vector3(), v2 = new THREE.Vector3(), up = new THREE.Vector3(0, 1, 0);
  for (let i = 0; i < N; i++) {
    const g = new THREE.Group();
    const body = new THREE.MeshStandardMaterial({ color: col.night2, roughness: 0.55, metalness: 0.25, emissive: col.gold, emissiveIntensity: 0 });
    const edge = new THREE.LineBasicMaterial({ color: col.edge, transparent: true, opacity: 0.9 });
    g.add(new THREE.Mesh(blockGeo, body), new THREE.LineSegments(edgesGeo, edge));
    const f = face(i);
    const fm = new THREE.Mesh(faceGeo, new THREE.MeshBasicMaterial({ map: f.tex, transparent: true }));
    fm.position.z = 0.42; g.add(fm);
    g.rotation.set(0.04 * Math.sin(i * 1.3), -0.18 + 0.1 * Math.cos(i * 0.7), 0);
    helix.add(g);
    blocks.push({ i, g, body, edge, face: f, verified: false, home: new THREE.Vector3() });
  }
  /* Maillons : deux anneaux entrelacés entre deux blocs consécutifs. */
  const linkGeo = new THREE.TorusGeometry(0.2, 0.055, 8, 20);
  const linkMat = new THREE.MeshStandardMaterial({ color: col.edge, roughness: 0.35, metalness: 0.6, emissive: col.gold, emissiveIntensity: 0 });
  const links = [];
  for (let i = 0; i < N - 1; i++) {
    const a = new THREE.Mesh(linkGeo, linkMat), b = new THREE.Mesh(linkGeo, linkMat);
    const grp = new THREE.Group(); grp.add(a, b); helix.add(grp); links.push(grp);
  }
  const X = new THREE.Vector3(1, 0, 0), qx = new THREE.Quaternion().setFromAxisAngle(X, Math.PI / 2);
  function layout() {
    wide = camera.aspect > 1.45;
    blocks.forEach((b) => posOf(b.i, b.home));
    links.forEach((grp, i) => {
      posOf(i, v); posOf(i + 1, v2);
      const dir = v2.clone().sub(v).normalize(), m = v.clone().add(v2).multiplyScalar(0.5);
      const q = new THREE.Quaternion().setFromUnitVectors(X, dir);
      grp.children[0].position.copy(m).addScaledVector(dir, -0.17); grp.children[0].quaternion.copy(q);
      grp.children[1].position.copy(m).addScaledVector(dir, 0.17); grp.children[1].quaternion.copy(q).multiply(qx);
    });
  }

  /* ── Récit ─────────────────────────────────────────────────── */
  const BUILD = 0.78, VER0 = 0.82, VER1 = 0.97;
  const tan = Math.tan(camera.fov * Math.PI / 360);
  let prog = scroll ? 0 : 1, spin = 0, lastOn = -2, lastVer = -1, lastProof = -1;
  const tgt = new THREE.Vector3(), eye = new THREE.Vector3(), camT = new THREE.Vector3(), camE = new THREE.Vector3();
  let camInit = false;

  function render(dt) {
    const target = scroll ? C.pinProgress(track) : 1;
    prog = scroll ? C.damp(prog, target, 0.18, dt) : 1;
    const p = prog, built = C.clamp(p / BUILD, 0, 1) * N;          /* blocs posés (fractionnaire) */
    const ver = C.clamp((p - VER0) / (VER1 - VER0), 0, 1) * N;     /* blocs vérifiés */

    blocks.forEach((b) => {
      const k = C.clamp(built - b.i, 0, 1), e = C.easeOut(k);
      b.g.visible = k > 0;
      b.g.position.copy(b.home); b.g.position.y += (1 - e) * 2.2;
      b.g.scale.setScalar(0.5 + 0.5 * e);
      const isVer = ver > b.i + 0.5;
      if (isVer !== b.verified) { b.verified = isVer; drawFace(b, isVer); }
      b.edge.color.copy(isVer ? col.gold : col.edge);
      const near = Math.max(0, 1 - Math.abs(ver - b.i - 0.5));
      b.body.emissiveIntensity = ver > 0 && ver < N ? near * 0.35 : 0;
    });
    links.forEach((l, i) => { l.visible = built > i + 1.4; });
    linkMat.emissiveIntensity = ver >= N ? 0.35 : 0;
    linkMat.color.copy(ver >= N ? col.gold : col.edge);
    if (ver > 0 && ver < N) { posOf(Math.min(N - 1, ver), v); pulse.position.copy(v).add(new THREE.Vector3(0, 0.6, 2)); pulse.intensity = 40; } else pulse.intensity = 0;

    /* caméra : elle suit le dernier bloc posé, puis recule pour montrer toute la chaîne */
    const cur = C.clamp(built - 0.6, 0, N - 1);
    const asp = camera.aspect;
    const ex = wide ? (N - 1) * 2.05 / 2 + 1.6 : (COLS - 1) * 2.85 / 2 + 1.5, ey = wide ? (N - 1) * 0.62 / 2 + 1.2 : (ROWS - 1) * 1.8 / 2 + 1.0;
    const whole = Math.max(ex / (tan * asp), ey / tan) * 1.06;
    const back = C.easeIO(C.smooth(BUILD - 0.04, VER0 + 0.06, p));
    posOf(cur, v);
    const nearD = Math.max(7.5, 3.9 / (tan * Math.min(1, asp)));
    tgt.set(C.lerp(v.x, 0, back), C.lerp(v.y, 0, back), C.lerp(v.z, 0, back));
    const yaw = C.lerp(-0.42, -0.16, back) + (scroll ? Math.sin(spin) * 0.18 : 0), dist = C.lerp(nearD, whole, back);
    eye.set(tgt.x + Math.sin(yaw) * dist, tgt.y + C.lerp(1.2, 0.8, back), tgt.z + Math.cos(yaw) * dist);
    if (!camInit || !scroll) { camT.copy(tgt); camE.copy(eye); camInit = true; }
    else { camT.lerp(tgt, 1 - Math.exp(-dt / 0.2)); camE.lerp(eye, 1 - Math.exp(-dt / 0.2)); }
    camera.position.copy(camE); camera.lookAt(camT);
    if (scroll && p > 0.985) spin += dt * 0.5;                      /* en fin de récit, la caméra se balance doucement */

    st.renderer.render(scene, camera);

    /* journal HTML */
    const on = Math.floor(built + 0.02) - 1;
    if (on !== lastOn) {
      lastOn = on;
      items.forEach((li, i) => { li.classList.toggle("is-on", i <= on); li.classList.toggle("is-new", i === on && on < N - 1); });
    }
    const vk = ver >= N ? 1 : 0;
    if (vk !== lastVer) { lastVer = vk; verifyEl.classList.toggle("is-on", !!vk); }
    const pk = p >= 0.985 ? 3 : p >= VER1 ? 2 : p >= VER0 ? 1 : 0;
    if (pk !== lastProof) { lastProof = pk; proofs.forEach((li, i) => li.classList.toggle("is-on", i < pk)); }
    return true;
  }

  const st = C.stage(THREE, canvas, {
    render,
    resize(w, h) { camera.aspect = w / h; camera.updateProjectionMatrix(); layout(); camInit = false; },
    lost() { fig.classList.remove("is-3d", "is-scroll"); },
  });
  if (scroll) addEventListener("scroll", () => st.request(), { passive: true });

  /* Polices puis hash : les faces et le journal affichent les vraies valeurs. */
  const ready = document.fonts && document.fonts.load ? Promise.all([document.fonts.load("500 40px " + F.mono), document.fonts.load("400 26px " + F.mono)]).catch(() => {}) : Promise.resolve();
  Promise.all([ready, chainHashes()]).then(() => {
    blocks.forEach((b) => drawFace(b, b.verified));
    items.forEach((li, i) => { const em = li.querySelector("em"); if (em) { em.textContent = short(LINES[i].hash); em.title = LINES[i].hash; } });
    st.request();
  });
  blocks.forEach((b) => drawFace(b, false));
  st.request();
}
