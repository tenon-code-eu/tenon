/* tenon · page d'accueil : copie, onglets, palette ⌘K et assemblage 3D (WebGL écrit à la main, sans bibliothèque). */
(function () {
  "use strict";
  var reduce = window.matchMedia("(prefers-reduced-motion: reduce)").matches;

  /* ── Copie : succès silencieux, libellé échangé 1,5 s ─────────────── */
  function copyText(btn) {
    var text = btn.getAttribute("data-copy");
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

  /* ── Assemblage tenon-mortaise en 3D ────────────────────────────────── */
  var stage = document.getElementById("h-stage");
  var canvas = document.getElementById("h-joint");
  var toggle = document.getElementById("h-joint-toggle");
  if (!stage || !canvas) return;
  var gl = canvas.getContext("webgl", { antialias: true, alpha: true, premultipliedAlpha: true });
  if (!gl) { stage.classList.add("is-fallback"); return; }

  /* Couleurs lues depuis les jetons CSS (oklch → sRGB linéaire). */
  function oklchToLinear(str) {
    var m = /oklch\(\s*([\d.]+)%\s+([\d.]+)\s+([\d.]+)/.exec(str || "");
    if (!m) return [0.5, 0.5, 0.5];
    var L = parseFloat(m[1]) / 100, C = parseFloat(m[2]), h = parseFloat(m[3]) * Math.PI / 180;
    var a = C * Math.cos(h), b = C * Math.sin(h);
    var l = Math.pow(L + 0.3963377774 * a + 0.2158037573 * b, 3);
    var mm = Math.pow(L - 0.1055613458 * a - 0.0638541728 * b, 3);
    var s = Math.pow(L - 0.0894841775 * a - 1.2914855480 * b, 3);
    var clamp = function (x) { return Math.max(0, Math.min(1, x)); };
    return [clamp(4.0767416621 * l - 3.3077115913 * mm + 0.2309699292 * s),
            clamp(-1.2684380046 * l + 2.6097574011 * mm - 0.3413193965 * s),
            clamp(-0.0041960863 * l - 0.7034186147 * mm + 1.7076147010 * s)];
  }
  var css = getComputedStyle(document.documentElement);
  var tok = function (n) { return oklchToLinear(css.getPropertyValue(n)); };
  var COL = { post: tok("--color-3d-post"), rail: tok("--color-3d-rail"), tenon: tok("--color-3d-tenon"),
              edge: tok("--color-3d-edge"), star: tok("--color-gold") };

  /* Petite algèbre 4×4 (colonnes majeures). */
  function mul(a, b) {
    var o = new Float32Array(16);
    for (var c = 0; c < 4; c++) for (var r = 0; r < 4; r++) {
      o[c * 4 + r] = a[r] * b[c * 4] + a[4 + r] * b[c * 4 + 1] + a[8 + r] * b[c * 4 + 2] + a[12 + r] * b[c * 4 + 3];
    }
    return o;
  }
  function perspective(fov, asp, n, f) {
    var t = 1 / Math.tan(fov / 2), o = new Float32Array(16);
    o[0] = t / asp; o[5] = t; o[10] = (f + n) / (n - f); o[11] = -1; o[14] = 2 * f * n / (n - f); return o;
  }
  function translate(x, y, z) { var o = new Float32Array([1,0,0,0, 0,1,0,0, 0,0,1,0, x,y,z,1]); return o; }
  function rotX(a) { var c = Math.cos(a), s = Math.sin(a); return new Float32Array([1,0,0,0, 0,c,s,0, 0,-s,c,0, 0,0,0,1]); }
  function rotY(a) { var c = Math.cos(a), s = Math.sin(a); return new Float32Array([c,0,-s,0, 0,1,0,0, s,0,c,0, 0,0,0,1]); }

  /* Géométrie : chaque pièce est une union de pavés. */
  function boxes(list) {
    var tri = [], lines = [];
    list.forEach(function (b) {
      var x0 = b[0], x1 = b[1], y0 = b[2], y1 = b[3], z0 = b[4], z1 = b[5];
      var faces = [
        [[x1,y0,z0],[x1,y1,z0],[x1,y1,z1],[x1,y0,z1],[1,0,0]],
        [[x0,y0,z1],[x0,y1,z1],[x0,y1,z0],[x0,y0,z0],[-1,0,0]],
        [[x0,y1,z0],[x0,y1,z1],[x1,y1,z1],[x1,y1,z0],[0,1,0]],
        [[x0,y0,z1],[x0,y0,z0],[x1,y0,z0],[x1,y0,z1],[0,-1,0]],
        [[x0,y0,z1],[x1,y0,z1],[x1,y1,z1],[x0,y1,z1],[0,0,1]],
        [[x1,y0,z0],[x0,y0,z0],[x0,y1,z0],[x1,y1,z0],[0,0,-1]]
      ];
      faces.forEach(function (f) {
        var n = f[4];
        [0, 1, 2, 0, 2, 3].forEach(function (i) { tri.push(f[i][0], f[i][1], f[i][2], n[0], n[1], n[2]); });
      });
      var c = [[x0,y0,z0],[x1,y0,z0],[x1,y1,z0],[x0,y1,z0],[x0,y0,z1],[x1,y0,z1],[x1,y1,z1],[x0,y1,z1]];
      [[0,1],[1,2],[2,3],[3,0],[4,5],[5,6],[6,7],[7,4],[0,4],[1,5],[2,6],[3,7]].forEach(function (e) {
        lines.push(c[e[0]][0], c[e[0]][1], c[e[0]][2], c[e[1]][0], c[e[1]][1], c[e[1]][2]);
      });
    });
    return { tri: new Float32Array(tri), lines: new Float32Array(lines) };
  }
  /* Montant percé d'une mortaise ouverte sur sa face +x. */
  var POST = boxes([
    [-1, -0.2, -2.2, 2.2, -1, 1],
    [-0.2, 1, 0.8, 2.2, -1, 1],
    [-0.2, 1, -2.2, -0.8, -1, 1],
    [-0.2, 1, -0.8, 0.8, -1, -0.3],
    [-0.2, 1, -0.8, 0.8, 0.3, 1]
  ]);
  var RAIL = boxes([[1, 5.2, -1.15, 1.15, -0.8, 0.8]]);
  var TENON = boxes([[-0.2, 1, -0.78, 0.78, -0.28, 0.28]]);
  var STARS = (function () {
    var p = [];
    for (var i = 0; i < 12; i++) {
      var a = i / 12 * Math.PI * 2;
      p.push(2.2 + 4.3 * Math.cos(a), 4.3 * Math.sin(a), -0.2);
    }
    return new Float32Array(p);
  })();

  function shader(type, src) {
    var s = gl.createShader(type); gl.shaderSource(s, src); gl.compileShader(s); return s;
  }
  function program(vs, fs) {
    var p = gl.createProgram();
    gl.attachShader(p, shader(gl.VERTEX_SHADER, vs)); gl.attachShader(p, shader(gl.FRAGMENT_SHADER, fs));
    gl.linkProgram(p);
    if (!gl.getProgramParameter(p, gl.LINK_STATUS)) throw new Error("link");
    return p;
  }
  var lit, flat, star;
  try {
    lit = program(
      "attribute vec3 p; attribute vec3 n; uniform mat4 mvp; uniform mat4 model; varying vec3 vN;" +
      "void main(){ vN = mat3(model) * n; gl_Position = mvp * vec4(p, 1.0); }",
      "precision mediump float; uniform vec3 color; varying vec3 vN;" +
      "void main(){ vec3 N = normalize(vN); float key = max(dot(N, normalize(vec3(0.45, 0.8, 0.6))), 0.0);" +
      "float fill = max(dot(N, normalize(vec3(-0.7, -0.2, 0.4))), 0.0); float sky = 0.5 + 0.5 * N.y;" +
      "vec3 c = color * (0.22 + 0.18 * sky + 0.62 * key + 0.16 * fill);" +
      "gl_FragColor = vec4(pow(c, vec3(1.0 / 2.2)), 1.0); }");
    flat = program(
      "attribute vec3 p; uniform mat4 mvp; void main(){ gl_Position = mvp * vec4(p, 1.0); }",
      "precision mediump float; uniform vec3 color; uniform float alpha;" +
      "void main(){ gl_FragColor = vec4(pow(color, vec3(1.0 / 2.2)) * alpha, alpha); }");
    star = program(
      "attribute vec3 p; uniform mat4 mvp; uniform float size;" +
      "void main(){ gl_Position = mvp * vec4(p, 1.0); gl_PointSize = size / gl_Position.w; }",
      "precision mediump float; uniform vec3 color;" +
      "void main(){ vec2 q = gl_PointCoord * 2.0 - 1.0; float r = length(q); float a = atan(q.x, -q.y);" +
      "float k = 0.5 + 0.5 * cos(5.0 * a); float edge = mix(0.42, 0.95, pow(k, 3.0));" +
      "float m = 1.0 - smoothstep(edge - 0.08, edge, r); if (m <= 0.01) discard;" +
      "gl_FragColor = vec4(pow(color, vec3(1.0 / 2.2)) * m, m); }");
  } catch (e) { stage.classList.add("is-fallback"); return; }

  function buffer(data) { var b = gl.createBuffer(); gl.bindBuffer(gl.ARRAY_BUFFER, b); gl.bufferData(gl.ARRAY_BUFFER, data, gl.STATIC_DRAW); return b; }
  var G = {
    post: { tri: buffer(POST.tri), n: POST.tri.length / 6, lines: buffer(POST.lines), ln: POST.lines.length / 3 },
    rail: { tri: buffer(RAIL.tri), n: RAIL.tri.length / 6, lines: buffer(RAIL.lines), ln: RAIL.lines.length / 3 },
    tenon: { tri: buffer(TENON.tri), n: TENON.tri.length / 6, lines: buffer(TENON.lines), ln: TENON.lines.length / 3 },
    stars: { buf: buffer(STARS), n: 12 }
  };

  /* État : orientation (inertie), écart du rail, demande de rendu. */
  var yaw = -0.75, pitch = 0.38, vyaw = 0, vpitch = 0;
  var gap = reduce ? 0 : 3.2, target = 0, dpr = 1, raf = 0, onScreen = true;
  var dragging = false, moved = 0, lx = 0, ly = 0;

  function resize() {
    dpr = Math.min(window.devicePixelRatio || 1, 2);
    var w = Math.round(canvas.clientWidth * dpr), h = Math.round(canvas.clientHeight * dpr);
    if (canvas.width !== w || canvas.height !== h) { canvas.width = w; canvas.height = h; }
    gl.viewport(0, 0, canvas.width, canvas.height);
    request();
  }
  function drawPart(part, model, vp, color) {
    var mvp = mul(vp, model);
    gl.useProgram(lit);
    gl.bindBuffer(gl.ARRAY_BUFFER, part.tri);
    var pl = gl.getAttribLocation(lit, "p"), nl = gl.getAttribLocation(lit, "n");
    gl.enableVertexAttribArray(pl); gl.enableVertexAttribArray(nl);
    gl.vertexAttribPointer(pl, 3, gl.FLOAT, false, 24, 0); gl.vertexAttribPointer(nl, 3, gl.FLOAT, false, 24, 12);
    gl.uniformMatrix4fv(gl.getUniformLocation(lit, "mvp"), false, mvp);
    gl.uniformMatrix4fv(gl.getUniformLocation(lit, "model"), false, model);
    gl.uniform3fv(gl.getUniformLocation(lit, "color"), color);
    gl.enable(gl.POLYGON_OFFSET_FILL); gl.polygonOffset(1, 1);
    gl.drawArrays(gl.TRIANGLES, 0, part.n);
    gl.disable(gl.POLYGON_OFFSET_FILL);
    gl.disableVertexAttribArray(nl);
    gl.useProgram(flat);
    gl.bindBuffer(gl.ARRAY_BUFFER, part.lines);
    var fl = gl.getAttribLocation(flat, "p");
    gl.enableVertexAttribArray(fl); gl.vertexAttribPointer(fl, 3, gl.FLOAT, false, 12, 0);
    gl.uniformMatrix4fv(gl.getUniformLocation(flat, "mvp"), false, mvp);
    gl.uniform3fv(gl.getUniformLocation(flat, "color"), COL.edge);
    gl.uniform1f(gl.getUniformLocation(flat, "alpha"), 0.75);
    gl.drawArrays(gl.LINES, 0, part.ln);
  }
  function frame() {
    raf = 0;
    if (!dragging) {
      yaw += vyaw; pitch += vpitch; vyaw *= 0.92; vpitch *= 0.92;
      if (Math.abs(vyaw) < 1e-4) vyaw = 0;
      if (Math.abs(vpitch) < 1e-4) vpitch = 0;
    }
    pitch = Math.max(-0.9, Math.min(1.1, pitch));
    gap += (target - gap) * (reduce ? 1 : 0.085);
    if (Math.abs(target - gap) < 0.002) gap = target;

    gl.clearColor(0, 0, 0, 0);
    gl.clear(gl.COLOR_BUFFER_BIT | gl.DEPTH_BUFFER_BIT);
    gl.enable(gl.DEPTH_TEST);
    gl.enable(gl.BLEND); gl.blendFunc(gl.ONE, gl.ONE_MINUS_SRC_ALPHA);

    var asp = canvas.width / Math.max(1, canvas.height);
    var proj = perspective(0.62, asp, 0.1, 100);
    var view = translate(0, -0.1, asp < 1 ? -19 : -16.5);
    var orient = mul(rotX(pitch), rotY(yaw));
    var center = translate(-2.2 - gap * 0.5, 0, 0);
    var vp = mul(proj, mul(view, orient));
    var base = center;
    var slide = mul(center, translate(gap, 0, 0));

    drawPart(G.post, base, vp, COL.post);
    drawPart(G.rail, slide, vp, COL.rail);
    drawPart(G.tenon, slide, vp, COL.tenon);

    gl.useProgram(star);
    gl.bindBuffer(gl.ARRAY_BUFFER, G.stars.buf);
    var sl = gl.getAttribLocation(star, "p");
    gl.enableVertexAttribArray(sl); gl.vertexAttribPointer(sl, 3, gl.FLOAT, false, 12, 0);
    gl.uniformMatrix4fv(gl.getUniformLocation(star, "mvp"), false, mul(vp, translate(-2.2, 0, 0)));
    gl.uniform1f(gl.getUniformLocation(star, "size"), 150 * dpr);
    gl.uniform3fv(gl.getUniformLocation(star, "color"), COL.star);
    gl.drawArrays(gl.POINTS, 0, G.stars.n);

    if (onScreen && (dragging || vyaw || vpitch || gap !== target)) request();
  }
  function request() { if (!raf) raf = requestAnimationFrame(frame); }

  function setTarget(joined) {
    target = joined ? 0 : 3.2;
    toggle.textContent = joined ? toggle.getAttribute("data-separate") : toggle.getAttribute("data-assemble");
    request();
  }
  function flip() { setTarget(target !== 0); }

  canvas.addEventListener("pointerdown", function (e) {
    dragging = true; moved = 0; lx = e.clientX; ly = e.clientY; vyaw = vpitch = 0;
    canvas.setPointerCapture(e.pointerId);
  });
  canvas.addEventListener("pointermove", function (e) {
    if (!dragging) return;
    var dx = e.clientX - lx, dy = e.clientY - ly; lx = e.clientX; ly = e.clientY;
    moved += Math.abs(dx) + Math.abs(dy);
    vyaw = dx * 0.008; vpitch = dy * 0.006;
    yaw += vyaw; pitch += vpitch;
    request();
  });
  var end = function (e) {
    if (!dragging) return;
    dragging = false;
    if (moved < 6) { vyaw = vpitch = 0; flip(); }
    if (reduce) { vyaw = vpitch = 0; }
    try { canvas.releasePointerCapture(e.pointerId); } catch (x) {}
    request();
  };
  canvas.addEventListener("pointerup", end);
  canvas.addEventListener("pointercancel", end);
  canvas.addEventListener("keydown", function (e) {
    var step = 0.12;
    if (e.key === "ArrowLeft") { yaw -= step; } else if (e.key === "ArrowRight") { yaw += step; }
    else if (e.key === "ArrowUp") { pitch -= step; } else if (e.key === "ArrowDown") { pitch += step; }
    else if (e.key === "Enter" || e.key === " ") { flip(); } else { return; }
    e.preventDefault(); request();
  });
  toggle.addEventListener("click", flip);

  if ("ResizeObserver" in window) new ResizeObserver(resize).observe(canvas); else window.addEventListener("resize", resize);
  if ("IntersectionObserver" in window) {
    new IntersectionObserver(function (en) { onScreen = en[0].isIntersecting; if (onScreen) request(); }).observe(canvas);
  }
  canvas.addEventListener("webglcontextlost", function (e) { e.preventDefault(); stage.classList.add("is-fallback"); });

  resize();
  /* Un seul mouvement au chargement : le rail vient s'emboîter, puis la scène attend l'utilisateur. */
  setTimeout(function () { setTarget(true); }, reduce ? 0 : 450);
})();
