/* tenon · « Le voyage d'une requête » : la boucle d'agent vue de l'intérieur.
 * Un anneau autour du moteur (masquage → routage → modèle → permissions → outil → résultat),
 * parcouru une fois par itération ; les garde-fous autour ; et, à côté, le journal d'audit en
 * direct avec les vrais types d'événements de tenon. Quatre chapitres, barre déplaçable.
 * seek(t) est une fonction pure du temps ; la caméra est précalculée par pas de 1/240 s. */
(function () {
  "use strict";
  var fig = document.getElementById("h-jr");
  var canvas = document.getElementById("h-jr-canvas");
  var dataEl = document.getElementById("h-jr-data");
  if (!fig || !canvas || !dataEl || !canvas.getContext) return;
  var TXT = JSON.parse(dataEl.textContent);
  var ctx = canvas.getContext("2d");
  var capEl = document.getElementById("h-jr-cap"), evEl = document.getElementById("h-jr-events");
  var proofEls = Array.prototype.slice.call(document.querySelectorAll("#h-jr-proofs li"));
  var chapEls = Array.prototype.slice.call(fig.querySelectorAll(".h-jr__chap"));
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
  var easeIO = function (x) { x = clamp(x, 0, 1); return x < 0.5 ? 4 * x * x * x : 1 - Math.pow(-2 * x + 2, 3) / 2; };
  var spring = function (x) { if (x <= 0) return 0; return 1 - Math.exp(-7 * x) * Math.cos(11 * x); };

  /* ── Géométrie (monde 1400 × 1000) ─────────────────────────────── */
  var CX = 720, CY = 510, R = 270;
  var RING = { redaction: -150, routing: -90, model: -30, permissions: 30, tool: 90, result: 150 };
  var ringPt = function (deg) { var a = deg * Math.PI / 180; return [CX + R * Math.cos(a), CY + R * Math.sin(a)]; };
  var N = {
    engine: { x: CX, y: CY, r: 96 },
    you: { x: 105, y: 510, w: 190, h: 104 },
    hooks: { x: 300, y: 205, w: 190, h: 88 },
    skills: { x: 300, y: 815, w: 190, h: 88 },
    checkpoints: { x: 1080, y: 865, w: 190, h: 88 },
    eu: { x: 1290, y: 175, w: 170, h: 120 }
  };
  Object.keys(RING).forEach(function (id) { var p = ringPt(RING[id]); N[id] = { x: p[0], y: p[1], w: 172, h: 84, ring: RING[id] }; });
  var FRAME = { x0: 195, y0: 108, x1: 1185, y1: 948 };

  /* ── Timeline : arrêts, légendes, événements du journal ─────────── */
  var K = [], EV = [], T0 = 1.3;
  function key(id, stay, cap, evs, zoom) { K.push({ id: id, stay: stay, cap: cap, evs: evs || [], zoom: !!zoom }); }
  key("you", 1.0, 1, [["UserMessage", TXT.ev.user]]);
  key("hooks", 0.9, 2, [["HookStarted", TXT.ev.hook_s], ["HookCompleted", TXT.ev.hook_c]]);
  key("engine", 0.6, 3);
  key("skills", 0.8, 3);
  key("engine", 0.4, 3);
  var CAPS = [[4, 4, 5, 5, 5, 6, 6], [7, 7, 7, 7, 8, 8, 8], [9, 9, 9, 9, 10, 10, 10], [11, 11, 11]];
  var CALL = [["ToolCallRequested", TXT.ev.read], ["ToolCallRequested", TXT.ev.write], ["ToolCallRequested", TXT.ev.shell], ["AssistantMessage", TXT.ev.answer]];
  var PERM = [[], [["PermissionRequested", TXT.ev.write], ["PermissionResolved", TXT.ev.allowed]], [["PermissionRequested", TXT.ev.shell], ["PermissionResolved", TXT.ev.allowed_net]]];
  var RES = [["ToolResult", TXT.ev.read_r], ["ToolResult", TXT.ev.write_r], ["ToolResult", TXT.ev.shell_r]];
  for (var it = 0; it < 4; it++) {
    var cp = CAPS[it];
    key("redaction", it === 0 ? 0.8 : 0.22, cp[0], it === 0 ? [["RedactionApplied", TXT.ev.redact]] : []);
    key("routing", it === 0 ? 1.2 : 0.22, cp[1], it === 0 ? [["SessionClassified", TXT.ev["class"]], ["ProviderSelected", TXT.ev.prov]] : [], it === 0);
    key("model", 0.9, cp[2], [CALL[it]]);
    if (it < 3) {
      key("permissions", it === 0 ? 0.5 : 1.15, cp[3], PERM[it], it === 1);
      key("tool", it === 0 ? 0.8 : it === 1 ? 1.25 : 2.0, cp[4], [RES[it]], it === 2);
      key("result", 0.22, cp[5]);
      key("engine", 0.35, cp[6]);
    } else {
      key("engine", 0.4, 11);
      key("you", 1.3, 11, [["TurnCompleted", TXT.ev.done]]);
    }
  }
  /* Temps : déplacement entre deux arrêts, plus court sur l'anneau. */
  var t = T0, nth = {};
  K.forEach(function (k, i) {
    if (i > 0) {
      var a = K[i - 1], ra = RING[a.id], rb = RING[k.id];
      var onRing = ra !== undefined && rb !== undefined;
      var dist = Math.hypot(N[k.id].x - N[a.id].x, N[k.id].y - N[a.id].y);
      t += onRing ? 0.42 : clamp(dist / 1300, 0.32, 0.6);
    }
    k.land = t; k.nth = nth[k.id] || 0; nth[k.id] = k.nth + 1;
    k.evs.forEach(function (e, j) { EV.push({ t: t + 0.25 + j * 0.3, type: e[0], detail: e[1] }); });
    t += k.stay;
  });
  var PROOF0 = t + 0.3, D = PROOF0 + 3.6 + 1.4;
  var PROOF_T = [PROOF0, PROOF0 + 1.1, PROOF0 + 2.2];
  var CHAP = [0, K.filter(function (k) { return k.id === "redaction"; })[0].land - 0.3,
              K.filter(function (k) { return k.id === "permissions"; })[1].land - 0.3,
              K.filter(function (k) { return k.id === "redaction"; })[3].land - 0.3];
  /* Hash factice et stable pour chaque ligne du journal. */
  EV.forEach(function (e, i) {
    var h = 2166136261;
    var str = e.type + i;
    for (var j = 0; j < str.length; j++) { h ^= str.charCodeAt(j); h = Math.imul(h, 16777619) >>> 0; }
    e.hash = ("00000000" + h.toString(16)).slice(-8);
  });
  var wrap = function (x) { return ((x % D) + D) % D; };
  var RESET = function (x) { return 1 - smooth(D - 1.4, D - 0.5, wrap(x)); };

  /* Position du héros : sur l'anneau il suit l'arc, ailleurs un saut en cloche. */
  var rest = function (id) { var n = N[id]; return [n.x, n.y - (n.r || n.h / 2) - 14]; };
  function hero(x) {
    x = wrap(x);
    for (var i = 0; i < K.length; i++) {
      var k = K[i], dep = k.land + k.stay;
      if (x >= k.land && x < dep) { var p = rest(k.id), q = x - k.land; return { x: p[0], y: p[1] - Math.abs(Math.sin(q * 9)) * 8 * Math.exp(-q * 6), land: q }; }
      if (i + 1 < K.length && x >= dep && x < K[i + 1].land) {
        var b = K[i + 1], u = easeIO((x - dep) / (b.land - dep));
        var ra = RING[k.id], rb = RING[b.id];
        if (ra !== undefined && rb !== undefined) {
          if (rb < ra) rb += 360;
          var pt = ringPt(lerp(ra, rb, u));
          return { x: pt[0], y: pt[1] - 56, land: -1, hop: 1 };
        }
        var a0 = rest(k.id), b0 = rest(b.id), H = 60 + Math.hypot(b0[0] - a0[0], b0[1] - a0[1]) * 0.15;
        return { x: lerp(a0[0], b0[0], u), y: lerp(a0[1], b0[1], u) - H * 4 * u * (1 - u), land: -1, hop: 1 };
      }
    }
    var y0 = rest("you");
    return { x: y0[0], y: y0[1], land: -1 };
  }
  function stopAt(x) {
    x = wrap(x);
    for (var i = 0; i < K.length; i++) if (x >= K[i].land - 0.05 && x < K[i].land + K[i].stay) return K[i];
    return null;
  }

  /* Caméra : vue d'ensemble, léger suivi, zoom sur trois moments forts. */
  var BASE = { x: 690, y: 525 };
  function camTarget(x) {
    x = wrap(x);
    var h = hero(x), s = stopAt(x), z = 1.08, cx = lerp(BASE.x, h.x, 0.18), cy = lerp(BASE.y, h.y, 0.18);
    if (x < T0 - 0.2 || x > PROOF0 - 0.2) { z = 1; cx = BASE.x; cy = BASE.y; }
    if (s && s.zoom) { z = 1.75; cx = N[s.id].x; cy = N[s.id].y + 20; }
    return { x: cx, y: cy, z: z };
  }
  var STEP = 1 / 240, NS = Math.round(D / STEP), CAM = new Float32Array(NS * 3);
  (function bake() {
    var c = camTarget(0), x = c.x, y = c.y, z = c.z, vx = 0, vy = 0, vz = 0, w = 5.5;
    for (var n = 0; n < NS * 2; n++) {
      var g = camTarget(n * STEP);
      vx += (w * w * (g.x - x) - 2 * w * vx) * STEP; x += vx * STEP;
      vy += (w * w * (g.y - y) - 2 * w * vy) * STEP; y += vy * STEP;
      vz += (w * w * (g.z - z) - 2 * w * vz) * STEP; z += vz * STEP;
      if (n >= NS) { var m = (n - NS) * 3; CAM[m] = x; CAM[m + 1] = y; CAM[m + 2] = z; }
    }
  })();
  function cam(x) {
    var f = wrap(x) / STEP, i = Math.floor(f) % NS, j = (i + 1) % NS, k = f - Math.floor(f);
    return { x: lerp(CAM[i * 3], CAM[j * 3], k), y: lerp(CAM[i * 3 + 1], CAM[j * 3 + 1], k), z: lerp(CAM[i * 3 + 2], CAM[j * 3 + 2], k) };
  }

  function last(id, x) {             /* dernier passage commencé : n-ième, progression, éclairage */
    x = wrap(x);
    var r = { nth: -1, p: 0, lit: 0 };
    if (RESET(x) < 0.5) return r;
    for (var i = 0; i < K.length; i++) {
      var k = K[i];
      if (k.id !== id || x < k.land - 0.12) continue;
      r.nth = k.nth; r.p = clamp((x - k.land) / Math.max(0.25, k.stay * 0.85), 0, 1);
      r.lit = smooth(k.land - 0.12, k.land, x) * (1 - smooth(k.land + k.stay, k.land + k.stay + 0.3, x));
    }
    return r;
  }
  function iteration(x) { x = wrap(x); var n = 0; K.forEach(function (k) { if (k.id === "model" && x >= k.land) n++; }); return RESET(x) < 0.5 ? 0 : n; }
  function capOf(x) {
    x = wrap(x);
    if (x < T0 - 0.3) return 0;
    if (x >= PROOF0 - 0.1) return 12;
    var c = 0; K.forEach(function (k) { if (x >= k.land - 0.25) c = k.cap; }); return c;
  }

  /* ── Dessin ─────────────────────────────────────────────────────── */
  function rr(x, y, w, h, r) {
    ctx.beginPath(); ctx.moveTo(x + r, y); ctx.arcTo(x + w, y, x + w, y + h, r); ctx.arcTo(x + w, y + h, x, y + h, r);
    ctx.arcTo(x, y + h, x, y, r); ctx.arcTo(x, y, x + w, y, r); ctx.closePath();
  }
  function text(s, x, y, size, color, font, align, weight) {
    ctx.font = (weight || 400) + " " + size + "px " + (font || F.mono);
    ctx.fillStyle = color; ctx.textAlign = align || "left"; ctx.textBaseline = "middle"; ctx.fillText(s, x, y);
  }
  function star(x, y, r, color, sx, sy, rot) {
    ctx.save(); ctx.translate(x, y); ctx.rotate(rot || 0); ctx.scale(sx || 1, sy || 1); ctx.beginPath();
    for (var i = 0; i < 10; i++) { var a = -Math.PI / 2 + i * Math.PI / 5, rad = i % 2 ? r * 0.45 : r; ctx.lineTo(Math.cos(a) * rad, Math.sin(a) * rad); }
    ctx.closePath(); ctx.fillStyle = color; ctx.fill(); ctx.restore();
  }
  function card(id, lit, dim) {
    var n = N[id], x = n.x - n.w / 2, y = n.y - n.h / 2;
    ctx.globalAlpha = dim ? 0.55 : 1;
    rr(x, y + 10, n.w, n.h, 12); ctx.fillStyle = C["3d-post"]; ctx.fill();
    rr(x, y, n.w, n.h, 12); ctx.fillStyle = C["night-2"]; ctx.fill();
    ctx.lineWidth = lit > 0.05 ? 2 + 2.5 * lit : 1.5; ctx.strokeStyle = lit > 0.05 ? C.gold : C["3d-edge"]; ctx.stroke();
    ctx.globalAlpha = 1;
  }
  function label(id, lit, below) {
    var n = N[id], y = n.y + (n.r || n.h / 2) + (below || 26);
    text(TXT.n[id], n.x, y, 19, lit ? C.gold : C["on-night"], F.body, "center", 600);
  }
  function bubble(s, x, y, gold) {
    ctx.font = "500 15px " + F.mono; var w = ctx.measureText(s).width + 26;
    rr(x - w / 2, y - 19, w, 38, 10); ctx.fillStyle = gold ? C.gold : C["on-night"]; ctx.fill();
    text(s, x, y, 15, gold ? C["gold-ink"] : C.night, F.mono, "center", 500);
  }
  function toggle(x, y, on, k) {
    rr(x, y - 16, 68, 32, 16); ctx.fillStyle = on ? C.gold : C["night-rule"]; ctx.fill();
    ctx.beginPath(); ctx.arc(x + 16 + 36 * clamp(k, 0, 1.08), y, 12, 0, Math.PI * 2); ctx.fillStyle = on ? C["gold-ink"] : C["on-night-muted"]; ctx.fill();
  }

  function drawRing(x, sc) {
    var looping = iteration(x) > 0 && x < PROOF0;
    ctx.save();
    ctx.beginPath(); ctx.arc(CX, CY, R, 0, Math.PI * 2);
    ctx.strokeStyle = looping ? C.gold : C["3d-edge"]; ctx.globalAlpha = looping ? 0.55 : 0.45; ctx.lineWidth = 6;
    ctx.setLineDash([18, 14]); ctx.lineDashOffset = looping ? -wrap(x) * 40 : 0; ctx.stroke(); ctx.setLineDash([]);
    ctx.globalAlpha = 1;
    for (var a = -120; a < 240; a += 60) {          /* chevrons : le sens de la boucle */
      var p = ringPt(a), ang = (a + 90) * Math.PI / 180;
      ctx.save(); ctx.translate(p[0], p[1]); ctx.rotate(ang);
      ctx.beginPath(); ctx.moveTo(-9, -9); ctx.lineTo(3, 0); ctx.lineTo(-9, 9);
      ctx.strokeStyle = looping ? C.gold : C["3d-edge"]; ctx.lineWidth = 4; ctx.lineCap = "round"; ctx.stroke(); ctx.restore();
    }
    text(TXT.n.loop, CX, CY - R - 74, 15, looping ? C.gold : C["on-night-muted"], F.mono, "center", 500);
    ctx.restore();
  }
  function drawEngine(x) {
    var L = last("engine", x), it = iteration(x), n = N.engine, act = L.lit;
    ctx.beginPath(); ctx.arc(n.x, n.y + 10, n.r, 0, Math.PI * 2); ctx.fillStyle = C["3d-post"]; ctx.fill();
    ctx.beginPath(); ctx.arc(n.x, n.y, n.r, 0, Math.PI * 2); ctx.fillStyle = C["night-2"]; ctx.fill();
    ctx.lineWidth = act > 0.05 ? 3 + 3 * act : 2; ctx.strokeStyle = act > 0.05 ? C.gold : C["3d-edge"]; ctx.stroke();
    for (var i = 0; i < it; i++) {                 /* une couronne par itération */
      ctx.beginPath(); ctx.arc(n.x, n.y, n.r - 12 - i * 9, 0, Math.PI * 2);
      ctx.strokeStyle = C.gold; ctx.globalAlpha = 0.35 + 0.15 * i; ctx.lineWidth = 3; ctx.stroke(); ctx.globalAlpha = 1;
    }
    text(it > 0 ? "↻ " + it : "↻", n.x, n.y - 8, 34, it > 0 ? C.gold : C["on-night-muted"], F.mono, "center", 500);
    text(it > 0 ? TXT.iter + " " + it + "/50" : TXT.n.engine, n.x, n.y + 30, 15, C["on-night-muted"], F.mono, "center", 500);
    label("engine", act > 0.5, 24);
  }
  function spokes() {
    ctx.strokeStyle = C["3d-edge"]; ctx.globalAlpha = 0.5; ctx.lineWidth = 2.5; ctx.setLineDash([8, 8]);
    ctx.beginPath();
    [["engine", "redaction"], ["result", "engine"], ["engine", "you"], ["engine", "hooks"], ["engine", "skills"], ["tool", "checkpoints"]].forEach(function (s) {
      ctx.moveTo(N[s[0]].x, N[s[0]].y); ctx.lineTo(N[s[1]].x, N[s[1]].y);
    });
    ctx.moveTo(N.routing.x, N.routing.y); ctx.lineTo(FRAME.x1, N.eu.y + 40); ctx.lineTo(N.eu.x - 80, N.eu.y + 40);
    ctx.stroke(); ctx.setLineDash([]); ctx.globalAlpha = 1;
  }
  function frame() {
    rr(FRAME.x0, FRAME.y0, FRAME.x1 - FRAME.x0, FRAME.y1 - FRAME.y0, 26);
    ctx.setLineDash([20, 12]); ctx.strokeStyle = C.gold; ctx.globalAlpha = 0.55; ctx.lineWidth = 3; ctx.stroke(); ctx.setLineDash([]); ctx.globalAlpha = 1;
    text(TXT.machine, FRAME.x0 + 26, FRAME.y1 - 26, 14, C.gold, F.mono, "left", 500);
  }

  var FACES = {
    you: function (x) {
      var L = last("you", x), n = N.you; card("you", L.lit, false);
      ctx.save(); rr(n.x - n.w / 2 + 6, n.y - n.h / 2 + 6, n.w - 12, n.h - 12, 8); ctx.clip();
      if (L.nth >= 1) {
        text("›", n.x - n.w / 2 + 16, n.y - 18, 18, C.gold);
        text(TXT.prompt.slice(0, 14) + "…", n.x - n.w / 2 + 34, n.y - 18, 14, C["on-night-muted"]);
        text(TXT.final, n.x - n.w / 2 + 16, n.y + 16, 17, C.gold, F.mono, "left", 500);
      } else {
        var s = TXT.prompt.slice(0, Math.round(TXT.prompt.length * L.p));
        text("›", n.x - n.w / 2 + 16, n.y - 12, 20, C.gold);
        text(s.length > 14 ? "…" + s.slice(-13) : s, n.x - n.w / 2 + 34, n.y - 12, 15, C["on-night"]);
        text("tenon", n.x - n.w / 2 + 16, n.y + 22, 13, C["on-night-muted"]);
      }
      ctx.restore(); label("you", L.lit > 0.5);
    },
    hooks: function (x) {
      var L = last("hooks", x), n = N.hooks, k = spring(L.p); card("hooks", L.lit, false);
      ctx.strokeStyle = L.nth >= 0 ? C.gold : C["3d-edge"]; ctx.lineWidth = 5; ctx.lineCap = "round";
      ctx.beginPath(); ctx.moveTo(n.x - 58, n.y - 22); ctx.lineTo(n.x - 58, n.y + 6); ctx.arc(n.x - 42, n.y + 6, 16, Math.PI, 0, true); ctx.stroke();
      if (L.nth >= 0) { ctx.globalAlpha = clamp(k, 0, 1); rr(n.x - 14, n.y - 16, 100, 32, 16); ctx.fillStyle = C.gold; ctx.fill();
        text(TXT.ctx, n.x + 36, n.y, 14, C["gold-ink"], F.mono, "center", 500); ctx.globalAlpha = 1; }
      label("hooks", L.lit > 0.5);
    },
    skills: function (x) {
      var L = last("skills", x), n = N.skills, k = easeOut(L.p); card("skills", L.lit, false);
      for (var i = 0; i < 3; i++) { rr(n.x - 62 + i * 6, n.y + 8 - i * 10, 120, 24, 5); ctx.fillStyle = C["3d-post"]; ctx.fill(); }
      if (L.nth >= 0) { rr(n.x - 50 + 34 * k, n.y - 30 - 4 * k, 120, 24, 5); ctx.fillStyle = C.gold; ctx.fill();
        text(TXT.skill, n.x + 10 + 34 * k, n.y - 18 - 4 * k, 13, C["gold-ink"], F.mono, "center", 500); }
      label("skills", L.lit > 0.5);
    },
    redaction: function (x) {
      var L = last("redaction", x), n = N.redaction, done = L.nth >= 1 || (L.nth === 0 && L.p > 0.55); card("redaction", L.lit, false);
      text(done ? TXT.masked : TXT.secret, n.x, n.y, 18, done ? C.gold : C["on-night"], F.mono, "center", 500);
      if (L.lit > 0.1) for (var l = 0; l < 5; l++) { ctx.fillStyle = C.night; ctx.globalAlpha = 0.85 * Math.sin(L.p * Math.PI); ctx.fillRect(n.x - 70 + l * 34 + L.p * 16, n.y - 22, 8, 44); }
      ctx.globalAlpha = 1; label("redaction", L.lit > 0.5);
    },
    routing: function (x) {
      var L = last("routing", x), n = N.routing, p = L.nth >= 1 ? 1 : L.nth === 0 ? L.p : 0; card("routing", L.lit, false);
      var st = spring(clamp(p * 1.7, 0, 1)), sw = smooth(0.4, 0.8, p), ax = n.x + 30, ay = n.y + 4;
      ctx.lineWidth = 4; ctx.lineCap = "round"; ctx.strokeStyle = C["night-rule"];
      ctx.beginPath(); ctx.moveTo(ax, ay); ctx.lineTo(ax + 44, ay - 22); ctx.moveTo(ax, ay); ctx.lineTo(ax + 44, ay + 22); ctx.stroke();
      var ang = lerp(-0.46, 0.46, sw);
      ctx.strokeStyle = C.gold; ctx.beginPath(); ctx.moveTo(ax - 18, ay); ctx.lineTo(ax, ay); ctx.lineTo(ax + 48 * Math.cos(ang), ay + 48 * Math.sin(ang)); ctx.stroke();
      text(TXT.eu, ax + 50, ay - 26, 12, sw < 0.5 ? C["on-night"] : C["on-night-muted"], F.mono, "left", 500);
      text(TXT.local, ax + 50, ay + 26, 12, sw >= 0.5 ? C.gold : C["on-night-muted"], F.mono, "left", 500);
      if (p > 0) {
        ctx.save(); ctx.translate(n.x - 38, n.y - 2); ctx.rotate(-0.22); var s = lerp(1.9, 1, st); ctx.scale(s, s); ctx.globalAlpha = clamp(st, 0, 1);
        ctx.font = "500 10px " + F.mono; var w = ctx.measureText(TXT.stamp).width + 14;
        rr(-w / 2, -11, w, 22, 3); ctx.strokeStyle = C.accent; ctx.lineWidth = 2.5; ctx.stroke();
        text(TXT.stamp, 0, 1, 10, C.accent, F.mono, "center", 500); ctx.restore(); ctx.globalAlpha = 1;
      }
      label("routing", L.lit > 0.5, 30);
    },
    model: function (x) {
      var L = last("model", x), n = N.model; card("model", L.lit, false);
      rr(n.x - 38, n.y - 20, 76, 40, 6); ctx.fillStyle = L.lit > 0.1 ? C.gold : C["3d-post"]; ctx.fill();
      for (var i = 0; i < 4; i++) { ctx.fillStyle = C["3d-edge"]; ctx.fillRect(n.x - 30 + i * 18, n.y - 28, 5, 8); ctx.fillRect(n.x - 30 + i * 18, n.y + 20, 5, 8); }
      if (L.nth >= 0 && L.lit > 0.05 && L.p > 0.15) { ctx.globalAlpha = clamp(spring(L.p), 0, 1) * clamp(L.lit * 1.6, 0, 1);
        bubble(TXT.replies[L.nth], n.x + 110, n.y - n.h / 2 - 34, L.nth === 3); ctx.globalAlpha = 1; }
      label("model", L.lit > 0.5);
    },
    permissions: function (x) {
      var L = last("permissions", x), n = N.permissions; card("permissions", L.lit, false);
      if (L.nth < 0) { toggle(n.x - 72, n.y, false, 0); text(TXT.perm_ask, n.x + 6, n.y, 15, C["on-night-muted"], F.body, "left", 600); }
      else if (L.nth === 0) { toggle(n.x - 72, n.y, true, 1); text(TXT.perm_auto, n.x + 6, n.y, 13, C.gold, F.mono, "left", 500); }
      else { var k = spring(clamp((L.p - 0.25) / 0.6, 0, 1)), on = k > 0.5; toggle(n.x - 72, n.y, on, k);
        text(on ? TXT.perm_ok : TXT.perm_ask, n.x + 6, n.y, 15, on ? C.gold : C["on-night"], F.body, "left", 600); }
      label("permissions", L.lit > 0.5);
    },
    tool: function (x) {
      var L = last("tool", x), n = N.tool; card("tool", L.lit, false);
      var nm = L.nth >= 0 ? TXT.tools[L.nth] : "read · write · shell";
      text(nm, n.x, n.y - 14, L.nth >= 0 ? 16 : 13, L.nth >= 0 ? C["on-night"] : C["on-night-muted"], F.mono, "center", 500);
      if (L.nth >= 0 && L.p > 0.55) text(TXT.tool_ok[L.nth], n.x, n.y + 18, 14, C.gold, F.mono, "center", 500);
      /* itération 2 : une copie part vers les points de reprise avant l'écriture */
      if (L.nth === 1 && L.lit > 0.05) {
        var k = easeOut(clamp(L.p / 0.6, 0, 1)), cx = lerp(n.x, N.checkpoints.x, k), cy = lerp(n.y, N.checkpoints.y, k) - 60 * 4 * k * (1 - k);
        rr(cx - 16, cy - 20, 32, 40, 4); ctx.fillStyle = C["3d-rail"]; ctx.fill();
        if (k > 0.2) text(TXT.snap, (n.x + N.checkpoints.x) / 2 + 20, N.checkpoints.y - 80, 13, C.gold, F.mono, "center", 500);
      }
      /* itération 3 : l'isolation système se referme, les sorties butent sur la paroi */
      if (L.nth === 2 && L.lit > 0.02) drawCage(n, L);
      label("tool", L.lit > 0.5, 32);
    },
    result: function (x) {
      var L = last("result", x), n = N.result; card("result", L.lit, false);
      rr(n.x - 52, n.y - 14, 104, 28, 14); ctx.fillStyle = L.lit > 0.1 ? C.gold : C["3d-post"]; ctx.fill();
      text("ToolResult", n.x, n.y, 12, L.lit > 0.1 ? C["gold-ink"] : C["on-night-muted"], F.mono, "center", 500);
      label("result", L.lit > 0.5);
    },
    checkpoints: function (x) {
      var L = last("tool", x), n = N.checkpoints, add = L.nth >= 1 ? 1 : 0; card("checkpoints", 0, false);
      for (var i = 0; i < 3 + add; i++) { ctx.fillStyle = i === 3 ? C.gold : C["3d-post"]; ctx.fillRect(n.x - 56 + i * 30, n.y + 22 - (i + 1) * 11, 24, (i + 1) * 11); }
      label("checkpoints", false);
    },
    eu: function () {
      var n = N.eu;
      for (var i = 0; i < 12; i++) { var a = -Math.PI / 2 + i * Math.PI / 6; star(n.x + 52 * Math.cos(a), n.y + 52 * Math.sin(a), 8, C["3d-edge"]); }
      text("eu-only", FRAME.x1, N.eu.y + 40, 12, C["on-night-muted"], F.mono, "center", 500);
      ctx.globalAlpha = 0.7; label("eu", false, 38); ctx.globalAlpha = 1;
    }
  };
  function drawCage(n, L) {
    var close = easeOut(clamp(L.p / 0.25, 0, 1)) * clamp(L.lit * 2, 0, 1);
    var x0 = n.x - n.w / 2 - 26, x1 = n.x + n.w / 2 + 26, y0 = n.y - n.h / 2 - 30, y1 = n.y + n.h / 2 + 34;
    ctx.globalAlpha = close;
    rr(x0, y0, x1 - x0, y1 - y0, 14); ctx.strokeStyle = C.gold; ctx.lineWidth = 4; ctx.stroke();
    for (var b = 0; b < 9; b++) {
      var bx = x0 + 10 + b * (x1 - x0 - 20) / 8;
      ctx.fillStyle = C.gold; ctx.fillRect(bx - 2, lerp(y0 - 60, y0, close), 4, 18); ctx.fillRect(bx - 2, y1 - 18, 4, 18);
    }
    text(TXT.n.sandbox + " · " + TXT.sb, n.x, y1 + 46, 14, C.gold, F.mono, "center", 500);
    ctx.globalAlpha = 1;
    var targets = [[x0, n.y - 6, TXT.escape[0], -1], [x1, n.y - 6, TXT.escape[1], 1]];
    for (var e = 0; e < 2; e++) {
      var k0 = clamp((L.p - 0.35 - e * 0.2) / 0.25, 0, 1);
      if (k0 <= 0) continue;
      var tx = targets[e][0], ex = lerp(n.x, tx, easeOut(k0));
      ctx.setLineDash([5, 5]); ctx.strokeStyle = C["on-night"]; ctx.lineWidth = 2.5;
      ctx.beginPath(); ctx.moveTo(n.x, n.y + 2); ctx.lineTo(ex, n.y - 6); ctx.stroke(); ctx.setLineDash([]);
      if (k0 >= 1) {
        ctx.strokeStyle = C.gold; ctx.lineWidth = 4; ctx.beginPath();
        ctx.moveTo(tx - 8, n.y - 14); ctx.lineTo(tx + 8, n.y + 2); ctx.moveTo(tx + 8, n.y - 14); ctx.lineTo(tx - 8, n.y + 2); ctx.stroke();
        text(targets[e][2] + " · " + TXT.refused, tx + targets[e][3] * 16, n.y - 6, 13, C.gold, F.mono, targets[e][3] < 0 ? "right" : "left", 500);
      }
    }
  }

  var W = 0, Hh = 0, dpr = 1;
  function seek(x) {
    x = wrap(x);
    var c = cam(x), fit = Math.min(W / 1440, Hh / 930), sc = fit * c.z;
    ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
    ctx.fillStyle = C.night; ctx.fillRect(0, 0, W, Hh);
    ctx.translate(W / 2, Hh / 2); ctx.scale(sc, sc); ctx.translate(-c.x, -c.y);
    frame(); spokes(); drawRing(x, sc);
    ["you", "hooks", "skills", "checkpoints", "eu", "redaction", "routing", "model", "permissions", "result", "tool"].forEach(function (id) { FACES[id](x); });
    drawEngine(x);
    var h = hero(x), sqx = 1, sqy = 1;
    if (h.land >= 0 && h.land < 0.35) { var q = Math.sin(h.land / 0.35 * Math.PI) * Math.exp(-h.land * 5); sqx = 1 + 0.4 * q; sqy = 1 - 0.3 * q; }
    if (h.land >= 0 && h.land < 0.5) { ctx.beginPath(); ctx.arc(h.x, h.y + 12, 12 + h.land * 120, 0, Math.PI * 2);
      ctx.strokeStyle = C.gold; ctx.globalAlpha = (1 - h.land / 0.5) * 0.8; ctx.lineWidth = 3; ctx.stroke(); ctx.globalAlpha = 1; }
    var g = ctx.createRadialGradient(h.x, h.y, 2, h.x, h.y, 60); g.addColorStop(0, C.gold); g.addColorStop(1, "transparent");
    ctx.globalAlpha = 0.35; ctx.fillStyle = g; ctx.beginPath(); ctx.arc(h.x, h.y, 60, 0, Math.PI * 2); ctx.fill(); ctx.globalAlpha = 1;
    star(h.x, h.y + 12 * (1 - sqy), 22, C.gold, sqx, sqy, h.hop ? x * 4 : 0);
    syncDom(x);
  }

  /* ── Panneau, légende, chapitres, barre (DOM, mis à jour seulement quand ça change) ── */
  var lastSig = "", lastCap = -1, lastChap = -1, lastPct = -1;
  function syncDom(x) {
    var shown = [], newest = -1;
    if (RESET(x) > 0.5) EV.forEach(function (e, i) { if (x >= e.t) { shown.push(i); newest = i; } });
    var isNew = newest >= 0 && x - EV[newest].t < 0.9, sig = shown.length + ":" + isNew;
    if (sig !== lastSig) {
      lastSig = sig;
      var html = "";
      shown.slice(-12).forEach(function (i) {
        var e = EV[i], nn = ("0" + (i + 1)).slice(-2);
        html += '<li' + (i === newest && isNew ? ' class="is-new"' : "") + "><span>#" + nn + "</span><i><b>" + e.type + "</b> " + esc(e.detail) + "</i><em>" + e.hash.slice(0, 6) + "…</em></li>";
      });
      evEl.innerHTML = html;
    }
    proofEls.forEach(function (el, i) { el.classList.toggle("is-on", RESET(x) > 0.5 && x >= PROOF_T[i]); });
    var cp = capOf(x);
    if (cp !== lastCap) { lastCap = cp; capEl.textContent = TXT.cap[cp]; }
    var ch = 0; CHAP.forEach(function (s, i) { if (x >= s) ch = i; });
    if (ch !== lastChap) { lastChap = ch; chapEls.forEach(function (b, i) { b.classList.toggle("is-active", i === ch); b.setAttribute("aria-current", i === ch ? "step" : "false"); }); }
    var pct = Math.round(x / D * 100);
    fill.style.transform = "scaleX(" + (x / D).toFixed(4) + ")";
    if (pct !== lastPct) { lastPct = pct; track.setAttribute("aria-valuenow", String(pct)); }
  }
  function esc(s) { return String(s).replace(/[&<>"]/g, function (c) { return { "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" }[c]; }); }

  var time = reduce ? PROOF0 + 3.4 : 0, playing = !reduce, visible = false, lastT = 0, raf = 0;
  /* Lien profond : #voyage=12.5 ouvre l'animation en pause à cet instant. */
  var deep = /(?:^|[#&])voyage=([\d.]+)/.exec(location.hash);
  if (deep) { time = wrap(parseFloat(deep[1])); playing = false; }
  var btn = document.getElementById("h-jr-toggle"), replay = document.getElementById("h-jr-replay");
  var fill = document.getElementById("h-jr-fill"), track = document.getElementById("h-jr-track"), marks = document.getElementById("h-jr-marks");
  CHAP.forEach(function (s) { var i = document.createElement("i"); i.style.left = (s / D * 100).toFixed(2) + "%"; marks.appendChild(i); });
  function draw(x) { if (W > 0) seek(x); }
  function resize() {
    dpr = Math.min(window.devicePixelRatio || 1, 2);
    W = canvas.clientWidth; Hh = canvas.clientHeight;
    canvas.width = Math.round(W * dpr); canvas.height = Math.round(Hh * dpr);
    draw(time);
  }
  function loop(now) {
    raf = 0;
    if (lastT) time = wrap(time + Math.min(0.05, (now - lastT) / 1000));
    lastT = now; draw(time);
    if (playing && visible) raf = requestAnimationFrame(loop); else lastT = 0;
  }
  function kick() { if (!raf && playing && visible) raf = requestAnimationFrame(loop); }
  function setPlaying(on) {
    playing = on; btn.textContent = on ? btn.getAttribute("data-pause") : btn.getAttribute("data-play");
    btn.setAttribute("aria-pressed", on ? "false" : "true"); kick();
  }
  function goTo(x) { time = wrap(x); draw(time); }
  btn.addEventListener("click", function () { setPlaying(!playing); });
  replay.addEventListener("click", function () { goTo(0); setPlaying(true); });
  chapEls.forEach(function (b, i) { b.addEventListener("click", function () { goTo(CHAP[i] + 0.01); setPlaying(true); }); });
  var scrubbing = false;
  function scrubAt(e) { var r = track.getBoundingClientRect(); goTo(clamp((e.clientX - r.left) / r.width, 0, 0.999) * D); }
  track.addEventListener("pointerdown", function (e) { scrubbing = true; track.setPointerCapture(e.pointerId); scrubAt(e); });
  track.addEventListener("pointermove", function (e) { if (scrubbing) scrubAt(e); });
  track.addEventListener("pointerup", function () { scrubbing = false; });
  track.addEventListener("keydown", function (e) {
    if (e.key === "ArrowRight") goTo(time + 2); else if (e.key === "ArrowLeft") goTo(time - 2);
    else if (e.key === "Home") goTo(0); else if (e.key === "End") goTo(D - 0.01); else return;
    e.preventDefault();
  });
  if (!playing) btn.textContent = btn.getAttribute("data-play");
  if ("IntersectionObserver" in window) new IntersectionObserver(function (en) { visible = en[0].isIntersecting; kick(); }, { threshold: 0.3 }).observe(canvas);
  else visible = true;
  if ("ResizeObserver" in window) new ResizeObserver(resize).observe(canvas); else window.addEventListener("resize", resize);
  var go = function () { resize(); kick(); };
  if (document.fonts && document.fonts.load) Promise.all([document.fonts.load("600 20px " + F.body), document.fonts.load("500 20px " + F.mono)]).then(go, go); else go();

  window.tenonJourney = { duration: D, stops: K, chapters: CHAP, events: EV, seek: function (x) { time = x; draw(x); } };
})();
