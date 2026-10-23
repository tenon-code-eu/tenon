/* tenon · « Le voyage d'une requête » : animation en une prise (méthode explainer-engaging),
 * rendue en direct sur un canvas 2D. seek(t) est une fonction pure du temps ; la caméra,
 * qui a une histoire, est précalculée au chargement par pas de 1/240 s. */
(function () {
  "use strict";
  var fig = document.getElementById("h-jr");
  var canvas = document.getElementById("h-jr-canvas");
  var dataEl = document.getElementById("h-jr-data");
  if (!fig || !canvas || !dataEl || !canvas.getContext) return;
  var TXT = JSON.parse(dataEl.textContent);
  var ctx = canvas.getContext("2d");
  var reduce = window.matchMedia("(prefers-reduced-motion: reduce)").matches;
  fig.classList.add("is-live");

  /* Jetons de couleur (le canvas accepte oklch directement). */
  var css = getComputedStyle(document.documentElement);
  var C = {};
  ["night", "night-2", "night-rule", "on-night", "on-night-muted", "gold", "gold-ink", "accent", "3d-rail", "3d-post"].forEach(function (n) {
    C[n] = css.getPropertyValue("--color-" + n).trim();
  });
  var F = { display: css.getPropertyValue("--font-display").trim(), body: css.getPropertyValue("--font-body").trim(), mono: css.getPropertyValue("--font-mono").trim() };

  /* ── Temps, courbes ─────────────────────────────────────────────── */
  var D = 14;                                   /* durée de la boucle, en secondes */
  var clamp = function (x, a, b) { return Math.max(a, Math.min(b, x)); };
  var lerp = function (a, b, k) { return a + (b - a) * k; };
  var smooth = function (a, b, t) { var x = clamp((t - a) / (b - a), 0, 1); return x * x * (3 - 2 * x); };
  var easeOut = function (x) { return 1 - Math.pow(1 - clamp(x, 0, 1), 3); };
  /* Ressort amorti (écrasement, rebond) : 0 → 1 avec un léger dépassement. */
  var spring = function (x) { if (x <= 0) return 0; return 1 - Math.exp(-7 * x) * Math.cos(11 * x); };

  /* ── Stations (monde : 2100 × 1250), dans l'ordre de visite, en lacet ── */
  var S = [
    { x: 260, y: 380, w: 380, h: 150 },   /* 01 prompt */
    { x: 770, y: 380, w: 340, h: 150 },   /* 02 permissions */
    { x: 1270, y: 380, w: 360, h: 170 },  /* 03 isolation */
    { x: 1790, y: 380, w: 380, h: 150 },  /* 04 masquage */
    { x: 1790, y: 900, w: 380, h: 170 },  /* 05 routage */
    { x: 1200, y: 900, w: 300, h: 300 },  /* 06 modèles (anneau d'étoiles) */
    { x: 560, y: 900, w: 460, h: 150 }    /* 07 journal */
  ];
  var LAND = S.map(function (_, i) { return 0.3 + 1.45 * i; });   /* instant où l'étoile se pose */
  var STAY = 0.9;                                                /* temps passé sur la station */
  var top = function (i) { return [S[i].x, S[i].y - S[i].h / 2 - 16]; };
  /* L'anneau des modèles : l'étoile en fait le tour au lieu de se poser. */
  var RING_R = 118;

  /* Position de l'étoile à l'instant t (pure). */
  function hero(t) {
    t = ((t % D) + D) % D;
    if (t < LAND[0]) t += D;                        /* fin du plongeon de retour */
    for (var i = 0; i < S.length; i++) {
      var dep = LAND[i] + STAY;
      if (t >= LAND[i] && t < dep) {
        var k = t - LAND[i];
        if (i === 5) {                              /* tour de l'anneau */
          var a = -Math.PI / 2 + easeOut(k / STAY) * Math.PI * 2;
          return { x: S[5].x + RING_R * Math.cos(a), y: S[5].y + RING_R * Math.sin(a), land: k, st: i };
        }
        var p = top(i);
        return { x: p[0], y: p[1] - Math.abs(Math.sin(k * 9)) * 10 * Math.exp(-k * 6), land: k, st: i };
      }
      var next = i + 1 < S.length ? LAND[i + 1] : D + LAND[0];
      if (t >= dep && t < next) {
        var a0 = i === 5 ? [S[5].x, S[5].y - RING_R] : top(i);
        var b0 = top(i + 1 < S.length ? i + 1 : 0);
        var u = (t - dep) / (next - dep);
        var big = i + 1 >= S.length;                 /* survol final : arc lent et haut */
        if (big) u = 0.5 - 0.5 * Math.cos(u * Math.PI);
        var H = big ? 560 : 150 + Math.abs(b0[1] - a0[1]) * 0.25;
        return { x: lerp(a0[0], b0[0], u), y: lerp(a0[1], b0[1], u) - H * 4 * u * (1 - u), land: -1, st: -1, hop: big ? 2 : 1 };
      }
    }
    return { x: top(0)[0], y: top(0)[1], land: 0, st: 0 };
  }

  /* Cible de caméra (pure) : station serrée, saut suivi, recul pour la révélation. */
  var WORLD = { x: 1030, y: 640 };
  function camTarget(t) {
    t = ((t % D) + D) % D;
    var reveal = smooth(LAND[6] + STAY - 0.1, LAND[6] + STAY + 1.0, t) * (1 - smooth(D - 1.1, D - 0.2, t));
    var h = hero(t), sx, sy;
    var st = -1;
    for (var i = 0; i < S.length; i++) if (t >= LAND[i] - 0.2 && t < LAND[i] + STAY) st = i;
    if (st >= 0) { sx = S[st].x; sy = S[st].y; } else { sx = h.x; sy = h.y + 60; }
    return { x: lerp(sx, WORLD.x, reveal), y: lerp(sy, WORLD.y, reveal), z: 1 - reveal };
  }
  /* Précalcul : ressort critique (ω = 8) sur deux boucles, on garde la seconde (raccord exact). */
  var STEP = 1 / 240, N = Math.round(D / STEP);
  var CAM = new Float32Array(N * 3);
  (function bake() {
    var c = camTarget(0), x = c.x, y = c.y, z = c.z, vx = 0, vy = 0, vz = 0, w = 8, wz = 5;
    for (var n = 0; n < N * 2; n++) {
      var g = camTarget(n * STEP);
      vx += (w * w * (g.x - x) - 2 * w * vx) * STEP; x += vx * STEP;
      vy += (w * w * (g.y - y) - 2 * w * vy) * STEP; y += vy * STEP;
      vz += (wz * wz * (g.z - z) - 2 * wz * vz) * STEP; z += vz * STEP;
      if (n >= N) { var m = (n - N) * 3; CAM[m] = x; CAM[m + 1] = y; CAM[m + 2] = z; }
    }
  })();
  function cam(t) {
    var f = (((t % D) + D) % D) / STEP, i = Math.floor(f) % N, j = (i + 1) % N, k = f - Math.floor(f);
    return { x: lerp(CAM[i * 3], CAM[j * 3], k), y: lerp(CAM[i * 3 + 1], CAM[j * 3 + 1], k), z: lerp(CAM[i * 3 + 2], CAM[j * 3 + 2], k) };
  }

  /* Progression d'une station : 0 avant le passage, monte, puis se réinitialise pendant la révélation. */
  var RESET = function (t) { return 1 - smooth(D - 2.2, D - 1.0, t); };
  function prog(i, t, dur) { return clamp((t - LAND[i]) / (dur || 0.6), 0, 1) * RESET(t); }

  /* ── Dessin ─────────────────────────────────────────────────────── */
  function rr(x, y, w, h, r) {
    ctx.beginPath(); ctx.moveTo(x + r, y); ctx.arcTo(x + w, y, x + w, y + h, r); ctx.arcTo(x + w, y + h, x, y + h, r);
    ctx.arcTo(x, y + h, x, y, r); ctx.arcTo(x, y, x + w, y, r); ctx.closePath();
  }
  function card(s, lit, squash) {
    var sq = 1 - 0.06 * squash;
    ctx.save(); ctx.translate(s.x, s.y + s.h / 2); ctx.scale(1, sq); ctx.translate(-s.x, -(s.y + s.h / 2));
    rr(s.x - s.w / 2, s.y - s.h / 2, s.w, s.h, 14);
    ctx.fillStyle = C["night-2"]; ctx.fill();
    ctx.lineWidth = lit > 0 ? 2 + lit : 1.5;
    ctx.strokeStyle = lit > 0 ? C.gold : C["night-rule"]; ctx.globalAlpha = 1; ctx.stroke();
    ctx.restore();
  }
  function text(str, x, y, size, color, font, align, weight) {
    ctx.font = (weight || 400) + " " + size + "px " + (font || F.mono);
    ctx.fillStyle = color; ctx.textAlign = align || "left"; ctx.textBaseline = "middle";
    ctx.fillText(str, x, y);
  }
  function label(i, s, lit) {
    var y = s.y + s.h / 2 + 34;
    text(("0" + (i + 1)).slice(-2), s.x - 6, y, 18, lit ? C.gold : C["on-night-muted"], F.mono, "right", 500);
    text(TXT.names[i], s.x + 6, y, 20, lit ? C["on-night"] : C["on-night-muted"], F.body, "left", 600);
  }
  function star(x, y, r, color, sx, sy, rot) {
    ctx.save(); ctx.translate(x, y); ctx.rotate(rot || 0); ctx.scale(sx || 1, sy || 1);
    ctx.beginPath();
    for (var i = 0; i < 10; i++) {
      var a = -Math.PI / 2 + i * Math.PI / 5, rad = i % 2 ? r * 0.45 : r;
      ctx.lineTo(Math.cos(a) * rad, Math.sin(a) * rad);
    }
    ctx.closePath(); ctx.fillStyle = color; ctx.fill(); ctx.restore();
  }
  /* Réaction d'impact : onde dorée qui s'élargit. */
  function ring(x, y, k) {
    if (k < 0 || k > 0.6) return;
    ctx.beginPath(); ctx.arc(x, y, 14 + k * 140, 0, Math.PI * 2);
    ctx.strokeStyle = C.gold; ctx.globalAlpha = (1 - k / 0.6) * 0.8; ctx.lineWidth = 3; ctx.stroke(); ctx.globalAlpha = 1;
  }
  function litOf(i, t) { return smooth(LAND[i] - 0.15, LAND[i], t) * (1 - smooth(LAND[i] + STAY + 0.2, LAND[i] + STAY + 0.6, t)); }
  function squashOf(i, t) { var k = t - LAND[i]; return k >= 0 && k < 0.5 ? Math.sin(k / 0.5 * Math.PI) * Math.exp(-k * 4) : 0; }

  var STATION = [
    function prompt(s, t) {                       /* 01 : le texte se tape */
      var p = prog(0, t, 0.7), n = Math.round(TXT.prompt.length * p);
      text(TXT.prompt.slice(0, Math.max(1, n)), s.x - s.w / 2 + 26, s.y - 18, 22, C["on-night"]);
      if (p < 1 || Math.floor(t * 2) % 2) {
        ctx.font = "400 22px " + F.mono;
        var cw = ctx.measureText(TXT.prompt.slice(0, Math.max(1, n))).width;
        ctx.fillStyle = C.gold; ctx.fillRect(s.x - s.w / 2 + 30 + cw, s.y - 31, 11, 26);
      }
      text("tenon", s.x - s.w / 2 + 26, s.y + 32, 16, C["on-night-muted"]);
    },
    function permissions(s, t) {                  /* 02 : l'interrupteur bascule */
      var p = spring(prog(1, t, 1)), on = p > 0.5;
      text(TXT.ask, s.x - s.w / 2 + 24, s.y - 30, 19, C["on-night"]);
      var tx = s.x - s.w / 2 + 24, ty = s.y + 24;
      rr(tx, ty - 18, 76, 36, 18); ctx.fillStyle = on ? C.gold : C["night-rule"]; ctx.fill();
      ctx.beginPath(); ctx.arc(tx + 18 + 40 * clamp(p, 0, 1.08), ty, 13, 0, Math.PI * 2); ctx.fillStyle = on ? C["gold-ink"] : C["on-night-muted"]; ctx.fill();
      text(on ? TXT.allowed : TXT.denied, tx + 96, ty, 19, on ? C.gold : C["on-night-muted"], F.body, "left", 600);
    },
    function sandbox(s, t) {                      /* 03 : la cage s'allume, le shell répond */
      var p = prog(2, t, 0.8);
      for (var b = 0; b < 7; b++) {                /* barreaux, derrière le texte */
        var bx = s.x - s.w / 2 + 14 + b * (s.w - 28) / 6, on = p * 7 > b;
        ctx.fillStyle = on ? C.gold : C["night-rule"]; ctx.globalAlpha = on ? 0.75 : 0.5;
        ctx.fillRect(bx - 1.5, s.y - s.h / 2 - 22, 3, s.h + 44); ctx.globalAlpha = 1;
      }
      ctx.fillStyle = p > 0.95 ? C.gold : C["night-rule"];
      ctx.fillRect(s.x - s.w / 2 + 10, s.y - s.h / 2 - 26, s.w - 20, 5); ctx.fillRect(s.x - s.w / 2 + 10, s.y + s.h / 2 + 21, s.w - 20, 5);
      rr(s.x - s.w / 2 + 26, s.y - 46, 230, 40, 6); ctx.fillStyle = C["night-2"]; ctx.fill();
      text(TXT.shell, s.x - s.w / 2 + 38, s.y - 26, 21, C["on-night"]);
      if (p > 0.55) { rr(s.x - s.w / 2 + 26, s.y - 2, 150, 40, 6); ctx.fillStyle = C["night-2"]; ctx.fill(); text(TXT.shell_ok, s.x - s.w / 2 + 38, s.y + 18, 21, C.gold); }
    },
    function masking(s, t) {                      /* 04 : le secret traverse les lamelles et se masque */
      var p = prog(3, t, 0.7);
      var cut = Math.round(TXT.secret.length * (1 - p));
      ctx.save(); rr(s.x - s.w / 2 + 6, s.y - 40, s.w - 12, 80, 8); ctx.clip();
      text(p < 1 ? TXT.secret : TXT.masked, s.x - s.w / 2 + 28, s.y, 23, p < 1 ? C["on-night"] : C.gold);
      ctx.restore();
      for (var l = 0; l < 7; l++) {                /* lamelles qui balaient le texte */
        var lx = s.x - s.w / 2 + 30 + l * 48, sw = 10;
        ctx.fillStyle = C["night"]; ctx.globalAlpha = 0.85 * (1 - Math.abs(0.5 - p) * 2);
        ctx.fillRect(lx + p * 20, s.y - 36, sw, 72); ctx.globalAlpha = 1;
      }
      if (cut > 0 && p > 0 && p < 1) text("▌", s.x - s.w / 2 + 28 + 13.6 * (TXT.secret.length - cut), s.y, 23, C.gold);
    },
    function routing(s, t) {                      /* 05 : le tampon s'abat, l'aiguillage passe au local */
      var p = prog(4, t, 0.9), stamp = spring(clamp(p * 1.6, 0, 1));
      var ax = s.x + 70, ay = s.y + 8;
      /* document */
      rr(s.x - s.w / 2 + 22, s.y - 54, 150, 108, 6); ctx.fillStyle = C["3d-rail"]; ctx.fill();
      for (var r = 0; r < 4; r++) { ctx.fillStyle = C["3d-post"]; ctx.globalAlpha = 0.35; ctx.fillRect(s.x - s.w / 2 + 38, s.y - 36 + r * 20, 110 - r * 14, 6); }
      ctx.globalAlpha = 1;
      if (p > 0) {
        ctx.save(); ctx.translate(s.x - s.w / 2 + 97, s.y); ctx.rotate(-0.22); var sc = lerp(1.8, 1, stamp); ctx.scale(sc, sc);
        ctx.globalAlpha = clamp(stamp, 0, 1);
        ctx.font = "500 14px " + F.mono; var sw2 = ctx.measureText(TXT.stamp).width + 24;
        rr(-sw2 / 2, -16, sw2, 32, 4); ctx.strokeStyle = C.accent; ctx.lineWidth = 3; ctx.stroke();
        text(TXT.stamp, 0, 1, 14, C.accent, F.mono, "center", 500);
        ctx.restore(); ctx.globalAlpha = 1;
      }
      /* aiguillage : deux voies, l'aiguille bascule */
      var sw = smooth(0.35, 0.75, p);
      ctx.lineWidth = 4; ctx.lineCap = "round";
      ctx.strokeStyle = C["night-rule"];
      ctx.beginPath(); ctx.moveTo(ax, ay); ctx.lineTo(ax + 90, ay - 40); ctx.moveTo(ax, ay); ctx.lineTo(ax + 90, ay + 40); ctx.stroke();
      var ang = lerp(-0.42, 0.42, sw);
      ctx.strokeStyle = C.gold; ctx.beginPath(); ctx.moveTo(ax - 30, ay); ctx.lineTo(ax, ay); ctx.lineTo(ax + 98 * Math.cos(ang), ay + 98 * Math.sin(ang)); ctx.stroke();
      text(TXT.eu, ax + 104, ay - 40, 17, sw < 0.5 ? C["on-night"] : C["on-night-muted"], F.mono, "left", 500);
      text(TXT.local, ax + 104, ay + 40, 17, sw >= 0.5 ? C.gold : C["on-night-muted"], F.mono, "left", 500);
    },
    function models(s, t) {                       /* 06 : les douze étoiles s'allument au passage */
      var k = t - LAND[5], p = clamp(k / STAY, 0, 1) * RESET(t);
      for (var i = 0; i < 12; i++) {
        var a = -Math.PI / 2 + i * Math.PI / 6, on = p * 12 > i + 0.3 || (RESET(t) > 0 && k > STAY);
        star(s.x + RING_R * Math.cos(a), s.y + RING_R * Math.sin(a), on ? 12 : 9, on ? C.gold : C["night-rule"]);
      }
      rr(s.x - 70, s.y - 30, 140, 60, 10); ctx.fillStyle = C["night"]; ctx.fill(); ctx.strokeStyle = p > 0.9 ? C.gold : C["night-rule"]; ctx.lineWidth = 2; ctx.stroke();
      text(TXT.model, s.x, s.y, 17, C["on-night"], F.mono, "center", 500);
    },
    function audit(s, t) {                        /* 07 : un bloc tombe, le maillon se ferme, le hash défile */
      var p = prog(6, t, 0.8), drop = easeOut(clamp(p * 1.4, 0, 1));
      var bx0 = s.x - s.w / 2 + 34, by = s.y - 6;
      for (var b = 0; b < 5; b++) {
        var x = bx0 + b * 78, isNew = b === 4, y = isNew ? lerp(by - 160, by, drop) : by;
        if (isNew && p <= 0) continue;
        if (b > 0 && (!isNew || p > 0.6)) { ctx.fillStyle = isNew ? C.gold : C["night-rule"]; ctx.fillRect(x - 30, by + 18, 30, 6); }
        rr(x, y, 46, 46, 6); ctx.fillStyle = isNew ? C.gold : C["3d-post"]; ctx.fill();
        ctx.strokeStyle = C["night-rule"]; ctx.lineWidth = 1.5; ctx.stroke();
      }
      var hex = "a3f9c27e0b5d18e4";
      var shown = p > 0.6 ? hex.slice(0, Math.round(lerp(4, 16, smooth(0.6, 1, p)))) : "";
      text(TXT.block + " #5 · sha256 " + shown, bx0, s.y + 56, 16, p > 0.6 ? C.gold : C["on-night-muted"]);
    }
  ];

  /* Pistes reliant les stations dans l'ordre de visite. */
  function path(t, sc) {
    ctx.lineWidth = 2 / sc; ctx.setLineDash([10 / sc, 10 / sc]); ctx.strokeStyle = C["night-rule"];
    ctx.beginPath();
    S.forEach(function (s, i) { if (i === 0) ctx.moveTo(s.x, s.y); else ctx.lineTo(s.x, s.y); });
    ctx.stroke(); ctx.setLineDash([]);
  }
  function grid(sc) {
    ctx.strokeStyle = C["night-rule"]; ctx.globalAlpha = 0.35; ctx.lineWidth = 1 / sc;
    ctx.beginPath();
    for (var x = -400; x <= 2500; x += 100) { ctx.moveTo(x, -300); ctx.lineTo(x, 1600); }
    for (var y = -300; y <= 1600; y += 100) { ctx.moveTo(-400, y); ctx.lineTo(2500, y); }
    ctx.stroke(); ctx.globalAlpha = 1;
  }

  var W = 0, Hh = 0, dpr = 1;
  function resize() {
    dpr = Math.min(window.devicePixelRatio || 1, 2);
    W = canvas.clientWidth; Hh = canvas.clientHeight;
    canvas.width = Math.round(W * dpr); canvas.height = Math.round(Hh * dpr);
    draw(time);
  }

  /* seek(t) : dessine l'image à l'instant t, sans état. */
  function seek(t) {
    t = ((t % D) + D) % D;
    var c = cam(t);
    var tight = Math.min(W / (W < Hh ? 520 : 700), Hh / 430), wide = Math.min(W / 2250, Hh / 1350);
    var sc = lerp(wide, tight, c.z), rot = -0.07 * (1 - c.z);
    ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
    ctx.fillStyle = C.night; ctx.fillRect(0, 0, W, Hh);
    ctx.translate(W / 2, Hh / 2); ctx.rotate(rot); ctx.scale(sc, sc); ctx.translate(-c.x, -c.y);
    grid(sc);
    path(t, sc);
    for (var i = 0; i < S.length; i++) {
      var lit = litOf(i, t);
      if (i !== 5) card(S[i], lit, squashOf(i, t));
      STATION[i](S[i], t);
      label(i, S[i], lit > 0.5);
    }
    var h = hero(t);
    var sqx = 1, sqy = 1;
    if (h.land >= 0 && h.land < 0.4 && h.st !== 5) { var q = Math.sin(h.land / 0.4 * Math.PI) * Math.exp(-h.land * 5); sqx = 1 + 0.45 * q; sqy = 1 - 0.35 * q; }
    if (h.land >= 0 && h.st !== 5) ring(h.x, h.y + 14, h.land);
    /* halo doux sous l'étoile, puis l'étoile */
    var g = ctx.createRadialGradient(h.x, h.y, 2, h.x, h.y, 60);
    g.addColorStop(0, C.gold); g.addColorStop(1, "transparent");
    ctx.globalAlpha = 0.35; ctx.fillStyle = g; ctx.beginPath(); ctx.arc(h.x, h.y, 60, 0, Math.PI * 2); ctx.fill(); ctx.globalAlpha = 1;
    star(h.x, h.y + 14 * (1 - sqy), 22, C.gold, sqx, sqy, h.hop ? t * 4 : 0);
  }

  /* ── Lecture : joue quand visible, pause et relecture, image fixe en mouvement réduit ── */
  var time = reduce ? 11.6 : 0, playing = !reduce, visible = false, last = 0, raf = 0;
  var btn = document.getElementById("h-jr-toggle"), replay = document.getElementById("h-jr-replay"), fill = document.getElementById("h-jr-fill");
  function draw(t) { if (W > 0) { seek(t); fill.style.transform = "scaleX(" + (((t % D) + D) % D / D).toFixed(4) + ")"; } }
  function loop(now) {
    raf = 0;
    if (last) time += Math.min(0.05, (now - last) / 1000);
    last = now;
    draw(time);
    if (playing && visible) raf = requestAnimationFrame(loop); else last = 0;
  }
  function kick() { if (!raf && playing && visible) raf = requestAnimationFrame(loop); }
  function setPlaying(on) {
    playing = on; btn.textContent = on ? btn.getAttribute("data-pause") : btn.getAttribute("data-play");
    btn.setAttribute("aria-pressed", on ? "false" : "true");
    kick();
  }
  btn.addEventListener("click", function () { setPlaying(!playing); });
  replay.addEventListener("click", function () { time = 0; draw(0); setPlaying(true); });
  if (!playing) btn.textContent = btn.getAttribute("data-play");

  if ("IntersectionObserver" in window) {
    new IntersectionObserver(function (en) { visible = en[0].isIntersecting; kick(); }, { threshold: 0.35 }).observe(canvas);
  } else { visible = true; }
  if ("ResizeObserver" in window) new ResizeObserver(resize).observe(canvas); else window.addEventListener("resize", resize);
  document.addEventListener("visibilitychange", function () { if (document.hidden) { visible = false; } });

  var go = function () { resize(); kick(); };
  if (document.fonts && document.fonts.load) {
    Promise.all([document.fonts.load("600 20px " + F.body), document.fonts.load("500 20px " + F.mono)]).then(go, go);
  } else go();

  window.tenonJourney = { duration: D, seek: function (t) { time = t; draw(t); } };
})();
