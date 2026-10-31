/* tenon · page d'accueil : copie, onglets, palette ⌘K et assemblage 3D (WebGL écrit à la main, sans bibliothèque). */
(function () {
  "use strict";
  var reduce = window.matchMedia("(prefers-reduced-motion: reduce)").matches;

  /* ── Copie : succès silencieux, libellé échangé 1,5 s ─────────────── */
  function copyText(btn) {
    var src = btn.getAttribute("data-copy-from"), from = src && document.getElementById(src);
    var text = from ? from.textContent : btn.getAttribute("data-copy");
    var done = function () {
      var label = btn.textContent;
      btn.setAttribute("data-state", "done");
      if (!btn.classList.contains("h-cmdk__item")) btn.textContent = btn.getAttribute("data-copied") || label;
      setTimeout(function () { btn.removeAttribute("data-state"); btn.textContent = label; }, 1500);
    };
    if (navigator.clipboard && window.isSecureContext) {
      navigator.clipboard.writeText(text).then(done, function () {});
    } else {
      var ta = document.createElement("textarea");
      ta.value = text; ta.setAttribute("readonly", ""); ta.style.position = "fixed"; ta.style.opacity = "0";
      document.body.appendChild(ta); ta.select();
      try { document.execCommand("copy"); done(); } catch (e) {}
      document.body.removeChild(ta);
    }
  }
  document.querySelectorAll(".h-copy").forEach(function (b) {
    b.addEventListener("click", function () { copyText(b); });
  });

  /* ── Onglets d'installation (flèches gauche/droite, sans saut de défilement) ── */
  document.querySelectorAll("[data-tabs]").forEach(function (root) {
    var tabs = Array.prototype.slice.call(root.querySelectorAll('[role="tab"]'));
    function select(tab) {
      tabs.forEach(function (t) {
        var on = t === tab;
        t.setAttribute("aria-selected", on ? "true" : "false");
        t.tabIndex = on ? 0 : -1;
        document.getElementById(t.getAttribute("aria-controls")).hidden = !on;
      });
      tab.focus({ preventScroll: true });
    }
    tabs.forEach(function (t, i) {
      t.addEventListener("click", function () { select(t); });
      t.addEventListener("keydown", function (e) {
        if (e.key === "ArrowRight") { e.preventDefault(); select(tabs[(i + 1) % tabs.length]); }
        if (e.key === "ArrowLeft") { e.preventDefault(); select(tabs[(i - 1 + tabs.length) % tabs.length]); }
      });
    });
  });

  /* ── Film de lancement : lecture muette quand il est à l'écran, pause sinon.
   * Jamais en mouvement réduit ; une pause demandée par le lecteur est respectée. ── */
  var film = document.getElementById("h-film");
  if (film && !reduce && "IntersectionObserver" in window) {
    var filmVis = false, autoPause = false, userStop = false;
    var playFilm = function () { if (filmVis && !userStop && !document.hidden && film.paused) { film.muted = true; var p = film.play(); if (p && p.catch) p.catch(function () {}); } };
    var pauseFilm = function () { if (!film.paused) { autoPause = true; film.pause(); } };
    film.addEventListener("pause", function () { if (!autoPause) userStop = true; autoPause = false; });
    film.addEventListener("play", function () { userStop = false; });
    new IntersectionObserver(function (en) { filmVis = en[0].isIntersecting; if (filmVis) playFilm(); else pauseFilm(); }, { threshold: 0.4 }).observe(film);
    document.addEventListener("visibilitychange", function () { if (document.hidden) pauseFilm(); else playFilm(); });
  }

  /* ── Palette ⌘K ─────────────────────────────────────────────────────── */
  var cmdk = document.getElementById("h-cmdk");
  var opener = document.getElementById("h-search");
  if (cmdk) {
    var input = document.getElementById("h-cmdk-input");
    var items = Array.prototype.slice.call(cmdk.querySelectorAll(".h-cmdk__item"));
    var groups = Array.prototype.slice.call(cmdk.querySelectorAll(".h-cmdk__group"));
    var empty = cmdk.querySelector(".h-cmdk__empty");
    var lastFocus = null, active = 0;
    var visible = function () { return items.filter(function (it) { return !it.hidden; }); };
    var mark = function () {
      var v = visible();
      items.forEach(function (it) { it.classList.remove("is-active"); it.setAttribute("aria-selected", "false"); });
      if (v.length) {
        active = Math.max(0, Math.min(active, v.length - 1));
        v[active].classList.add("is-active"); v[active].setAttribute("aria-selected", "true");
        v[active].scrollIntoView({ block: "nearest" });
      }
    };
    var filter = function () {
      var q = input.value.trim().toLowerCase();
      items.forEach(function (it) { it.hidden = q !== "" && it.textContent.toLowerCase().indexOf(q) < 0; });
      groups.forEach(function (g) {
        var n = g.nextElementSibling, any = false;
        while (n && n.classList.contains("h-cmdk__item")) { if (!n.hidden) any = true; n = n.nextElementSibling; }
        g.hidden = !any;
      });
      empty.hidden = visible().length > 0;
      active = 0; mark();
    };
    var open = function () {
      lastFocus = document.activeElement;
      cmdk.classList.add("is-open"); cmdk.setAttribute("aria-hidden", "false");
      document.documentElement.style.overflow = "hidden";
      input.value = ""; filter();
      input.focus({ preventScroll: true });
    };
    var close = function () {
      cmdk.classList.remove("is-open"); cmdk.setAttribute("aria-hidden", "true");
      document.documentElement.style.overflow = "";
      if (lastFocus) lastFocus.focus({ preventScroll: true });
    };
    var run = function (it) {
      if (!it) return;
      if (it.hasAttribute("data-copy")) { copyText(it); setTimeout(close, 450); return; }
      window.location.href = it.getAttribute("data-href");
    };
    if (opener) opener.addEventListener("click", open);
    cmdk.querySelector("[data-close]").addEventListener("click", close);
    input.addEventListener("input", filter);
    items.forEach(function (it) {
      it.addEventListener("click", function () { run(it); });
      it.addEventListener("mousemove", function () { active = visible().indexOf(it); mark(); });
    });
    cmdk.addEventListener("keydown", function (e) {
      if (e.key === "Escape") { e.preventDefault(); close(); }
      else if (e.key === "ArrowDown") { e.preventDefault(); active++; if (active >= visible().length) active = 0; mark(); }
      else if (e.key === "ArrowUp") { e.preventDefault(); active--; if (active < 0) active = visible().length - 1; mark(); }
      else if (e.key === "Enter") { e.preventDefault(); run(visible()[active]); }
      else if (e.key === "Tab") { e.preventDefault(); input.focus(); }
    });
    document.addEventListener("keydown", function (e) {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k") {
        e.preventDefault();
        if (cmdk.classList.contains("is-open")) close(); else open();
      }
    });
  }

  /* ── Maquette 3D du harness : vue de haut, visite composant par composant ── */
  var stage = document.getElementById("h-stage");
  var canvas = document.getElementById("h-harness");
  var pinsBox = document.getElementById("h-pins");
  var dataEl = document.getElementById("h-parts");
  if (!stage || !canvas || !dataEl) return;
  var PARTS_TXT = JSON.parse(dataEl.textContent);
  var gl = canvas.getContext("webgl", { antialias: true, alpha: true, premultipliedAlpha: true });
  if (!gl) return;

  /* Couleurs lues depuis les jetons CSS (oklch → sRGB linéaire). */
  function oklchToLinear(str) {
    var m = /oklch\(\s*([\d.]+)%\s+([\d.]+)\s+([\d.]+)/.exec(str || "");
    if (!m) return [0.5, 0.5, 0.5];
    var L = parseFloat(m[1]) / 100, C = parseFloat(m[2]), h = parseFloat(m[3]) * Math.PI / 180;
    var a = C * Math.cos(h), b = C * Math.sin(h);
    var l = Math.pow(L + 0.3963377774 * a + 0.2158037573 * b, 3);
    var mm = Math.pow(L - 0.1055613458 * a - 0.0638541728 * b, 3);
    var s = Math.pow(L - 0.0894841775 * a - 1.2914855480 * b, 3);
    var c = function (x) { return Math.max(0, Math.min(1, x)); };
    return [c(4.0767416621 * l - 3.3077115913 * mm + 0.2309699292 * s),
            c(-1.2684380046 * l + 2.6097574011 * mm - 0.3413193965 * s),
            c(-0.0041960863 * l - 0.7034186147 * mm + 1.7076147010 * s)];
  }
  var css = getComputedStyle(document.documentElement);
  var tok = function (n) { return oklchToLinear(css.getPropertyValue(n)); };
  var COL = { board: tok("--color-night-2"), grid: tok("--color-night-rule"), part: tok("--color-3d-rail"),
              dim: tok("--color-3d-post"), edge: tok("--color-3d-edge"), gold: tok("--color-gold") };

  /* Algèbre 4×4, colonnes majeures. */
  function mul(a, b) {
    var o = new Float32Array(16);
    for (var c = 0; c < 4; c++) for (var r = 0; r < 4; r++)
      o[c * 4 + r] = a[r] * b[c * 4] + a[4 + r] * b[c * 4 + 1] + a[8 + r] * b[c * 4 + 2] + a[12 + r] * b[c * 4 + 3];
    return o;
  }
  function perspective(fov, asp, n, f) {
    var t = 1 / Math.tan(fov / 2), o = new Float32Array(16);
    o[0] = t / asp; o[5] = t; o[10] = (f + n) / (n - f); o[11] = -1; o[14] = 2 * f * n / (n - f); return o;
  }
  function lookAt(e, t) {
    var fx = t[0] - e[0], fy = t[1] - e[1], fz = t[2] - e[2], fl = Math.hypot(fx, fy, fz);
    fx /= fl; fy /= fl; fz /= fl;
    var sx = -fz, sy = 0, sz = fx, sl = Math.hypot(sx, sz) || 1; sx /= sl; sz /= sl;   /* s = f × (0,1,0) */
    var ux = sy * fz - sz * fy, uy = sz * fx - sx * fz, uz = sx * fy - sy * fx;
    return new Float32Array([sx, ux, -fx, 0, sy, uy, -fy, 0, sz, uz, -fz, 0,
      -(sx * e[0] + sy * e[1] + sz * e[2]), -(ux * e[0] + uy * e[1] + uz * e[2]), fx * e[0] + fy * e[1] + fz * e[2], 1]);
  }

  /* Géométrie : pavés posés sur la carte (y = 0). B(cx, y0, cz, sx, sy, sz). */
  function B(cx, y0, cz, sx, sy, sz) { return [cx - sx / 2, cx + sx / 2, y0, y0 + sy, cz - sz / 2, cz + sz / 2]; }
  function mesh(list) {
    var tri = [], lines = [];
    list.forEach(function (b) {
      var x0 = b[0], x1 = b[1], y0 = b[2], y1 = b[3], z0 = b[4], z1 = b[5];
      [[[x1,y0,z0],[x1,y1,z0],[x1,y1,z1],[x1,y0,z1],[1,0,0]], [[x0,y0,z1],[x0,y1,z1],[x0,y1,z0],[x0,y0,z0],[-1,0,0]],
       [[x0,y1,z0],[x0,y1,z1],[x1,y1,z1],[x1,y1,z0],[0,1,0]], [[x0,y0,z1],[x0,y0,z0],[x1,y0,z0],[x1,y0,z1],[0,-1,0]],
       [[x0,y0,z1],[x1,y0,z1],[x1,y1,z1],[x0,y1,z1],[0,0,1]], [[x1,y0,z0],[x0,y0,z0],[x0,y1,z0],[x1,y1,z0],[0,0,-1]]
      ].forEach(function (f) { [0, 1, 2, 0, 2, 3].forEach(function (i) { tri.push(f[i][0], f[i][1], f[i][2], f[4][0], f[4][1], f[4][2]); }); });
      var c = [[x0,y0,z0],[x1,y0,z0],[x1,y1,z0],[x0,y1,z0],[x0,y0,z1],[x1,y0,z1],[x1,y1,z1],[x0,y1,z1]];
      [[0,1],[1,2],[2,3],[3,0],[4,5],[5,6],[6,7],[7,4],[0,4],[1,5],[2,6],[3,7]].forEach(function (e) {
        lines.push(c[e[0]][0], c[e[0]][1], c[e[0]][2], c[e[1]][0], c[e[1]][1], c[e[1]][2]);
      });
    });
    return { tri: new Float32Array(tri), lines: new Float32Array(lines) };
  }

  /* Les composants : formes, point d'ancrage de l'étiquette, cadrage du zoom. */
  var SHAPES = {
    tools: { at: [-8, 1.3, 0], d: 9, boxes: [B(-8, 0, -1.4, 1.6, 0.5, 1), B(-8, 0, 0, 1.6, 0.9, 1), B(-8, 0, 1.4, 1.6, 0.7, 1)] },
    sandbox: { at: [-6.9, 1.7, -2.3], d: 10, boxes: [B(-9.1, 0, -2.3, 0.14, 1.4, 0.14), B(-6.9, 0, -2.3, 0.14, 1.4, 0.14), B(-9.1, 0, 2.3, 0.14, 1.4, 0.14),
      B(-6.9, 0, 2.3, 0.14, 1.4, 0.14), B(-8, 1.4, -2.3, 2.34, 0.12, 0.14), B(-8, 1.4, 2.3, 2.34, 0.12, 0.14), B(-9.1, 1.4, 0, 0.14, 0.12, 4.74), B(-6.9, 1.4, 0, 0.14, 0.12, 4.74)] },
    permissions: { at: [-4, 2.2, 0], d: 8, boxes: [B(-4, 0, -0.9, 0.4, 1.6, 0.4), B(-4, 0, 0.9, 0.4, 1.6, 0.4), B(-4, 1.6, 0, 0.5, 0.3, 2.3)] },
    engine: { at: [0, 3, 0], d: 10, boxes: [B(0, 0, 0, 3, 1.6, 3), B(0, 1.6, 0, 2, 0.7, 2), B(0, 2.3, 0, 0.9, 0.4, 0.9)] },
    redaction: { at: [4, 1.8, 0], d: 8, boxes: [0, 1, 2, 3, 4, 5].map(function (i) { return B(4 + (i - 2.5) * 0.34, 0, 0, 0.18, 1.3, 2.2); }) },
    routing: { at: [6.4, 1.4, 0], d: 8, boxes: [B(6.4, 0, 0, 0.9, 0.55, 0.9), B(6.4, 0.55, 0, 0.5, 0.35, 0.5), B(6.4, 0, -1.2, 0.3, 0.3, 0.6), B(6.4, 0, 1.2, 0.3, 0.3, 0.6)] },
    profile: { at: [8.4, 1.3, -2.8], d: 8, boxes: [B(7.7, 0, -2.8, 0.25, 1.0, 0.25), B(9.1, 0, -2.8, 0.25, 1.0, 0.25), B(8.4, 0.8, -2.8, 1.9, 0.22, 0.22)] },
    timestamp: { at: [8.4, 1.7, 4.8], d: 8, boxes: [B(8.4, 0, 4.8, 1.4, 0.3, 1.4), B(8.4, 0.3, 4.8, 0.9, 0.9, 0.9), B(8.4, 1.2, 4.8, 0.3, 0.25, 0.3)] },
    export: { at: [4.8, 1.3, 4.8], d: 8, boxes: [B(4.4, 0, 4.8, 0.9, 1.0, 0.12), B(5.2, 0, 4.9, 0.9, 0.8, 0.12), B(4.8, 0, 4.8, 2.2, 0.1, 1.2)] },
    cra: { at: [-8, 1.3, -5], d: 8, boxes: [B(-8, 0, -5, 2.2, 0.4, 1.4), B(-8.5, 0.4, -5, 0.5, 0.6, 0.5), B(-7.8, 0.4, -5, 0.5, 0.4, 0.5), B(-7.1, 0.4, -5, 0.5, 0.8, 0.5)] },
    providers: { at: [8.4, 3.7, 0], d: 11, boxes: [B(8.4, 0, 0, 1.6, 2.6, 1.6), B(8.4, 2.6, 0, 0.6, 0.8, 0.6)] },
    audit: { at: [0, 1.2, 4.8], d: 9, boxes: (function () {
      var l = []; for (var i = 0; i < 6; i++) { var x = -2.5 + i; l.push(B(x, 0, 4.8, 0.7, 0.7, 0.7)); if (i < 5) l.push(B(x + 0.5, 0.29, 4.8, 0.32, 0.12, 0.12)); } return l; })() },
    skills: { at: [0, 1.4, -4.8], d: 8, boxes: [0, 1, 2, 3, 4].map(function (i) { return B(i * 0.09 - 0.18, i * 0.22, -4.8, 2.2, 0.16, 1.5); }) },
    subagents: { at: [4.8, 1.5, -4.4], d: 9, boxes: [3.5, 4.8, 6.1].reduce(function (l, x) { return l.concat([B(x, 0, -4.4, 1, 0.7, 1), B(x, 0.7, -4.4, 0.6, 0.35, 0.6)]); }, []) },
    hooks: { at: [-4.7, 1.9, -4.2], d: 7, boxes: [B(-5.1, 0, -4.2, 0.4, 1.5, 0.4), B(-4.6, 1.15, -4.2, 1.4, 0.35, 0.4), B(-4.1, 0.6, -4.2, 0.4, 0.9, 0.4)] },
    mcp: { at: [-7.5, 1.2, 4.5], d: 8, boxes: [B(-7.5, 0, 4.5, 2.2, 0.8, 1.6), B(-6.2, 0.25, 4.1, 0.5, 0.3, 0.3), B(-6.2, 0.25, 4.9, 0.5, 0.3, 0.3)] },
    checkpoints: { at: [-3.4, 1.6, 5.6], d: 8, boxes: [0, 1, 2, 3].map(function (i) { return B(-4.3 + i * 0.6, 0, 5.6, 0.55, 0.3 + i * 0.25, 1.2); }) },
    /* Mémoire : trois fiches empilées, la plus haute décalée (une note lue). */
    memory: { at: [-2.3, 1.2, -2.9], d: 7, boxes: [B(-2.3, 0, -2.9, 1.3, 0.14, 0.9), B(-2.25, 0.14, -2.9, 1.3, 0.14, 0.9), B(-2.15, 0.28, -2.95, 1.3, 0.14, 0.9), B(-2.0, 0.42, -3.05, 1.3, 0.14, 0.9)] },
    /* Sobriété : une jauge, trois cellules pleines sur dix. */
    sobriety: { at: [3.2, 1.1, 2.7], d: 7, boxes: [B(3.2, 0, 2.7, 2.2, 0.18, 0.8)].concat([0, 1, 2, 3, 4, 5, 6, 7, 8, 9].map(function (i) {
      return B(2.33 + i * 0.193, 0.18, 2.7, 0.15, i < 3 ? 0.5 : 0.14, 0.5); })) },
    /* Éditeurs : un écran sur son pied. */
    editors: { at: [-5.3, 1.8, 2.9], d: 7, boxes: [B(-5.3, 0, 2.9, 0.9, 0.06, 0.55), B(-5.3, 0.06, 2.9, 0.18, 0.4, 0.18), B(-5.3, 0.46, 2.9, 1.7, 1.0, 0.12)] },
    /* Pipelines : quatre étapes de plus en plus hautes sur un conduit. */
    ci: { at: [-5.6, 1.4, -2.6], d: 7, boxes: [B(-5.6, 0, -2.6, 0.16, 0.16, 2.4)].concat([0, 1, 2, 3].map(function (i) {
      return B(-5.6, 0, -3.5 + i * 0.6, 0.7, 0.35 + i * 0.18, 0.42); })) },
    policy: { at: [9.6, 1.1, -6.1], d: 13, boxes: [B(0, 0, -7.2, 21.6, 0.3, 0.12), B(0, 0, 7.4, 21.6, 0.3, 0.12), B(-10.8, 0, 0.1, 0.12, 0.3, 14.6),
      B(10.8, 0, 0.1, 0.12, 0.3, 14.6), B(9.6, 0, -6.1, 1, 0.5, 1)] }
  };
  /* Pistes de données (x, z), avec les composants qu'elles relient. */
  var TRACES = [
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
    { ends: ["ci", "permissions"], pts: [[-5.25, -1.4], [-5.25, -0.9], [-4.2, -0.9]] }
  ];
  TRACES.forEach(function (t) {
    var len = 0, segs = [];
    for (var i = 1; i < t.pts.length; i++) {
      var a = t.pts[i - 1], b = t.pts[i], l = Math.hypot(b[0] - a[0], b[1] - a[1]);
      segs.push({ a: a, b: b, l: l, o: len }); len += l;
    }
    t.len = len; t.segs = segs;
  });

  var PARTS = PARTS_TXT.map(function (p, i) {
    var sh = SHAPES[p.id], m = mesh(sh.boxes);
    return { id: p.id, name: p.name, desc: p.desc, num: (i + 1 < 10 ? "0" : "") + (i + 1), at: sh.at, d: sh.d, mesh: m };
  });

  function shader(type, src) { var s = gl.createShader(type); gl.shaderSource(s, src); gl.compileShader(s); return s; }
  function program(vs, fs) {
    var p = gl.createProgram();
    gl.attachShader(p, shader(gl.VERTEX_SHADER, vs)); gl.attachShader(p, shader(gl.FRAGMENT_SHADER, fs));
    gl.linkProgram(p);
    if (!gl.getProgramParameter(p, gl.LINK_STATUS)) throw new Error("link");
    return p;
  }
  var lit, flat, dots;
  try {
    lit = program(
      "attribute vec3 p; attribute vec3 n; uniform mat4 mvp; varying vec3 vN;" +
      "void main(){ vN = n; gl_Position = mvp * vec4(p, 1.0); }",
      "precision mediump float; uniform vec3 color; varying vec3 vN;" +
      "void main(){ vec3 N = normalize(vN); float key = max(dot(N, normalize(vec3(0.4, 0.85, 0.5))), 0.0);" +
      "float fill = max(dot(N, normalize(vec3(-0.6, 0.3, -0.5))), 0.0);" +
      "vec3 c = color * (0.28 + 0.62 * key + 0.18 * fill);" +
      "gl_FragColor = vec4(pow(c, vec3(1.0 / 2.2)), 1.0); }");
    flat = program(
      "attribute vec3 p; uniform mat4 mvp; void main(){ gl_Position = mvp * vec4(p, 1.0); }",
      "precision mediump float; uniform vec3 color; uniform float alpha;" +
      "void main(){ gl_FragColor = vec4(pow(color, vec3(1.0 / 2.2)) * alpha, alpha); }");
    dots = program(
      "attribute vec3 p; uniform mat4 mvp; uniform float size;" +
      "void main(){ gl_Position = mvp * vec4(p, 1.0); gl_PointSize = size / gl_Position.w; }",
      "precision mediump float; uniform vec3 color; uniform float star;" +
      "void main(){ vec2 q = gl_PointCoord * 2.0 - 1.0; float r = length(q); float a = atan(q.x, -q.y);" +
      "float k = 0.5 + 0.5 * cos(5.0 * a); float edge = mix(0.9, mix(0.42, 0.95, pow(k, 3.0)), star);" +
      "float m = 1.0 - smoothstep(edge - 0.1, edge, r); if (m <= 0.01) discard;" +
      "gl_FragColor = vec4(pow(color, vec3(1.0 / 2.2)) * m, m); }");
  } catch (e) { return; }
  stage.classList.remove("is-fallback");

  function buffer(data, usage) { var b = gl.createBuffer(); gl.bindBuffer(gl.ARRAY_BUFFER, b); gl.bufferData(gl.ARRAY_BUFFER, data, usage || gl.STATIC_DRAW); return b; }
  var board = mesh([B(0, -0.3, 0.1, 22, 0.3, 15)]);
  var BOARD = { tri: buffer(board.tri), n: board.tri.length / 6 };
  var GRID = (function () {
    var g = [];
    for (var x = -10; x <= 10; x++) g.push(x, 0.004, -7, x, 0.004, 7.3);
    for (var z = -7; z <= 7; z++) g.push(-10.8, 0.004, z, 10.8, 0.004, z);
    return { buf: buffer(new Float32Array(g)), n: g.length / 3 };
  })();
  TRACES.forEach(function (t) {
    var l = [];
    t.segs.forEach(function (s) { l.push(s.a[0], 0.02, s.a[1], s.b[0], 0.02, s.b[1]); });
    t.buf = buffer(new Float32Array(l)); t.n = l.length / 3;
  });
  PARTS.forEach(function (p) {
    p.tri = buffer(p.mesh.tri); p.ntri = p.mesh.tri.length / 6;
    p.lines = buffer(p.mesh.lines); p.nlines = p.mesh.lines.length / 3;
  });
  var STARS = (function () {
    var s = [];
    for (var i = 0; i < 12; i++) { var a = i / 12 * Math.PI * 2; s.push(8.4 + 2.3 * Math.cos(a), 1.6 + 0.0 * i, 2.3 * Math.sin(a)); }
    return { buf: buffer(new Float32Array(s)), n: 12 };
  })();
  var PACKETS = { buf: buffer(new Float32Array(3 * 64), gl.DYNAMIC_DRAW), data: new Float32Array(3 * 64), n: 0 };

  /* Caméra : cible, distance, lacet, tangage ; « goal » est l'état visé, interpolé à chaque image. */
  var OVER = { t: [0, 0, 0.2], d: 42, yaw: 0, pitch: 1.12 };
  var cam = { t: [0, 0, 0.2], d: 44, yaw: -0.5, pitch: 1.3 };
  var goal = { t: OVER.t.slice(), d: OVER.d, yaw: OVER.yaw, pitch: OVER.pitch };
  var focus = -1, playing = false, timer = 0, dpr = 1, raf = 0, onScreen = true, t0 = performance.now(), last = 0;
  var dragging = false, moved = 0, lx = 0, ly = 0, vp = null;

  var numEl = document.getElementById("h-part-num"), nameEl = document.getElementById("h-part-name"), descEl = document.getElementById("h-part-desc");
  var tourBtn = document.getElementById("h-tour");
  var pins = PARTS.map(function (p, i) {
    var b = document.createElement("button");
    b.type = "button"; b.className = "h-pin";
    b.innerHTML = '<span class="h-pin__num"></span><span class="h-pin__name"></span>';
    b.querySelector(".h-pin__num").textContent = p.num;
    b.querySelector(".h-pin__name").textContent = p.name;
    b.setAttribute("aria-label", p.num + " " + p.name);
    b.addEventListener("click", function () { stopTour(); select(i); });
    pinsBox.appendChild(b);
    return b;
  });

  function select(i) {
    focus = i;
    if (i < 0) {
      goal.t = OVER.t.slice(); goal.d = OVER.d * fitFactor(); goal.yaw = OVER.yaw; goal.pitch = OVER.pitch;
      numEl.textContent = "00"; nameEl.textContent = dataEl.getAttribute("data-overview-name"); descEl.textContent = dataEl.getAttribute("data-overview-desc");
    } else {
      var p = PARTS[i];
      goal.t = [p.at[0], p.at[1] * 0.45, p.at[2]]; goal.d = p.d * fitFactor();
      goal.yaw = -0.55 + (i % 4) * 0.36; goal.pitch = 0.62;
      numEl.textContent = p.num; nameEl.textContent = p.name; descEl.textContent = p.desc;
    }
    pins.forEach(function (b, k) {
      b.classList.toggle("is-active", k === i);
      b.classList.toggle("is-dim", i >= 0 && k !== i);
      b.setAttribute("aria-pressed", k === i ? "true" : "false");
    });
    if (reduce) { cam.t = goal.t.slice(); cam.d = goal.d; cam.yaw = goal.yaw; cam.pitch = goal.pitch; }
    request();
  }
  function fitFactor() { var asp = canvas.clientWidth / Math.max(1, canvas.clientHeight); return asp < 0.9 ? 1.25 : 1; }
  function step(dir) { var n = PARTS.length; select(focus < 0 ? (dir > 0 ? 0 : n - 1) : (focus + dir + n) % n); }

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
  tourBtn.addEventListener("click", function () { if (playing) stopTour(); else startTour(); });
  document.getElementById("h-prev").addEventListener("click", function () { stopTour(); step(-1); });
  document.getElementById("h-next").addEventListener("click", function () { stopTour(); step(1); });
  document.getElementById("h-overview").addEventListener("click", function () { stopTour(); select(-1); });

  function resize() {
    dpr = Math.min(window.devicePixelRatio || 1, 2);
    var w = Math.round(canvas.clientWidth * dpr), h = Math.round(canvas.clientHeight * dpr);
    if (canvas.width !== w || canvas.height !== h) { canvas.width = w; canvas.height = h; }
    gl.viewport(0, 0, canvas.width, canvas.height);
    goal.d = (focus < 0 ? OVER.d : PARTS[focus].d) * fitFactor();
    request();
  }
  function project(pt) {
    var x = pt[0], y = pt[1], z = pt[2];
    var cx = vp[0] * x + vp[4] * y + vp[8] * z + vp[12], cy = vp[1] * x + vp[5] * y + vp[9] * z + vp[13], w = vp[3] * x + vp[7] * y + vp[11] * z + vp[15];
    return [(cx / w * 0.5 + 0.5) * canvas.clientWidth, (1 - (cy / w * 0.5 + 0.5)) * canvas.clientHeight, w];
  }
  function drawLines(buf, n, mvp, color, alpha) {
    gl.useProgram(flat);
    gl.bindBuffer(gl.ARRAY_BUFFER, buf);
    var pl = gl.getAttribLocation(flat, "p");
    gl.enableVertexAttribArray(pl); gl.vertexAttribPointer(pl, 3, gl.FLOAT, false, 12, 0);
    gl.uniformMatrix4fv(gl.getUniformLocation(flat, "mvp"), false, mvp);
    gl.uniform3fv(gl.getUniformLocation(flat, "color"), color);
    gl.uniform1f(gl.getUniformLocation(flat, "alpha"), alpha);
    gl.drawArrays(gl.LINES, 0, n);
  }
  function drawSolid(buf, n, mvp, color) {
    gl.useProgram(lit);
    gl.bindBuffer(gl.ARRAY_BUFFER, buf);
    var pl = gl.getAttribLocation(lit, "p"), nl = gl.getAttribLocation(lit, "n");
    gl.enableVertexAttribArray(pl); gl.enableVertexAttribArray(nl);
    gl.vertexAttribPointer(pl, 3, gl.FLOAT, false, 24, 0); gl.vertexAttribPointer(nl, 3, gl.FLOAT, false, 24, 12);
    gl.uniformMatrix4fv(gl.getUniformLocation(lit, "mvp"), false, mvp);
    gl.uniform3fv(gl.getUniformLocation(lit, "color"), color);
    gl.enable(gl.POLYGON_OFFSET_FILL); gl.polygonOffset(1, 1);
    gl.drawArrays(gl.TRIANGLES, 0, n);
    gl.disable(gl.POLYGON_OFFSET_FILL);
    gl.disableVertexAttribArray(nl);
  }
  function drawDots(buf, n, mvp, color, size, isStar) {
    gl.useProgram(dots);
    gl.bindBuffer(gl.ARRAY_BUFFER, buf);
    var pl = gl.getAttribLocation(dots, "p");
    gl.enableVertexAttribArray(pl); gl.vertexAttribPointer(pl, 3, gl.FLOAT, false, 12, 0);
    gl.uniformMatrix4fv(gl.getUniformLocation(dots, "mvp"), false, mvp);
    gl.uniform3fv(gl.getUniformLocation(dots, "color"), color);
    gl.uniform1f(gl.getUniformLocation(dots, "size"), size * dpr);
    gl.uniform1f(gl.getUniformLocation(dots, "star"), isStar ? 1 : 0);
    gl.drawArrays(gl.POINTS, 0, n);
  }
  var lerp = function (a, b, k) { return a + (b - a) * k; };

  function frame(now) {
    raf = 0;
    /* Interpolation calée sur le temps écoulé (indépendante de la cadence d'affichage). */
    var dt = last ? Math.min(0.1, (now - last) / 1000) : 0.016; last = now;
    var k = reduce ? 1 : 1 - Math.exp(-dt / 0.32), settled = true;
    ["d", "yaw", "pitch"].forEach(function (f) { cam[f] = lerp(cam[f], goal[f], k); if (Math.abs(cam[f] - goal[f]) > 1e-3) settled = false; else cam[f] = goal[f]; });
    for (var i = 0; i < 3; i++) { cam.t[i] = lerp(cam.t[i], goal.t[i], k); if (Math.abs(cam.t[i] - goal.t[i]) > 1e-3) settled = false; else cam.t[i] = goal.t[i]; }

    var asp = canvas.width / Math.max(1, canvas.height);
    var cp = Math.cos(cam.pitch);
    var eye = [cam.t[0] + cam.d * cp * Math.sin(cam.yaw), cam.t[1] + cam.d * Math.sin(cam.pitch), cam.t[2] + cam.d * cp * Math.cos(cam.yaw)];
    vp = mul(perspective(0.6, asp, 0.5, 200), lookAt(eye, cam.t));

    gl.clearColor(0, 0, 0, 0);
    gl.clear(gl.COLOR_BUFFER_BIT | gl.DEPTH_BUFFER_BIT);
    gl.enable(gl.DEPTH_TEST);
    gl.enable(gl.BLEND); gl.blendFunc(gl.ONE, gl.ONE_MINUS_SRC_ALPHA);

    drawSolid(BOARD.tri, BOARD.n, vp, COL.board);
    drawLines(GRID.buf, GRID.n, vp, COL.grid, 0.55);

    var active = focus >= 0 ? PARTS[focus].id : null;
    TRACES.forEach(function (t) {
      var on = active && (t.ends.indexOf(active) >= 0 || active === "policy");
      drawLines(t.buf, t.n, vp, on ? COL.gold : COL.edge, on ? 1 : 0.7);
    });
    PARTS.forEach(function (p, i) {
      var on = i === focus, dim = focus >= 0 && !on;
      drawSolid(p.tri, p.ntri, vp, dim ? COL.dim : COL.part);
      drawLines(p.lines, p.nlines, vp, on ? COL.gold : COL.edge, on ? 1 : 0.8);
    });
    drawDots(STARS.buf, STARS.n, vp, COL.gold, 90, true);

    /* Paquets de données : ils circulent sur les pistes du composant visé. */
    var animating = false;
    if (active && !reduce) {
      var n = 0, time = (now - t0) / 1000;
      TRACES.forEach(function (t) {
        if (!(t.ends.indexOf(active) >= 0 || active === "policy")) return;
        for (var j = 0; j < 3 && n < 64; j++) {
          var u = ((time * 2.2 / t.len) + j / 3) % 1 * t.len;
          for (var s = 0; s < t.segs.length; s++) {
            var sg = t.segs[s];
            if (u <= sg.o + sg.l) {
              var f = (u - sg.o) / sg.l;
              PACKETS.data[n * 3] = sg.a[0] + (sg.b[0] - sg.a[0]) * f; PACKETS.data[n * 3 + 1] = 0.08; PACKETS.data[n * 3 + 2] = sg.a[1] + (sg.b[1] - sg.a[1]) * f;
              n++; break;
            }
          }
        }
      });
      gl.bindBuffer(gl.ARRAY_BUFFER, PACKETS.buf);
      gl.bufferSubData(gl.ARRAY_BUFFER, 0, PACKETS.data.subarray(0, n * 3));
      drawDots(PACKETS.buf, n, vp, COL.gold, 55, false);
      animating = n > 0;
    }

    /* Étiquettes HTML calées sur la projection des ancrages. */
    PARTS.forEach(function (p, i) {
      var s = project(p.at), b = pins[i];
      var out = s[2] <= 0 || s[0] < 0 || s[1] < 0 || s[0] > canvas.clientWidth || s[1] > canvas.clientHeight;
      b.classList.toggle("is-hidden", out);
      b.style.transform = "translate(" + s[0].toFixed(1) + "px," + s[1].toFixed(1) + "px) translate(-50%, calc(-100% - 10px))";
      b.style.zIndex = i === focus ? 2 : 1;
    });

    if (onScreen && (dragging || !settled || animating)) request(); else last = 0;
  }
  function request() { if (!raf) raf = requestAnimationFrame(frame); }

  /* Orbite à la souris ou au doigt ; un clic sans glisser vise le composant le plus proche. */
  canvas.addEventListener("pointerdown", function (e) {
    dragging = true; moved = 0; lx = e.clientX; ly = e.clientY; canvas.setPointerCapture(e.pointerId);
  });
  canvas.addEventListener("pointermove", function (e) {
    if (!dragging) return;
    var dx = e.clientX - lx, dy = e.clientY - ly; lx = e.clientX; ly = e.clientY;
    moved += Math.abs(dx) + Math.abs(dy);
    if (moved > 4) stopTour();
    goal.yaw -= dx * 0.008; goal.pitch = Math.max(0.25, Math.min(1.45, goal.pitch + dy * 0.006));
    cam.yaw = goal.yaw; cam.pitch = goal.pitch;
    request();
  });
  var end = function (e) {
    if (!dragging) return;
    dragging = false;
    try { canvas.releasePointerCapture(e.pointerId); } catch (x) {}
    if (moved < 6 && vp) {
      var r = canvas.getBoundingClientRect(), mx = e.clientX - r.left, my = e.clientY - r.top, best = -1, bd = 70;
      PARTS.forEach(function (p, i) { var s = project(p.at); var dd = Math.hypot(s[0] - mx, s[1] - my + 20); if (dd < bd) { bd = dd; best = i; } });
      stopTour(); select(best);
    }
    request();
  };
  canvas.addEventListener("pointerup", end);
  canvas.addEventListener("pointercancel", end);
  canvas.addEventListener("keydown", function (e) {
    if (e.key === "ArrowRight") { stopTour(); step(1); }
    else if (e.key === "ArrowLeft") { stopTour(); step(-1); }
    else if (e.key === "Escape") { stopTour(); select(-1); }
    else if (e.key === "Enter" || e.key === " ") { if (playing) stopTour(); else startTour(); }
    else return;
    e.preventDefault();
  });

  if ("ResizeObserver" in window) new ResizeObserver(resize).observe(canvas); else window.addEventListener("resize", resize);
  if ("IntersectionObserver" in window) {
    new IntersectionObserver(function (en) { onScreen = en[0].isIntersecting; if (onScreen) request(); }).observe(canvas);
  }
  canvas.addEventListener("webglcontextlost", function (e) { e.preventDefault(); stopTour(); stage.classList.add("is-fallback"); });

  resize();
  select(-1);
  /* La caméra descend sur la carte, puis la visite démarre une fois (jamais en mouvement réduit). */
  if (!reduce) timer = setTimeout(startTour, 2200);
})();
