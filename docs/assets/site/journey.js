/* tenon · « Le voyage d'une requête » : le plan complet du harness vu de haut (maquette 2,5D),
 * traversé par une requête. Méthode explainer-engaging (un héros, une prise, caméra qui suit,
 * recul final), rendue en direct sur un canvas 2D. seek(t) est une fonction pure du temps ;
 * la caméra, qui a une histoire, est précalculée au chargement par pas de 1/240 s. */
(function () {
  "use strict";
  var fig = document.getElementById("h-jr");
  var canvas = document.getElementById("h-jr-canvas");
  var dataEl = document.getElementById("h-jr-data");
  if (!fig || !canvas || !dataEl || !canvas.getContext) return;
  var TXT = JSON.parse(dataEl.textContent);
  var ctx = canvas.getContext("2d");
  var capEl = document.getElementById("h-jr-cap");
  var reduce = window.matchMedia("(prefers-reduced-motion: reduce)").matches;
  fig.classList.add("is-live");

  var css = getComputedStyle(document.documentElement);
  var C = {};
  ["night", "night-2", "night-rule", "on-night", "on-night-muted", "gold", "gold-ink", "accent", "3d-rail", "3d-post", "3d-edge"].forEach(function (n) {
    C[n] = css.getPropertyValue("--color-" + n).trim();
  });
  var F = { body: css.getPropertyValue("--font-body").trim(), mono: css.getPropertyValue("--font-mono").trim() };

  var clamp = function (x, a, b) { return Math.max(a, Math.min(b, x)); };
  var lerp = function (a, b, k) { return a + (b - a) * k; };
  var smooth = function (a, b, t) { var x = clamp((t - a) / (b - a), 0, 1); return x * x * (3 - 2 * x); };
  var easeOut = function (x) { return 1 - Math.pow(1 - clamp(x, 0, 1), 3); };
  var spring = function (x) { if (x <= 0) return 0; return 1 - Math.exp(-7 * x) * Math.cos(11 * x); };

  /* ── Le plan (monde 2600 × 1500) : le plateau est votre machine ─── */
  var BOARD = { x0: 300, y0: 150, x1: 2110, y1: 1360 };
  var P = {   /* composants : centre, taille du dessus, épaisseur (faux relief) */
    you:         { x: 130,  y: 760,  w: 200, h: 120, d: 0 },
    hooks:       { x: 560,  y: 340,  w: 240, h: 120, d: 18 },
    skills:      { x: 1150, y: 330,  w: 260, h: 130, d: 18 },
    subagents:   { x: 1700, y: 330,  w: 300, h: 130, d: 18 },
    tools:       { x: 470,  y: 760,  w: 230, h: 140, d: 18 },
    permissions: { x: 810,  y: 760,  w: 240, h: 130, d: 18 },
    engine:      { x: 1160, y: 760,  w: 300, h: 210, d: 34 },
    redaction:   { x: 1520, y: 760,  w: 250, h: 130, d: 18 },
    routing:     { x: 1860, y: 760,  w: 260, h: 150, d: 18 },
    local:       { x: 1860, y: 1130, w: 240, h: 120, d: 22 },
    eu:          { x: 2380, y: 760,  w: 300, h: 300, d: 0 },
    mcp:         { x: 470,  y: 1170, w: 230, h: 120, d: 18 },
    checkpoints: { x: 840,  y: 1170, w: 240, h: 120, d: 18 },
    audit:       { x: 1330, y: 1170, w: 420, h: 130, d: 18 }
  };
  var ORDER = ["you", "hooks", "engine", "skills", "redaction", "routing", "local", "engine", "permissions", "tools", "audit"];
  var START = 1.8, GAP = 1.55, STAY = 1.0;
  var LAND = ORDER.map(function (_, i) { return START + GAP * i; });
  var END_STAY = LAND[LAND.length - 1] + STAY;     /* 18,3 s */
  var D = 22.5;                                    /* boucle */
  var STEP = 1 / 240, N = Math.round(D / STEP);

  var topOf = function (id) { var c = P[id]; return [c.x, c.y - c.h / 2 - 18]; };
  var wrap = function (t) { return ((t % D) + D) % D; };

  /* Position de l'étoile (pure). Avant le premier atterrissage : elle flotte au-dessus de « Vous ». */
  function hero(t) {
    t = wrap(t);
    for (var i = 0; i < ORDER.length; i++) {
      var dep = LAND[i] + STAY;
      if (t >= LAND[i] && t < dep) {
        var k = t - LAND[i], p = topOf(ORDER[i]);
        return { x: p[0], y: p[1] - Math.abs(Math.sin(k * 9)) * 10 * Math.exp(-k * 6), land: k, i: i };
      }
      if (i + 1 < ORDER.length && t >= dep && t < LAND[i + 1]) {
        var a = topOf(ORDER[i]), b = topOf(ORDER[i + 1]), u = (t - dep) / (LAND[i + 1] - dep);
        var H = 120 + Math.hypot(b[0] - a[0], b[1] - a[1]) * 0.18;
        return { x: lerp(a[0], b[0], u), y: lerp(a[1], b[1], u) - H * 4 * u * (1 - u), land: -1, i: -1, hop: 1 };
      }
    }
    /* Retour : du journal jusqu'au-dessus de « Vous » (arc lent), puis descente. */
    var a2 = topOf("audit"), y0 = topOf("you");
    var s0 = END_STAY, s1 = D + START;
    var tt = t < START ? t + D : t;
    var u2 = clamp((tt - s0) / (s1 - s0), 0, 1); u2 = 0.5 - 0.5 * Math.cos(u2 * Math.PI);
    return { x: lerp(a2[0], y0[0], u2), y: lerp(a2[1], y0[1], u2) - 620 * 4 * u2 * (1 - u2), land: -1, i: -1, hop: 2 };
  }

  /* Cible de caméra : vue d'ensemble au début et à la fin, sinon le composant visité. */
  var WORLD = { x: 1250, y: 760 };
  function overviewAmt(t) {
    t = wrap(t);
    return 1 - smooth(START - 1.2, START + 0.2, t) + smooth(END_STAY - 0.2, END_STAY + 1.2, t) - smooth(D - 0.6, D, t);
  }
  function camTarget(t) {
    t = wrap(t);
    var ov = clamp(overviewAmt(t), 0, 1), h = hero(t), sx = h.x, sy = h.y + 80;
    for (var i = 0; i < ORDER.length; i++) if (t >= LAND[i] - 0.25 && t < LAND[i] + STAY) { sx = P[ORDER[i]].x; sy = P[ORDER[i]].y; }
    return { x: lerp(sx, WORLD.x, ov), y: lerp(sy, WORLD.y, ov), z: 1 - ov };
  }
  var CAM = new Float32Array(N * 3);
  (function bake() {
    var c = camTarget(0), x = c.x, y = c.y, z = c.z, vx = 0, vy = 0, vz = 0, w = 7, wz = 4.5;
    for (var n = 0; n < N * 2; n++) {
      var g = camTarget(n * STEP);
      vx += (w * w * (g.x - x) - 2 * w * vx) * STEP; x += vx * STEP;
      vy += (w * w * (g.y - y) - 2 * w * vy) * STEP; y += vy * STEP;
      vz += (wz * wz * (g.z - z) - 2 * wz * vz) * STEP; z += vz * STEP;
      if (n >= N) { var m = (n - N) * 3; CAM[m] = x; CAM[m + 1] = y; CAM[m + 2] = z; }
    }
  })();
  function cam(t) {
    var f = wrap(t) / STEP, i = Math.floor(f) % N, j = (i + 1) % N, k = f - Math.floor(f);
    return { x: lerp(CAM[i * 3], CAM[j * 3], k), y: lerp(CAM[i * 3 + 1], CAM[j * 3 + 1], k), z: lerp(CAM[i * 3 + 2], CAM[j * 3 + 2], k) };
  }

  /* Étape courante (pour la légende) et progression locale d'un passage. */
  function stepOf(t) {
    t = wrap(t);
    if (t >= END_STAY + 0.6 || t < LAND[0] - 0.4) return t >= END_STAY + 0.6 ? 12 : 0;
    var s = 0;
    for (var i = 0; i < ORDER.length; i++) if (t >= LAND[i] - 0.4) s = i + 1;
    return s;
  }
  var RESET = function (t) { return 1 - smooth(D - 2.6, D - 1.2, wrap(t)); };
  function visit(id, t, which) {             /* progression du n-ième passage sur un composant (0 → 1) */
    t = wrap(t);
    var n = -1, idx = -1;
    for (var i = 0; i < ORDER.length; i++) if (ORDER[i] === id) { n++; if (n === (which || 0)) idx = i; }
    if (idx < 0) return 0;
    return clamp((t - LAND[idx]) / 0.8, 0, 1) * RESET(t);
  }
  function litOf(id, t) {
    t = wrap(t);
    var v = 0;
    for (var i = 0; i < ORDER.length; i++) if (ORDER[i] === id) v = Math.max(v, smooth(LAND[i] - 0.2, LAND[i], t) * (1 - smooth(LAND[i] + STAY, LAND[i] + STAY + 0.4, t)));
    return v;
  }
  function visited(id) { return ORDER.indexOf(id) >= 0; }

  /* ── Dessin ─────────────────────────────────────────────────────── */
  function rr(x, y, w, h, r) {
    ctx.beginPath(); ctx.moveTo(x + r, y); ctx.arcTo(x + w, y, x + w, y + h, r); ctx.arcTo(x + w, y + h, x, y + h, r);
    ctx.arcTo(x, y + h, x, y, r); ctx.arcTo(x, y, x + w, y, r); ctx.closePath();
  }
  function text(str, x, y, size, color, font, align, weight) {
    ctx.font = (weight || 400) + " " + size + "px " + (font || F.mono);
    ctx.fillStyle = color; ctx.textAlign = align || "left"; ctx.textBaseline = "middle"; ctx.fillText(str, x, y);
  }
  /* Bloc en faux relief : face avant (épaisseur), puis dessus. */
  function block(c, lit, dim, squash) {
    var x = c.x - c.w / 2, y = c.y - c.h / 2, sq = 1 - 0.05 * (squash || 0);
    ctx.save(); ctx.translate(c.x, c.y + c.h / 2); ctx.scale(1, sq); ctx.translate(-c.x, -(c.y + c.h / 2));
    ctx.globalAlpha = dim ? 0.55 : 1;
    if (c.d) { rr(x, y + c.d, c.w, c.h, 14); ctx.fillStyle = C["3d-post"]; ctx.fill(); }
    rr(x, y, c.w, c.h, 14); ctx.fillStyle = C["night-2"]; ctx.fill();
    ctx.lineWidth = lit > 0.05 ? 2 + 2 * lit : 1.5; ctx.strokeStyle = lit > 0.05 ? C.gold : C["3d-edge"]; ctx.stroke();
    ctx.restore(); ctx.globalAlpha = 1;
  }
  function name(id, lit, dim) {
    var c = P[id];
    ctx.globalAlpha = dim ? 0.6 : 1;
    text(TXT.n[id], c.x, c.y + c.h / 2 + (c.d || 0) + 30, 22, lit ? C.gold : C["on-night"], F.body, "center", 600);
    ctx.globalAlpha = 1;
  }
  function star(x, y, r, color, sx, sy, rot) {
    ctx.save(); ctx.translate(x, y); ctx.rotate(rot || 0); ctx.scale(sx || 1, sy || 1);
    ctx.beginPath();
    for (var i = 0; i < 10; i++) { var a = -Math.PI / 2 + i * Math.PI / 5, rad = i % 2 ? r * 0.45 : r; ctx.lineTo(Math.cos(a) * rad, Math.sin(a) * rad); }
    ctx.closePath(); ctx.fillStyle = color; ctx.fill(); ctx.restore();
  }

  /* Contenu animé posé sur chaque composant. */
  var FACE = {
    you: function (c, t) {
      var p = visit("you", t), n = Math.round(TXT.prompt.length * p);
      rr(c.x - c.w / 2, c.y - c.h / 2, c.w, c.h, 14); ctx.fillStyle = C["night-2"]; ctx.fill();
      ctx.lineWidth = 1.5; ctx.strokeStyle = C["3d-edge"]; ctx.stroke();
      text("›", c.x - c.w / 2 + 18, c.y - 14, 22, C.gold);
      var shown = TXT.prompt.slice(0, n);
      ctx.save(); rr(c.x - c.w / 2 + 6, c.y - c.h / 2 + 6, c.w - 12, c.h - 12, 10); ctx.clip();
      text(shown.length > 15 ? "…" + shown.slice(-14) : shown, c.x - c.w / 2 + 38, c.y - 14, 18, C["on-night"]);
      ctx.restore();
      if (Math.floor(wrap(t) * 2) % 2 && p < 1) { ctx.fillStyle = C.gold; ctx.fillRect(c.x - c.w / 2 + 40 + Math.min(15, shown.length) * 10.8, c.y - 26, 9, 22); }
      text("tenon", c.x - c.w / 2 + 18, c.y + 26, 15, C["on-night-muted"]);
    },
    hooks: function (c, t) {
      var p = visit("hooks", t), k = spring(p);
      ctx.strokeStyle = p > 0 ? C.gold : C["3d-edge"]; ctx.lineWidth = 6; ctx.lineCap = "round";
      ctx.beginPath(); ctx.moveTo(c.x - 60, c.y - 30); ctx.lineTo(c.x - 60, c.y + 10); ctx.arc(c.x - 42, c.y + 10, 18, Math.PI, 0, true); ctx.stroke();
      if (p > 0) {
        ctx.globalAlpha = clamp(k, 0, 1);
        rr(c.x - 10, c.y - 18 - 10 * (1 - k), 110, 36, 18); ctx.fillStyle = C.gold; ctx.fill();
        text(TXT.ctx, c.x + 45, c.y - 10 * (1 - k), 16, C["gold-ink"], F.mono, "center", 500); ctx.globalAlpha = 1;
      }
    },
    engine: function (c, t) {
      var p1 = visit("engine", t, 0), p2 = visit("engine", t, 1), act = Math.max(litOf("engine", t), 0);
      var spin = wrap(t) * (act > 0.1 ? 3 : 0.4);
      for (var r = 0; r < 3; r++) {
        ctx.beginPath(); ctx.arc(c.x, c.y, 34 + r * 22, spin * (r % 2 ? -1 : 1), spin * (r % 2 ? -1 : 1) + Math.PI * 1.4);
        ctx.strokeStyle = act > 0.1 ? C.gold : C["3d-edge"]; ctx.lineWidth = 4; ctx.globalAlpha = 0.5 + 0.5 * act; ctx.stroke();
      }
      ctx.globalAlpha = 1;
      star(c.x, c.y, 14, (p1 > 0 || p2 > 0) && act > 0.1 ? C.gold : C["3d-edge"]);
    },
    skills: function (c, t) {
      var p = visit("skills", t), k = easeOut(p);
      for (var i = 0; i < 4; i++) { rr(c.x - 70 + i * 6, c.y + 24 - i * 12, 140, 30, 6); ctx.fillStyle = C["3d-post"]; ctx.fill(); ctx.strokeStyle = C["3d-edge"]; ctx.lineWidth = 1; ctx.stroke(); }
      if (p > 0) {
        rr(c.x - 52 + 70 * k, c.y - 36 - 6 * k, 140, 30, 6); ctx.fillStyle = C.gold; ctx.fill();
        text(TXT.skill, c.x + 18 + 70 * k, c.y - 21 - 6 * k, 15, C["gold-ink"], F.mono, "center", 500);
      }
    },
    subagents: function (c, t) {
      for (var i = 0; i < 3; i++) { var x = c.x - 90 + i * 90; rr(x - 30, c.y - 22, 60, 44, 8); ctx.fillStyle = C["3d-post"]; ctx.fill(); star(x, c.y, 9, C["3d-edge"]); }
    },
    redaction: function (c, t) {
      var p = visit("redaction", t);
      ctx.save(); rr(c.x - c.w / 2 + 8, c.y - 30, c.w - 16, 60, 8); ctx.clip();
      text(p < 0.6 ? TXT.secret : TXT.masked, c.x, c.y, 22, p < 0.6 ? C["on-night"] : C.gold, F.mono, "center", 500);
      ctx.restore();
      for (var l = 0; l < 6; l++) {
        ctx.fillStyle = C.night; ctx.globalAlpha = 0.85 * Math.sin(clamp(p, 0, 1) * Math.PI);
        ctx.fillRect(c.x - 100 + l * 40 + p * 20, c.y - 30, 9, 60); ctx.globalAlpha = 1;
      }
    },
    routing: function (c, t) {
      var p = visit("routing", t), st = spring(clamp(p * 1.6, 0, 1)), sw = smooth(0.35, 0.75, p);
      /* aiguillage : vers « UE » (sortie du plateau, à droite) ou vers « local » (en bas) */
      var ax = c.x + 30, ay = c.y;
      ctx.lineWidth = 5; ctx.lineCap = "round"; ctx.strokeStyle = C["night-rule"];
      ctx.beginPath(); ctx.moveTo(ax, ay); ctx.lineTo(ax + 80, ay); ctx.moveTo(ax, ay); ctx.lineTo(ax, ay + 60); ctx.stroke();
      var ang = lerp(0, Math.PI / 2, sw);
      ctx.strokeStyle = C.gold; ctx.beginPath(); ctx.moveTo(ax - 40, ay); ctx.lineTo(ax, ay); ctx.lineTo(ax + 78 * Math.cos(ang), ay + 78 * Math.sin(ang)); ctx.stroke();
      text(TXT.eu, ax + 92, ay, 16, sw < 0.5 ? C["on-night"] : C["on-night-muted"], F.mono, "left", 500);
      text(TXT.local, ax + 14, ay + 62, 16, sw >= 0.5 ? C.gold : C["on-night-muted"], F.mono, "left", 500);
      if (p > 0) {
        ctx.save(); ctx.translate(c.x - 50, c.y - 22); ctx.rotate(-0.2); var sc = lerp(1.8, 1, st); ctx.scale(sc, sc); ctx.globalAlpha = clamp(st, 0, 1);
        ctx.font = "500 13px " + F.mono; var w = ctx.measureText(TXT.stamp).width + 20;
        rr(-w / 2, -14, w, 28, 4); ctx.strokeStyle = C.accent; ctx.lineWidth = 3; ctx.stroke();
        text(TXT.stamp, 0, 1, 13, C.accent, F.mono, "center", 500); ctx.restore(); ctx.globalAlpha = 1;
      }
    },
    local: function (c, t) {
      var p = visit("local", t), k = spring(p);
      rr(c.x - 50, c.y - 26, 100, 52, 8); ctx.fillStyle = p > 0 ? C.gold : C["3d-post"]; ctx.fill();
      for (var i = 0; i < 5; i++) { ctx.fillStyle = C["3d-edge"]; ctx.fillRect(c.x - 40 + i * 20, c.y - 36, 6, 10); ctx.fillRect(c.x - 40 + i * 20, c.y + 26, 6, 10); }
      if (p > 0.15) {
        ctx.globalAlpha = clamp(k, 0, 1);
        ctx.font = "500 15px " + F.mono; var w = ctx.measureText(TXT.reply).width + 28;
        rr(c.x - w / 2, c.y - c.h / 2 - 66, w, 40, 10); ctx.fillStyle = C["on-night"]; ctx.fill();
        text(TXT.reply, c.x, c.y - c.h / 2 - 46, 15, C.night, F.mono, "center", 500); ctx.globalAlpha = 1;
      }
    },
    eu: function (c, t) {
      for (var i = 0; i < 12; i++) { var a = -Math.PI / 2 + i * Math.PI / 6; star(c.x + 110 * Math.cos(a), c.y + 110 * Math.sin(a), 11, C["3d-edge"]); }
    },
    permissions: function (c, t) {
      var p = spring(visit("permissions", t)), on = p > 0.5;
      var tx = c.x - 80, ty = c.y;
      rr(tx, ty - 18, 76, 36, 18); ctx.fillStyle = on ? C.gold : C["night-rule"]; ctx.fill();
      ctx.beginPath(); ctx.arc(tx + 18 + 40 * clamp(p, 0, 1.08), ty, 13, 0, Math.PI * 2); ctx.fillStyle = on ? C["gold-ink"] : C["on-night-muted"]; ctx.fill();
      text(on ? TXT.allowed : TXT.waiting, tx + 92, ty, 18, on ? C.gold : C["on-night-muted"], F.body, "left", 600);
    },
    tools: function (c, t) {
      var p = visit("tools", t);
      text(TXT.shell, c.x - c.w / 2 + 22, c.y - 16, 17, C["on-night"]);
      if (p > 0.5) text(TXT.ok, c.x - c.w / 2 + 22, c.y + 20, 17, C.gold);
    },
    mcp: function (c, t) {
      rr(c.x - 50, c.y - 24, 100, 48, 8); ctx.fillStyle = C["3d-post"]; ctx.fill();
      ctx.fillStyle = C["3d-edge"]; ctx.fillRect(c.x + 50, c.y - 14, 24, 8); ctx.fillRect(c.x + 50, c.y + 6, 24, 8);
    },
    checkpoints: function (c, t) {
      for (var i = 0; i < 4; i++) { ctx.fillStyle = C["3d-post"]; ctx.fillRect(c.x - 70 + i * 36, c.y + 30 - (i + 1) * 14, 30, (i + 1) * 14); }
    },
    audit: function (c, t) {
      var p = visit("audit", t), drop = easeOut(clamp(p * 1.4, 0, 1));
      /* une étincelle par étape déjà franchie : chaque passage ajoute un bloc */
      var done = 0; for (var i = 0; i < ORDER.length - 1; i++) if (wrap(t) >= LAND[i] + 0.5) done++;
      done = Math.round(done * RESET(t));
      var n = Math.min(6, 2 + Math.floor(done / 2)), bx0 = c.x - c.w / 2 + 30;
      for (var b = 0; b < 6; b++) {
        if (b >= n && !(b === 5 && p > 0)) continue;
        var isNew = b === 5 && p > 0, y = isNew ? lerp(c.y - 150, c.y - 20, drop) : c.y - 20;
        if (b > 0) { ctx.fillStyle = isNew && p > 0.6 ? C.gold : C["night-rule"]; ctx.fillRect(bx0 + b * 62 - 18, c.y - 2, 18, 6); }
        rr(bx0 + b * 62, y, 44, 44, 6); ctx.fillStyle = isNew ? C.gold : C["3d-post"]; ctx.fill();
      }
      if (p > 0.6) text(TXT.block + " · sha256 a3f9c27e…", bx0, c.y + 44, 15, C.gold);
    }
  };

  /* Étincelles : à chaque passage, un éclat file vers le journal (tout est journalisé). */
  function sparks(t) {
    t = wrap(t);
    for (var i = 0; i < ORDER.length - 1; i++) {
      var k = (t - (LAND[i] + 0.3)) / 0.9;
      if (k <= 0 || k >= 1) continue;
      var a = P[ORDER[i]], b = P.audit, u = easeOut(k);
      var x = lerp(a.x, b.x, u), y = lerp(a.y, b.y - 30, u) - 90 * 4 * u * (1 - u);
      ctx.globalAlpha = 1 - k * 0.5; star(x, y, 7, C.gold); ctx.globalAlpha = 1;
    }
  }
  function boardAndFence(sc) {
    var b = BOARD;
    rr(b.x0, b.y0, b.x1 - b.x0, b.y1 - b.y0, 26); ctx.fillStyle = C["night-2"]; ctx.globalAlpha = 0.45; ctx.fill(); ctx.globalAlpha = 1;
    ctx.save(); rr(b.x0, b.y0, b.x1 - b.x0, b.y1 - b.y0, 26); ctx.clip();
    ctx.strokeStyle = C["night-rule"]; ctx.globalAlpha = 0.45; ctx.lineWidth = 1 / sc; ctx.beginPath();
    for (var x = b.x0; x <= b.x1; x += 80) { ctx.moveTo(x, b.y0); ctx.lineTo(x, b.y1); }
    for (var y = b.y0; y <= b.y1; y += 80) { ctx.moveTo(b.x0, y); ctx.lineTo(b.x1, y); }
    ctx.stroke(); ctx.restore(); ctx.globalAlpha = 1;
    ctx.setLineDash([22, 12]); rr(b.x0, b.y0, b.x1 - b.x0, b.y1 - b.y0, 26); ctx.strokeStyle = C.gold; ctx.globalAlpha = 0.7; ctx.lineWidth = 4; ctx.stroke(); ctx.setLineDash([]); ctx.globalAlpha = 1;
    text(TXT.machine, b.x0 + 34, b.y0 + 40, 20, C["on-night-muted"], F.mono, "left", 500);
    text(TXT.fence, b.x1 - 34, b.y1 - 34, 18, C.gold, F.mono, "right", 500);
  }
  function traces(t) {      /* pistes du trajet */
    ctx.strokeStyle = C["night-rule"]; ctx.lineWidth = 3; ctx.setLineDash([12, 10]);
    ctx.beginPath();
    for (var i = 0; i < ORDER.length; i++) { var c = P[ORDER[i]]; if (i === 0) ctx.moveTo(c.x, c.y); else ctx.lineTo(c.x, c.y); }
    ctx.stroke(); ctx.setLineDash([]);
    /* sortie vers l'UE, jamais empruntée ici */
    ctx.setLineDash([6, 10]); ctx.beginPath(); ctx.moveTo(P.routing.x + 130, P.routing.y); ctx.lineTo(P.eu.x - 130, P.eu.y); ctx.stroke(); ctx.setLineDash([]);
  }

  var W = 0, Hh = 0, dpr = 1, lastStep = -1;
  function resize() {
    dpr = Math.min(window.devicePixelRatio || 1, 2);
    W = canvas.clientWidth; Hh = canvas.clientHeight;
    canvas.width = Math.round(W * dpr); canvas.height = Math.round(Hh * dpr);
    draw(time);
  }

  function seek(t) {
    t = wrap(t);
    var c = cam(t), portrait = W < Hh;
    var tight = Math.min(W / (portrait ? 620 : 980), Hh / 620), wide = Math.min(W / 2800, Hh / 1500);
    var sc = lerp(wide, tight, c.z), rot = -0.05 * (1 - c.z);
    ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
    ctx.fillStyle = C.night; ctx.fillRect(0, 0, W, Hh);
    ctx.translate(W / 2, Hh / 2); ctx.rotate(rot); ctx.scale(sc, sc); ctx.translate(-c.x, -c.y);
    boardAndFence(sc);
    traces(t);
    /* la cage d'isolation entoure les outils */
    var cage = visit("tools", t), tl = P.tools;
    for (var bI = 0; bI < 8; bI++) {
      var bx = tl.x - tl.w / 2 - 20 + bI * (tl.w + 40) / 7;
      ctx.fillStyle = cage * 8 > bI ? C.gold : C["3d-edge"]; ctx.globalAlpha = cage * 8 > bI ? 0.8 : 0.35;
      ctx.fillRect(bx - 2, tl.y - tl.h / 2 - 40, 4, tl.h + 80 + tl.d); ctx.globalAlpha = 1;
    }
    Object.keys(P).forEach(function (id) {
      if (id === "you") { FACE.you(P.you, t); name("you", litOf("you", t) > 0.5, false); return; }
      var lit = litOf(id, t), dim = !visited(id);
      if (id !== "eu") block(P[id], lit, dim, 0);
      ctx.globalAlpha = dim ? 0.6 : 1; FACE[id](P[id], t); ctx.globalAlpha = 1;
      name(id, lit > 0.5, dim);
    });
    text(TXT.n.sandbox, tl.x - tl.w / 2 - 20, tl.y - tl.h / 2 - 58, 18, cage > 0 ? C.gold : C["on-night-muted"], F.mono, "left", 500);
    sparks(t);

    var h = hero(t), sqx = 1, sqy = 1;
    if (h.land >= 0 && h.land < 0.4) { var q = Math.sin(h.land / 0.4 * Math.PI) * Math.exp(-h.land * 5); sqx = 1 + 0.45 * q; sqy = 1 - 0.35 * q; }
    if (h.land >= 0 && h.land < 0.6) {
      ctx.beginPath(); ctx.arc(h.x, h.y + 14, 14 + h.land * 140, 0, Math.PI * 2);
      ctx.strokeStyle = C.gold; ctx.globalAlpha = (1 - h.land / 0.6) * 0.8; ctx.lineWidth = 3; ctx.stroke(); ctx.globalAlpha = 1;
    }
    var g = ctx.createRadialGradient(h.x, h.y, 2, h.x, h.y, 70);
    g.addColorStop(0, C.gold); g.addColorStop(1, "transparent");
    ctx.globalAlpha = 0.35; ctx.fillStyle = g; ctx.beginPath(); ctx.arc(h.x, h.y, 70, 0, Math.PI * 2); ctx.fill(); ctx.globalAlpha = 1;
    star(h.x, h.y + 14 * (1 - sqy), 26, C.gold, sqx, sqy, h.hop ? t * 4 : 0);

    var st = stepOf(t);
    if (st !== lastStep) { lastStep = st; if (capEl) capEl.textContent = TXT.cap[st]; }
  }

  var time = reduce ? END_STAY + 2 : 0, playing = !reduce, visible = false, last = 0, raf = 0;
  var btn = document.getElementById("h-jr-toggle"), replay = document.getElementById("h-jr-replay"), fill = document.getElementById("h-jr-fill");
  function draw(t) { if (W > 0) { seek(t); fill.style.transform = "scaleX(" + (wrap(t) / D).toFixed(4) + ")"; } }
  function loop(now) {
    raf = 0;
    if (last) time += Math.min(0.05, (now - last) / 1000);
    last = now; draw(time);
    if (playing && visible) raf = requestAnimationFrame(loop); else last = 0;
  }
  function kick() { if (!raf && playing && visible) raf = requestAnimationFrame(loop); }
  function setPlaying(on) {
    playing = on; btn.textContent = on ? btn.getAttribute("data-pause") : btn.getAttribute("data-play");
    btn.setAttribute("aria-pressed", on ? "false" : "true"); kick();
  }
  btn.addEventListener("click", function () { setPlaying(!playing); });
  replay.addEventListener("click", function () { time = 0; draw(0); setPlaying(true); });
  if (!playing) btn.textContent = btn.getAttribute("data-play");
  if ("IntersectionObserver" in window) {
    new IntersectionObserver(function (en) { visible = en[0].isIntersecting; kick(); }, { threshold: 0.35 }).observe(canvas);
  } else { visible = true; }
  if ("ResizeObserver" in window) new ResizeObserver(resize).observe(canvas); else window.addEventListener("resize", resize);
  var go = function () { resize(); kick(); };
  if (document.fonts && document.fonts.load) Promise.all([document.fonts.load("600 20px " + F.body), document.fonts.load("500 20px " + F.mono)]).then(go, go); else go();

  window.tenonJourney = { duration: D, seek: function (t) { time = t; draw(t); } };
})();
