/* tenon · page d'accueil : deux animations en HTML et CSS, sans canvas.
 * « tenon, en vrai » : la maquette de l'interface du terminal (ouverture, accueil, demande,
 * fil et panneau latéral, réponse et badge), avec un bouton de thème.
 * « tenon dans vos pipelines » : le job CI en quatre étapes et son journal.
 * Chaque animation est une fonction pure du temps (render(t)) : la boucle, la pause au survol
 * ou au focus, et les liens profonds #tui=9.7 et #ci=8 (image figée à cet instant) s'en servent.
 * En mouvement réduit : la dernière image, sans boucle. */
(function () {
  "use strict";
  var reduce = window.matchMedia("(prefers-reduced-motion: reduce)").matches;

  /* Lecteur commun : boucle, visibilité, pause au survol et au focus, boutons, lien profond. */
  function player(root, area, D, END, render, deepKey, btn, replay) {
    var time = reduce ? END : 0, playing = !reduce, visible = false, hover = false, raf = 0, lastT = 0;
    var deep = new RegExp("(?:^|[#&])" + deepKey + "=([\\d.]+)").exec(location.hash);
    if (deep) { time = Math.min(D, parseFloat(deep[1])); playing = false; }
    function draw() { render(time); }
    function loop(now) {
      raf = 0;
      if (lastT) { time += Math.min(0.05, (now - lastT) / 1000); if (time >= D) time = 0; }
      lastT = now; draw();
      if (playing && visible && !hover) raf = requestAnimationFrame(loop); else lastT = 0;
    }
    function kick() { if (!raf && playing && visible && !hover) raf = requestAnimationFrame(loop); }
    function label() {
      btn.textContent = playing ? btn.getAttribute("data-pause") : btn.getAttribute("data-play");
      btn.setAttribute("aria-pressed", playing ? "false" : "true");
    }
    btn.addEventListener("click", function () { playing = !playing; label(); kick(); });
    replay.addEventListener("click", function () { time = 0; playing = true; label(); draw(); kick(); });
    /* Pause au survol et au focus de la maquette seulement : les boutons restent utilisables. */
    area.addEventListener("mouseenter", function () { hover = true; });
    area.addEventListener("mouseleave", function () { hover = false; kick(); });
    area.addEventListener("focus", function () { hover = true; });
    area.addEventListener("blur", function () { hover = false; kick(); });
    if ("IntersectionObserver" in window) new IntersectionObserver(function (en) { visible = en[0].isIntersecting; kick(); }, { threshold: 0.25 }).observe(root);
    else visible = true;
    label(); draw();
  }
  /* Les éléments [data-at] apparaissent à leur instant. */
  function stamp(els, t) { els.forEach(function (el) { el.classList.toggle("is-on", t >= parseFloat(el.getAttribute("data-at"))); }); }
  var all = function (root, sel) { return Array.prototype.slice.call(root.querySelectorAll(sel)); };

  /* ── Maquette du terminal ────────────────────────────────────────── */
  var tui = document.getElementById("h-tui"), tuiData = document.getElementById("h-tui-data");
  if (tui && tuiData) {
    var X = JSON.parse(tuiData.textContent);
    var typed = document.getElementById("h-tui-typed"), title = document.getElementById("h-tui-title");
    var gauge = document.getElementById("h-tui-gauge"), tokens = document.getElementById("h-tui-tokens");
    var file = document.getElementById("h-tui-file"), send = document.getElementById("h-tui-send");
    var fileHTML = file.innerHTML, ats = all(tui, "[data-at]");
    var CALLS = [5.9, 6.9, 8.0, 9.1], EDIT = 6.9, TYPE0 = 3.2, TYPE_DT = 0.045, SEND = 5.0;
    var last = {};
    var set = function (el, key, val, html) { if (last[key] !== val) { last[key] = val; if (html) el.innerHTML = val; else el.textContent = val; } };
    tui.classList.add("is-live");
    function render(t) {
      var phase = t < SEND ? "home" : "thread";
      tui.setAttribute("data-phase", phase);
      tui.classList.toggle("is-in1", t >= 0.3);    /* les deux moitiés du bouclier se rejoignent */
      tui.classList.toggle("is-in2", t >= 1.3);    /* le mot TENON */
      tui.classList.toggle("is-in3", t >= 2.0);    /* le reste de l'accueil */
      tui.classList.toggle("is-out", t >= 13.6);   /* fondu avant de reboucler */
      var n = Math.max(0, Math.min(X.prompt.length, Math.floor((t - TYPE0) / TYPE_DT)));
      set(typed, "typed", phase === "home" ? X.prompt.slice(0, n) : "");
      set(title, "title", t >= SEND ? X.prompt : X.untitled);
      var c = 0; CALLS.forEach(function (s) { if (t >= s) c++; });
      set(gauge, "gauge", "█".repeat(X.cells[c]) + "░".repeat(10 - X.cells[c]) + " " + X.pct[c]);
      set(tokens, "tokens", X.tokens[c]);
      set(send, "send", c ? X.send.replace("{n}", c) : X.nosend);
      set(file, "file", t >= EDIT ? fileHTML : X.none, true);
      file.classList.toggle("is-none", t < EDIT);
      stamp(ats, t);
    }
    /* Thème : tenon, tokyonight, catppuccin, gruvbox, light (les couleurs de internal/ui/tui/theme.go). */
    var themeBtn = document.getElementById("h-tui-theme"), ti = 0;
    themeBtn.addEventListener("click", function () {
      ti = (ti + 1) % X.themes.length;
      tui.setAttribute("data-theme", X.themes[ti]);
      themeBtn.textContent = themeBtn.getAttribute("data-label") + X.themes[ti];
    });
    var th = /(?:^|[#&])theme=([a-z]+)/.exec(location.hash);   /* lien profond #theme=gruvbox (captures) */
    if (th && X.themes.indexOf(th[1]) > 0) { ti = X.themes.indexOf(th[1]); tui.setAttribute("data-theme", th[1]); themeBtn.textContent = themeBtn.getAttribute("data-label") + th[1]; }
    player(tui, tui.querySelector(".h-tui__screen"), 14.6, 12, render, "tui", document.getElementById("h-tui-toggle"), document.getElementById("h-tui-replay"));
  }

  /* ── Le job CI ───────────────────────────────────────────────────── */
  var pipe = document.getElementById("h-pipe");
  if (pipe) {
    var steps = all(pipe, ".h-pipe__step"), pats = all(pipe, "[data-at]");
    var words = { run: pipe.getAttribute("data-running"), ok: pipe.getAttribute("data-ok"), fail: pipe.getAttribute("data-fail"), wait: pipe.getAttribute("data-wait") };
    var chipText = {};
    pipe.classList.add("is-live");
    function renderPipe(t) {
      steps.forEach(function (st, i) {
        var a = parseFloat(st.getAttribute("data-from")), b = parseFloat(st.getAttribute("data-to")), res = st.getAttribute("data-result");
        var state = t < a ? "wait" : t < b ? "run" : res;
        st.setAttribute("data-state", state);
        if (chipText[i] !== state) { chipText[i] = state; st.querySelector(".h-pipe__chip").textContent = words[state]; }
      });
      stamp(pats, t);
    }
    player(pipe, pipe.querySelector(".h-pipe__stage"), 16, 15, renderPipe, "ci", document.getElementById("h-pipe-toggle"), document.getElementById("h-pipe-replay"));
  }
})();
