/* tenon · « Le voyage d'une requête » : le harness vu de l'intérieur. Plan complet de tenon 0.7
 * vu de haut (maquette 2,5D) et une requête qui fait tourner la boucle d'agent : chaque appel
 * du modèle repasse par le masquage et le routage, chaque outil par les permissions, chaque
 * événement part au journal. Méthode explainer-engaging (un héros, une prise, caméra qui suit,
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

  /* ── Le plan (monde ≈ 2600 × 1700) : le plateau est votre machine ── */
  var BOARD = { x0: 300, y0: 150, x1: 2110, y1: 1640 };
  var P = {
    you:         { x: 130,  y: 760,  w: 220, h: 120, d: 0 },
    ci:          { x: 130,  y: 1100, w: 220, h: 100, d: 0 },
    hooks:       { x: 560,  y: 340,  w: 240, h: 120, d: 18 },
    skills:      { x: 1150, y: 330,  w: 260, h: 130, d: 18 },
    subagents:   { x: 1700, y: 330,  w: 300, h: 130, d: 18 },
    tools:       { x: 470,  y: 760,  w: 230, h: 140, d: 18 },
    permissions: { x: 810,  y: 760,  w: 240, h: 130, d: 18 },
    engine:      { x: 1160, y: 760,  w: 300, h: 210, d: 34 },
    redaction:   { x: 1520, y: 760,  w: 250, h: 130, d: 18 },
    routing:     { x: 1860, y: 760,  w: 250, h: 150, d: 18 },
    gate:        { x: 2110, y: 540,  w: 170, h: 100, d: 14 },
    local:       { x: 1860, y: 1130, w: 240, h: 120, d: 22 },
    eu:          { x: 2400, y: 540,  w: 300, h: 300, d: 0 },
    mcp:         { x: 470,  y: 1170, w: 230, h: 120, d: 18 },
    checkpoints: { x: 840,  y: 1170, w: 240, h: 120, d: 18 },
    audit:       { x: 1330, y: 1170, w: 420, h: 130, d: 18 },
    tsa:         { x: 2400, y: 1300, w: 240, h: 150, d: 0 },
    cra:         { x: 640,  y: 1480, w: 320, h: 110, d: 18 },
    export:      { x: 1330, y: 1480, w: 300, h: 110, d: 18 }
  };

  /* La timeline : [composant, durée sur place, légende]. Les passages rapides (0,35 s) sont
   * le masquage et le routage qui précèdent chaque nouvel appel du modèle. */
  var STOPS = [
    ["you", 1.0, 1], ["hooks", 1.0, 2], ["engine", 1.0, 3], ["skills", 1.0, 4],
    ["redaction", 1.0, 5], ["routing", 1.0, 6], ["local", 1.1, 7], ["engine", 0.7, 7],
    ["permissions", 0.9, 8], ["tools", 0.9, 8],
    ["engine", 0.8, 9], ["redaction", 0.35, 9], ["routing", 0.35, 9], ["local", 1.0, 10], ["engine", 0.6, 10],
    ["permissions", 1.1, 11], ["checkpoints", 1.0, 12], ["tools", 0.9, 12],
    ["engine", 0.7, 13], ["redaction", 0.35, 13], ["routing", 0.35, 13], ["local", 1.0, 13], ["engine", 0.6, 13],
    ["permissions", 1.0, 14], ["tools", 1.2, 14],
    ["engine", 0.7, 15], ["redaction", 0.35, 15], ["routing", 0.35, 15], ["local", 1.0, 15], ["engine", 0.6, 15],
    ["you", 1.1, 16], ["audit", 1.1, 17], ["tsa", 1.1, 18], ["export", 1.3, 19]
  ];
  var START = 1.8;
  var topOf = function (id) { var c = P[id]; return [c.x, c.y - c.h / 2 - 18]; };
  var TL = [], tcur = START;
  STOPS.forEach(function (s, i) {
    if (i > 0) {
      var a = topOf(STOPS[i - 1][0]), b = topOf(s[0]);
      tcur += clamp(Math.hypot(b[0] - a[0], b[1] - a[1]) / 1500, 0.3, 0.65);
    }
    var nth = 0;
    for (var j = 0; j < i; j++) if (STOPS[j][0] === s[0]) nth++;
    TL.push({ id: s[0], land: tcur, stay: s[1], cap: s[2], nth: nth });
    tcur += s[1];
  });
  var END_STAY = tcur;
  var D = END_STAY + 3.4;
  var STEP = 1 / 240, N = Math.round(D / STEP);
  var wrap = function (t) { return ((t % D) + D) % D; };

  function hero(t) {
    t = wrap(t);
    for (var i = 0; i < TL.length; i++) {
      var s = TL[i], dep = s.land + s.stay;
      if (t >= s.land && t < dep) {
        var k = t - s.land, p = topOf(s.id);
        return { x: p[0], y: p[1] - Math.abs(Math.sin(k * 9)) * 10 * Math.exp(-k * 6), land: k, i: i };
      }
      if (i + 1 < TL.length && t >= dep && t < TL[i + 1].land) {
        var a = topOf(s.id), b = topOf(TL[i + 1].id), u = (t - dep) / (TL[i + 1].land - dep);
        var H = 90 + Math.hypot(b[0] - a[0], b[1] - a[1]) * 0.18;
        return { x: lerp(a[0], b[0], u), y: lerp(a[1], b[1], u) - H * 4 * u * (1 - u), land: -1, i: -1, hop: 1 };
      }
    }
    var a2 = topOf(TL[TL.length - 1].id), y0 = topOf("you");
    var tt = t < START ? t + D : t;
    var u2 = clamp((tt - END_STAY) / (D + START - END_STAY), 0, 1); u2 = 0.5 - 0.5 * Math.cos(u2 * Math.PI);
    return { x: lerp(a2[0], y0[0], u2), y: lerp(a2[1], y0[1], u2) - 680 * 4 * u2 * (1 - u2), land: -1, i: -1, hop: 2 };
  }

  var WORLD = { x: 1290, y: 900 };
  function overviewAmt(t) {
    t = wrap(t);
    return clamp(1 - smooth(START - 1.2, START + 0.2, t) + smooth(END_STAY - 0.2, END_STAY + 1.2, t) - smooth(D - 0.6, D, t), 0, 1);
  }
  function camTarget(t) {
    t = wrap(t);
    var ov = overviewAmt(t), h = hero(t), sx = h.x, sy = h.y + 80;
    for (var i = 0; i < TL.length; i++) {
      var s = TL[i];
      if (s.stay >= 0.6 && t >= s.land - 0.2 && t < s.land + s.stay) { sx = P[s.id].x; sy = P[s.id].y; }
    }
    return { x: lerp(sx, WORLD.x, ov), y: lerp(sy, WORLD.y, ov), z: 1 - ov };
  }
  var CAM = new Float32Array(N * 3);
  (function bake() {
    var c = camTarget(0), x = c.x, y = c.y, z = c.z, vx = 0, vy = 0, vz = 0, w = 6.5, wz = 4.5;
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

  /* État dérivé du temps (pur) : légende, dernier passage d'un composant, itération. */
  var RESET = function (t) { return 1 - smooth(D - 2.6, D - 1.2, wrap(t)); };
  function capOf(t) {
    t = wrap(t);
    if (t < TL[0].land - 0.4) return 0;
    if (t >= END_STAY + 0.6) return 20;
    var c = 0;
    for (var i = 0; i < TL.length; i++) if (t >= TL[i].land - 0.3) c = TL[i].cap;
    return c;
  }
  /* Dernier passage commencé sur id : { nth, p (progression 0 → 1), lit } ; nth = -1 sinon. */
  function last(id, t) {
    t = wrap(t);
    var r = { nth: -1, p: 0, lit: 0 };
    if (RESET(t) <= 0.01) return r;
    for (var i = 0; i < TL.length; i++) {
      var s = TL[i];
      if (s.id !== id || t < s.land - 0.15) continue;
      r.nth = s.nth;
      r.p = clamp((t - s.land) / Math.max(0.3, s.stay * 0.8), 0, 1) * RESET(t);
      r.lit = smooth(s.land - 0.15, s.land, t) * (1 - smooth(s.land + s.stay, s.land + s.stay + 0.35, t));
    }
    return r;
  }
  function iteration(t) {
    t = wrap(t);
    var k = 0;
    for (var i = 0; i < TL.length; i++) if (TL[i].id === "local" && t >= TL[i].land) k++;
    return RESET(t) > 0.5 ? k : 0;
  }
  var VISITED = {};
  TL.forEach(function (s) { VISITED[s.id] = true; });

  /* ── Dessin ─────────────────────────────────────────────────────── */
  function rr(x, y, w, h, r) {
    ctx.beginPath(); ctx.moveTo(x + r, y); ctx.arcTo(x + w, y, x + w, y + h, r); ctx.arcTo(x + w, y + h, x, y + h, r);
    ctx.arcTo(x, y + h, x, y, r); ctx.arcTo(x, y, x + w, y, r); ctx.closePath();
  }
  function text(str, x, y, size, color, font, align, weight) {
    ctx.font = (weight || 400) + " " + size + "px " + (font || F.mono);
    ctx.fillStyle = color; ctx.textAlign = align || "left"; ctx.textBaseline = "middle"; ctx.fillText(str, x, y);
  }
  function block(c, lit, dim) {
    var x = c.x - c.w / 2, y = c.y - c.h / 2;
    ctx.globalAlpha = dim ? 0.55 : 1;
    if (c.d) { rr(x, y + c.d, c.w, c.h, 14); ctx.fillStyle = C["3d-post"]; ctx.fill(); }
    rr(x, y, c.w, c.h, 14); ctx.fillStyle = C["night-2"]; ctx.fill();
    ctx.lineWidth = lit > 0.05 ? 2 + 2 * lit : 1.5; ctx.strokeStyle = lit > 0.05 ? C.gold : C["3d-edge"]; ctx.stroke();
    ctx.globalAlpha = 1;
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
  function bubble(str, cx, y, color, ink) {
    ctx.font = "500 15px " + F.mono; var w = ctx.measureText(str).width + 28;
    rr(cx - w / 2, y - 20, w, 40, 10); ctx.fillStyle = color; ctx.fill();
    text(str, cx, y, 15, ink, F.mono, "center", 500);
  }
  function toggle(x, y, on, k) {
    rr(x, y - 18, 76, 36, 18); ctx.fillStyle = on ? C.gold : C["night-rule"]; ctx.fill();
    ctx.beginPath(); ctx.arc(x + 18 + 40 * clamp(k, 0, 1.08), y, 13, 0, Math.PI * 2); ctx.fillStyle = on ? C["gold-ink"] : C["on-night-muted"]; ctx.fill();
  }

  var FACE = {
    you: function (c, t) {
      var L = last("you", t), cw = c.w;
      rr(c.x - cw / 2, c.y - c.h / 2, cw, c.h, 14); ctx.fillStyle = C["night-2"]; ctx.fill();
      ctx.lineWidth = L.lit > 0.05 ? 2 + 2 * L.lit : 1.5; ctx.strokeStyle = L.lit > 0.05 ? C.gold : C["3d-edge"]; ctx.stroke();
      ctx.save(); rr(c.x - cw / 2 + 6, c.y - c.h / 2 + 6, cw - 12, c.h - 12, 10); ctx.clip();
      if (L.nth >= 1) {
        text("›", c.x - cw / 2 + 18, c.y - 22, 20, C.gold);
        text(TXT.prompt.slice(0, 16) + "…", c.x - cw / 2 + 38, c.y - 22, 15, C["on-night-muted"]);
        text(TXT.final, c.x - cw / 2 + 18, c.y + 14, 18, C.gold, F.mono, "left", 500);
      } else {
        var n = Math.round(TXT.prompt.length * L.p), shown = TXT.prompt.slice(0, n);
        text("›", c.x - cw / 2 + 18, c.y - 14, 22, C.gold);
        text(shown.length > 16 ? "…" + shown.slice(-15) : shown, c.x - cw / 2 + 38, c.y - 14, 17, C["on-night"]);
        text("tenon", c.x - cw / 2 + 18, c.y + 26, 15, C["on-night-muted"]);
      }
      ctx.restore();
    },
    ci: function (c) { text(TXT.ci, c.x, c.y, 16, C["on-night-muted"], F.mono, "center", 500); },
    hooks: function (c, t) {
      var L = last("hooks", t), k = spring(L.p);
      ctx.strokeStyle = L.nth >= 0 ? C.gold : C["3d-edge"]; ctx.lineWidth = 6; ctx.lineCap = "round";
      ctx.beginPath(); ctx.moveTo(c.x - 60, c.y - 30); ctx.lineTo(c.x - 60, c.y + 10); ctx.arc(c.x - 42, c.y + 10, 18, Math.PI, 0, true); ctx.stroke();
      if (L.nth >= 0) { ctx.globalAlpha = clamp(k, 0, 1); rr(c.x - 10, c.y - 18 - 10 * (1 - k), 110, 36, 18); ctx.fillStyle = C.gold; ctx.fill();
        text(TXT.ctx, c.x + 45, c.y - 10 * (1 - k), 16, C["gold-ink"], F.mono, "center", 500); ctx.globalAlpha = 1; }
    },
    engine: function (c, t) {
      var L = last("engine", t), act = L.lit, spin = wrap(t) * (act > 0.1 ? 3 : 0.4), it = iteration(t);
      for (var r = 0; r < 3; r++) {
        ctx.beginPath(); ctx.arc(c.x, c.y - 6, 40 + r * 22, spin * (r % 2 ? -1 : 1), spin * (r % 2 ? -1 : 1) + Math.PI * 1.4);
        ctx.strokeStyle = act > 0.1 ? C.gold : C["3d-edge"]; ctx.lineWidth = 4; ctx.globalAlpha = 0.5 + 0.5 * act; ctx.stroke();
      }
      ctx.globalAlpha = 1;
      text(it > 0 ? "↻ " + it : "↻", c.x, c.y - 6, 24, it > 0 ? C.gold : C["on-night-muted"], F.mono, "center", 500);
    },
    skills: function (c, t) {
      var L = last("skills", t), k = easeOut(L.p);
      for (var i = 0; i < 4; i++) { rr(c.x - 70 + i * 6, c.y + 24 - i * 12, 140, 30, 6); ctx.fillStyle = C["3d-post"]; ctx.fill(); ctx.strokeStyle = C["3d-edge"]; ctx.lineWidth = 1; ctx.stroke(); }
      if (L.nth >= 0) { rr(c.x - 52 + 70 * k, c.y - 36 - 6 * k, 140, 30, 6); ctx.fillStyle = C.gold; ctx.fill();
        text(TXT.skill, c.x + 18 + 70 * k, c.y - 21 - 6 * k, 15, C["gold-ink"], F.mono, "center", 500); }
    },
    subagents: function (c) {
      for (var i = 0; i < 3; i++) { var x = c.x - 90 + i * 90; rr(x - 30, c.y - 22, 60, 44, 8); ctx.fillStyle = C["3d-post"]; ctx.fill(); star(x, c.y, 9, C["3d-edge"]); }
    },
    redaction: function (c, t) {
      var L = last("redaction", t), p = L.nth >= 0 ? (L.nth === 0 ? L.p : 1) : 0;
      ctx.save(); rr(c.x - c.w / 2 + 8, c.y - 30, c.w - 16, 60, 8); ctx.clip();
      text(p < 0.6 ? TXT.secret : TXT.masked, c.x, c.y, 22, p < 0.6 ? C["on-night"] : C.gold, F.mono, "center", 500);
      ctx.restore();
      var sweep = L.lit > 0.1 ? Math.sin(clamp(L.p, 0, 1) * Math.PI) : 0;
      for (var l = 0; l < 6; l++) { ctx.fillStyle = C.night; ctx.globalAlpha = 0.85 * sweep; ctx.fillRect(c.x - 100 + l * 40 + L.p * 20, c.y - 30, 9, 60); }
      ctx.globalAlpha = 1;
    },
    routing: function (c, t) {
      var L = last("routing", t), p = L.nth >= 0 ? (L.nth === 0 ? L.p : 1) : 0;
      var st = spring(clamp(p * 1.6, 0, 1)), sw = smooth(0.35, 0.75, p), ax = c.x + 30, ay = c.y;
      ctx.lineWidth = 5; ctx.lineCap = "round"; ctx.strokeStyle = C["night-rule"];
      ctx.beginPath(); ctx.moveTo(ax, ay); ctx.lineTo(ax + 70, ay - 50); ctx.moveTo(ax, ay); ctx.lineTo(ax, ay + 60); ctx.stroke();
      var ang = lerp(-0.62, Math.PI / 2, sw);
      ctx.strokeStyle = C.gold; ctx.beginPath(); ctx.moveTo(ax - 40, ay); ctx.lineTo(ax, ay); ctx.lineTo(ax + 78 * Math.cos(ang), ay + 78 * Math.sin(ang)); ctx.stroke();
      text(TXT.eu, ax + 78, ay - 58, 16, sw < 0.5 ? C["on-night"] : C["on-night-muted"], F.mono, "left", 500);
      text(TXT.local, ax + 14, ay + 62, 16, sw >= 0.5 ? C.gold : C["on-night-muted"], F.mono, "left", 500);
      if (p > 0) {
        ctx.save(); ctx.translate(c.x - 52, c.y - 22); ctx.rotate(-0.2); var sc = lerp(1.8, 1, st); ctx.scale(sc, sc); ctx.globalAlpha = clamp(st, 0, 1);
        ctx.font = "500 13px " + F.mono; var w = ctx.measureText(TXT.stamp).width + 20;
        rr(-w / 2, -14, w, 28, 4); ctx.strokeStyle = C.accent; ctx.lineWidth = 3; ctx.stroke();
        text(TXT.stamp, 0, 1, 13, C.accent, F.mono, "center", 500); ctx.restore(); ctx.globalAlpha = 1;
      }
    },
    gate: function (c) {
      for (var i = 0; i < 5; i++) { ctx.fillStyle = i % 2 ? C.gold : C["night-2"]; ctx.globalAlpha = 0.7; ctx.fillRect(c.x - 70 + i * 28, c.y - 30, 28, 14); }
      ctx.globalAlpha = 1;
      text(TXT.gate, c.x, c.y + 14, 16, C["on-night"], F.mono, "center", 500);
    },
    local: function (c, t) {
      var L = last("local", t), k = spring(L.p);
      rr(c.x - 50, c.y - 26, 100, 52, 8); ctx.fillStyle = L.lit > 0.1 ? C.gold : C["3d-post"]; ctx.fill();
      for (var i = 0; i < 5; i++) { ctx.fillStyle = C["3d-edge"]; ctx.fillRect(c.x - 40 + i * 20, c.y - 36, 6, 10); ctx.fillRect(c.x - 40 + i * 20, c.y + 26, 6, 10); }
      if (L.nth >= 0 && L.lit > 0.05 && L.p > 0.1) {
        ctx.globalAlpha = clamp(k, 0, 1) * clamp(L.lit * 1.5, 0, 1);
        var last4 = L.nth === 3;
        bubble(TXT.replies[L.nth], c.x, c.y - c.h / 2 - 46, last4 ? C.gold : C["on-night"], last4 ? C["gold-ink"] : C.night);
        ctx.globalAlpha = 1;
      }
    },
    eu: function (c) { for (var i = 0; i < 12; i++) { var a = -Math.PI / 2 + i * Math.PI / 6; star(c.x + 110 * Math.cos(a), c.y + 110 * Math.sin(a), 11, C["3d-edge"]); } },
    permissions: function (c, t) {
      var L = last("permissions", t);
      if (L.nth < 0) { toggle(c.x - 82, c.y, false, 0); text(TXT.waiting, c.x + 10, c.y, 18, C["on-night-muted"], F.body, "left", 600); return; }
      if (L.nth === 0) { toggle(c.x - 82, c.y, true, 1); text(TXT.perm[0], c.x + 10, c.y, 16, C.gold, F.mono, "left", 500); return; }
      var k = spring(L.p), on = k > 0.5;
      toggle(c.x - 82, c.y, on, k);
      text(on ? TXT.perm[L.nth] : TXT.waiting, c.x + 10, c.y, 18, on ? C.gold : C["on-night-muted"], F.body, "left", 600);
    },
    tools: function (c, t) {
      var L = last("tools", t);
      if (L.nth < 0) { text("read · write · shell", c.x, c.y, 15, C["on-night-muted"], F.mono, "center", 500); return; }
      var show = L.p > 0.4;
      text(L.nth === 0 ? "read_file" : L.nth === 1 ? "write_file" : "shell", c.x - c.w / 2 + 22, c.y - 22, 16, C["on-night"], F.mono, "left", 500);
      if (show) text(TXT.tool[L.nth], c.x - c.w / 2 + 22, c.y + 18, 15, C.gold, F.mono, "left", 500);
    },
    mcp: function (c) {
      rr(c.x - 50, c.y - 24, 100, 48, 8); ctx.fillStyle = C["3d-post"]; ctx.fill();
      ctx.fillStyle = C["3d-edge"]; ctx.fillRect(c.x + 50, c.y - 14, 24, 8); ctx.fillRect(c.x + 50, c.y + 6, 24, 8);
    },
    checkpoints: function (c, t) {
      var L = last("checkpoints", t), n = L.nth >= 0 ? 4 : 3;
      for (var i = 0; i < n; i++) {
        var isNew = i === 3, drop = isNew ? easeOut(L.p) : 1;
        ctx.fillStyle = isNew ? C.gold : C["3d-post"];
        ctx.fillRect(c.x - 70 + i * 36, lerp(c.y - 80, c.y + 30 - (i + 1) * 14, drop), 30, (i + 1) * 14);
      }
      if (L.lit > 0.2) text(TXT.snap, c.x, c.y - c.h / 2 - 22, 15, C.gold, F.mono, "center", 500);
    },
    audit: function (c, t) {
      var L = last("audit", t), tw = wrap(t), done = 0;
      for (var i = 0; i < TL.length; i++) if (tw >= TL[i].land + 0.5 && TL[i].id !== "audit") done++;
      done = RESET(t) > 0.5 ? done : 0;
      var n = Math.min(6, 1 + Math.floor(done / 6)), bx0 = c.x - c.w / 2 + 30;
      for (var b = 0; b < 6; b++) {
        var isNew = b === 5 && L.nth >= 0;
        if (b >= n && !isNew) continue;
        var y = isNew ? lerp(c.y - 150, c.y - 20, easeOut(clamp(L.p * 1.4, 0, 1))) : c.y - 20;
        if (b > 0) { ctx.fillStyle = isNew && L.p > 0.6 ? C.gold : C["night-rule"]; ctx.fillRect(bx0 + b * 62 - 18, c.y - 2, 18, 6); }
        rr(bx0 + b * 62, y, 44, 44, 6); ctx.fillStyle = isNew ? C.gold : C["3d-post"]; ctx.fill();
      }
      text(TXT.block + " · sha256 a3f9c27e…", bx0, c.y + 44, 15, L.nth >= 0 && L.p > 0.6 ? C.gold : C["on-night-muted"]);
    },
    tsa: function (c, t) {
      var L = last("tsa", t), on = L.nth >= 0 && L.p > 0.5;
      ctx.beginPath(); ctx.arc(c.x, c.y - 10, 44, 0, Math.PI * 2); ctx.strokeStyle = on ? C.gold : C["3d-edge"]; ctx.lineWidth = 4; ctx.stroke();
      for (var i = 0; i < 12; i++) { var a = i * Math.PI / 6; ctx.fillStyle = on ? C.gold : C["3d-edge"]; ctx.fillRect(c.x + 36 * Math.cos(a) - 2, c.y - 10 + 36 * Math.sin(a) - 2, 4, 4); }
      var hand = (L.nth >= 0 ? L.p : 0) * Math.PI * 2 - Math.PI / 2;
      ctx.beginPath(); ctx.moveTo(c.x, c.y - 10); ctx.lineTo(c.x + 26 * Math.cos(hand), c.y - 10 + 26 * Math.sin(hand)); ctx.strokeStyle = on ? C.gold : C["on-night-muted"]; ctx.lineWidth = 3; ctx.stroke();
      text(on ? TXT.tsa_ok : TXT.tsa, c.x, c.y + 54, 15, on ? C.gold : C["on-night-muted"], F.mono, "center", 500);
    },
    export: function (c, t) {
      var L = last("export", t), k = easeOut(L.p);
      rr(c.x - 120, c.y - 34 - 20 * k, 110, 68, 6); ctx.fillStyle = L.nth >= 0 ? C["3d-rail"] : C["3d-post"]; ctx.fill();
      rr(c.x + 10, c.y - 34 - 10 * k, 110, 68, 6); ctx.fillStyle = L.nth >= 0 ? C["3d-rail"] : C["3d-post"]; ctx.fill();
      text("HTML", c.x - 65, c.y - 20 * k, 16, C.night, F.mono, "center", 500);
      text("JSON", c.x + 65, c.y - 10 * k, 16, C.night, F.mono, "center", 500);
      if (L.nth >= 0 && L.p > 0.5) text(TXT.report, c.x, c.y - c.h / 2 - 22, 15, C.gold, F.mono, "center", 500);
    },
    cra: function (c) {
      for (var i = 0; i < 3; i++) { ctx.fillStyle = C["3d-edge"]; ctx.globalAlpha = 0.6; ctx.fillRect(c.x - c.w / 2 + 24, c.y - 30 + i * 14, 120 - i * 24, 6); }
      ctx.globalAlpha = 1;
      text(TXT.cra, c.x + 40, c.y + 22, 15, C["on-night"], F.mono, "center", 500);
    }
  };

  function sparks(t) {
    t = wrap(t);
    for (var i = 0; i < TL.length; i++) {
      var s = TL[i];
      if (s.id === "audit" || s.id === "tsa" || s.id === "export") continue;
      var k = (t - (s.land + 0.2)) / 0.9;
      if (k <= 0 || k >= 1) continue;
      var a = P[s.id], b = P.audit, u = easeOut(k);
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
  function traces() {
    ctx.strokeStyle = C["night-rule"]; ctx.lineWidth = 3; ctx.setLineDash([12, 10]);
    var seen = {};
    ctx.beginPath();
    for (var i = 1; i < TL.length; i++) {
      var a = TL[i - 1].id, b = TL[i].id, key = a < b ? a + b : b + a;
      if (seen[key]) continue; seen[key] = 1;
      ctx.moveTo(P[a].x, P[a].y); ctx.lineTo(P[b].x, P[b].y);
    }
    ctx.stroke();
    ctx.setLineDash([6, 10]); ctx.beginPath();
    ctx.moveTo(P.routing.x + 70, P.routing.y - 60); ctx.lineTo(P.gate.x, P.gate.y); ctx.lineTo(P.eu.x - 130, P.eu.y);
    ctx.moveTo(P.ci.x, P.ci.y); ctx.lineTo(P.you.x, P.you.y);
    ctx.moveTo(P.cra.x, P.cra.y); ctx.lineTo(P.checkpoints.x, P.checkpoints.y);
    ctx.stroke(); ctx.setLineDash([]);
  }
  function hud(t, W) {     /* compteur de boucle, en coordonnées écran */
    var tw = wrap(t), it = iteration(t), done = tw >= TL[TL.length - 5].land && RESET(t) > 0.5, label;
    if (tw < TL[0].land || RESET(t) <= 0.5) return;
    label = done ? TXT.hud_done : it > 0 ? TXT.hud_turn + " · " + TXT.hud_iter + " " + it + "/50" : TXT.hud_turn;
    ctx.font = "500 13px " + F.mono; var w = ctx.measureText(label).width + 26;
    rr(14, 14, w, 30, 6); ctx.fillStyle = C.night; ctx.globalAlpha = 0.9; ctx.fill(); ctx.globalAlpha = 1;
    ctx.strokeStyle = C.gold; ctx.lineWidth = 1.5; ctx.stroke();
    text(label, 27, 29, 13, C.gold, F.mono, "left", 500);
  }

  var W = 0, Hh = 0, dpr = 1, lastCap = -1;
  function resize() {
    dpr = Math.min(window.devicePixelRatio || 1, 2);
    W = canvas.clientWidth; Hh = canvas.clientHeight;
    canvas.width = Math.round(W * dpr); canvas.height = Math.round(Hh * dpr);
    draw(time);
  }

  function seek(t) {
    t = wrap(t);
    var c = cam(t), portrait = W < Hh;
    var tight = Math.min(W / (portrait ? 620 : 1000), Hh / 640), wide = Math.min(W / 2800, Hh / 1750);
    var sc = lerp(wide, tight, c.z), rot = -0.05 * (1 - c.z);
    ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
    ctx.fillStyle = C.night; ctx.fillRect(0, 0, W, Hh);
    ctx.translate(W / 2, Hh / 2); ctx.rotate(rot); ctx.scale(sc, sc); ctx.translate(-c.x, -c.y);
    boardAndFence(sc);
    traces();
    /* la cage d'isolation entoure les outils et s'allume pour la commande shell */
    var T3 = last("tools", t), cage = T3.nth === 2 ? T3.p : 0, tl = P.tools;
    for (var bI = 0; bI < 8; bI++) {
      var bx = tl.x - tl.w / 2 - 20 + bI * (tl.w + 40) / 7, on = cage * 8 > bI;
      ctx.fillStyle = on ? C.gold : C["3d-edge"]; ctx.globalAlpha = on ? 0.8 : 0.35;
      ctx.fillRect(bx - 2, tl.y - tl.h / 2 - 40, 4, tl.h + 80 + tl.d); ctx.globalAlpha = 1;
    }
    Object.keys(P).forEach(function (id) {
      var L = last(id, t), dim = !VISITED[id];
      if (id === "you") { FACE.you(P.you, t); name("you", L.lit > 0.5, false); return; }
      if (id !== "eu" && id !== "tsa" && id !== "ci") block(P[id], L.lit, dim);
      if (id === "ci") { rr(P.ci.x - P.ci.w / 2, P.ci.y - P.ci.h / 2, P.ci.w, P.ci.h, 14); ctx.strokeStyle = C["3d-edge"]; ctx.lineWidth = 1.5; ctx.globalAlpha = 0.6; ctx.stroke(); ctx.globalAlpha = 1; }
      ctx.globalAlpha = dim ? 0.6 : 1; FACE[id](P[id], t); ctx.globalAlpha = 1;
      name(id, L.lit > 0.5, dim);
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

    ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
    hud(t, W);

    var cp = capOf(t);
    if (cp !== lastCap) { lastCap = cp; if (capEl) capEl.textContent = TXT.cap[cp]; }
  }

  var time = reduce ? END_STAY + 2 : 0, playing = !reduce, visible = false, lastT = 0, raf = 0;
  var btn = document.getElementById("h-jr-toggle"), replay = document.getElementById("h-jr-replay"), fill = document.getElementById("h-jr-fill");
  function draw(t) { if (W > 0) { seek(t); fill.style.transform = "scaleX(" + (wrap(t) / D).toFixed(4) + ")"; } }
  function loop(now) {
    raf = 0;
    if (lastT) time += Math.min(0.05, (now - lastT) / 1000);
    lastT = now; draw(time);
    if (playing && visible) raf = requestAnimationFrame(loop); else lastT = 0;
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

  window.tenonJourney = { duration: D, stops: TL, seek: function (t) { time = t; draw(t); } };
})();
