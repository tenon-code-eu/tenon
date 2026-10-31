/* tenon · page d'accueil : copie, onglets, film de lancement et palette ⌘K.
 * Les scènes 3D (three.js vendoré) sont dans 3d/ et se chargent à part (3d/main.js). */
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
})();
