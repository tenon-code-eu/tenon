/* tenon · socle commun des scènes 3D : rendu, boucle, pause, couleurs, formes.
 * Règles partagées par toutes les scènes :
 *  - pixelRatio plafonné à 2 ;
 *  - aucune image calculée hors écran (IntersectionObserver) ni onglet caché ;
 *  - mouvement réduit : une seule image fixe, l'état final de la scène ;
 *  - perte du contexte WebGL : la figure revient à sa version HTML. */
export const reduce = window.matchMedia("(prefers-reduced-motion: reduce)").matches;

export const clamp = (x, a, b) => Math.max(a, Math.min(b, x));
export const lerp = (a, b, k) => a + (b - a) * k;
export const smooth = (a, b, t) => { const x = clamp((t - a) / (b - a), 0, 1); return x * x * (3 - 2 * x); };
export const easeOut = (x) => 1 - Math.pow(1 - clamp(x, 0, 1), 3);
export const easeIO = (x) => { x = clamp(x, 0, 1); return x < 0.5 ? 4 * x * x * x : 1 - Math.pow(-2 * x + 2, 3) / 2; };
/* Approche amortie, indépendante de la cadence d'affichage. */
export const damp = (a, b, tau, dt) => lerp(a, b, 1 - Math.exp(-dt / tau));

/* Couleurs lues dans tokens.css (oklch → sRGB linéaire) : aucune couleur nouvelle. */
function oklchToLinear(str) {
  const m = /oklch\(\s*([\d.]+)%\s+([\d.]+)\s+([\d.]+)/.exec(str || "");
  if (!m) return [0.5, 0.5, 0.5];
  const L = parseFloat(m[1]) / 100, C = parseFloat(m[2]), h = parseFloat(m[3]) * Math.PI / 180;
  const a = C * Math.cos(h), b = C * Math.sin(h);
  const l = Math.pow(L + 0.3963377774 * a + 0.2158037573 * b, 3);
  const mm = Math.pow(L - 0.1055613458 * a - 0.0638541728 * b, 3);
  const s = Math.pow(L - 0.0894841775 * a - 1.2914855480 * b, 3);
  const c = (x) => clamp(x, 0, 1);
  return [c(4.0767416621 * l - 3.3077115913 * mm + 0.2309699292 * s),
          c(-1.2684380046 * l + 2.6097574011 * mm - 0.3413193965 * s),
          c(-0.0041960863 * l - 0.7034186147 * mm + 1.7076147010 * s)];
}
export function colors(THREE) {
  const css = getComputedStyle(document.documentElement);
  const tok = (n) => { const [r, g, b] = oklchToLinear(css.getPropertyValue("--color-" + n)); return new THREE.Color().setRGB(r, g, b, THREE.LinearSRGBColorSpace); };
  const hex = (h) => new THREE.Color(h);   /* couleurs du logo, reprises de mark.svg */
  return {
    night: tok("night"), night2: tok("night-2"), rule: tok("night-rule"), on: tok("on-night"), muted: tok("on-night-muted"),
    gold: tok("gold"), accent: tok("accent"), danger: tok("danger"), post: tok("3d-post"), rail: tok("3d-rail"), edge: tok("3d-edge"),
    markTop: hex("#1f4bb0"), markBottom: hex("#0c1f52"), markTrace: hex("#5fd8ff"), markNode: hex("#7fe3ff"),
  };
}
export function fonts() {
  const css = getComputedStyle(document.documentElement);
  return { display: css.getPropertyValue("--font-display").trim(), body: css.getPropertyValue("--font-body").trim(), mono: css.getPropertyValue("--font-mono").trim() };
}

/* Rendu et boucle d'une scène. render(dt, t) dessine et renvoie true s'il faut une autre image. */
export function stage(THREE, canvas, opts) {
  const renderer = new THREE.WebGLRenderer({ canvas, antialias: true, alpha: true, powerPreference: "high-performance" });
  renderer.setPixelRatio(Math.min(window.devicePixelRatio || 1, 2));
  renderer.setClearColor(0x000000, 0);
  const st = { renderer, width: 0, height: 0, onScreen: false, request, dispose };
  let raf = 0, last = 0, clock = 0;
  function size() {
    const w = canvas.clientWidth, h = canvas.clientHeight;
    if (!w || !h || (w === st.width && h === st.height)) return;
    st.width = w; st.height = h;
    renderer.setSize(w, h, false);
    if (opts.resize) opts.resize(w, h);
  }
  function frame(now) {
    raf = 0;
    const dt = last ? Math.min(0.05, (now - last) / 1000) : 1 / 60;
    last = now; clock += dt;
    size();
    const more = opts.render(dt, clock) && !reduce;
    if (more && st.onScreen && !document.hidden) request(); else last = 0;
  }
  function request() { if (!raf && st.onScreen && !document.hidden) raf = requestAnimationFrame(frame); }
  function dispose() { if (raf) cancelAnimationFrame(raf); raf = 0; renderer.dispose(); }
  if ("IntersectionObserver" in window) {
    new IntersectionObserver((en) => { st.onScreen = en[0].isIntersecting; if (st.onScreen) request(); else last = 0; }).observe(canvas);
  } else st.onScreen = true;
  document.addEventListener("visibilitychange", () => { if (document.hidden) last = 0; else request(); });
  if ("ResizeObserver" in window) new ResizeObserver(request).observe(canvas); else addEventListener("resize", request);
  canvas.addEventListener("webglcontextlost", (e) => { e.preventDefault(); dispose(); if (opts.lost) opts.lost(); });
  return st;
}

/* Avancement 0 → 1 d'un bloc « collant » : 0 quand son haut touche le haut de l'écran,
 * 1 quand son bas touche le bas. */
export function pinProgress(el) {
  const r = el.getBoundingClientRect(), span = r.height - window.innerHeight;
  return span > 0 ? clamp(-r.top / span, 0, 1) : 1;
}

/* Halo radial dessiné à la volée (particules, lueurs) : aucune image externe. */
export function glowTexture(THREE) {
  const c = document.createElement("canvas"); c.width = c.height = 64;
  const g = c.getContext("2d"), grd = g.createRadialGradient(32, 32, 0, 32, 32, 32);
  grd.addColorStop(0, "rgba(255,255,255,1)"); grd.addColorStop(0.25, "rgba(255,255,255,0.85)");
  grd.addColorStop(0.6, "rgba(255,255,255,0.18)"); grd.addColorStop(1, "rgba(255,255,255,0)");
  g.fillStyle = grd; g.fillRect(0, 0, 64, 64);
  const t = new THREE.CanvasTexture(c); t.colorSpace = THREE.SRGBColorSpace; return t;
}

/* Étoile à cinq branches (drapeau européen : rayon intérieur ≈ 0,382 × extérieur). */
export function starShape(THREE, R) {
  const s = new THREE.Shape(), r = R * 0.382;
  for (let i = 0; i < 10; i++) {
    const a = Math.PI / 2 + i * Math.PI / 5, rad = i % 2 ? r : R;
    if (i === 0) s.moveTo(Math.cos(a) * rad, Math.sin(a) * rad); else s.lineTo(Math.cos(a) * rad, Math.sin(a) * rad);
  }
  s.closePath(); return s;
}
export function starGeometry(THREE, R, depth) {
  const g = new THREE.ExtrudeGeometry(starShape(THREE, R), { depth, bevelEnabled: true, bevelThickness: depth * 0.45, bevelSize: R * 0.06, bevelSegments: 2 });
  g.center(); return g;
}

/* Hexagone du bouclier tenon (mark.svg, viewBox 64) ramené au centre, unité = u. */
export const HEX = [[32, 2.5], [57.5, 17.2], [57.5, 46.8], [32, 61.5], [6.5, 46.8], [6.5, 17.2]];
export function hexShape(THREE, u, grow) {
  const s = new THREE.Shape(), k = 1 + (grow || 0) / 29.5;
  HEX.forEach(([x, y], i) => { const X = (x - 32) * u * k, Y = -(y - 32) * u * k; if (i === 0) s.moveTo(X, Y); else s.lineTo(X, Y); });
  s.closePath(); return s;
}
/* Anneau hexagonal (contour du bouclier) d'épaisseur w (unités du SVG). */
export function hexRing(THREE, u, w, depth) {
  const outer = hexShape(THREE, u, w / 2), inner = hexShape(THREE, u, -w / 2);
  outer.holes.push(new THREE.Path(inner.getPoints().reverse()));
  const g = new THREE.ExtrudeGeometry(outer, { depth, bevelEnabled: false });
  g.translate(0, 0, -depth / 2); return g;
}

/* Étiquettes HTML calées sur des points 3D. */
export function placeLabels(THREE, camera, w, h, items) {
  const v = new THREE.Vector3();
  items.forEach((it) => {
    v.copy(it.at).project(camera);
    const x = (v.x * 0.5 + 0.5) * w, y = (1 - (v.y * 0.5 + 0.5)) * h, hidden = v.z > 1 || it.hide;
    it.el.style.transform = "translate(" + x.toFixed(1) + "px," + y.toFixed(1) + "px) " + (it.anchor || "translate(-50%, -100%)");
    it.el.classList.toggle("is-off", !!hidden);
  });
}
