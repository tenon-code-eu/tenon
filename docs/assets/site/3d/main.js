/* tenon · scènes 3D de la page d'accueil (three.js vendoré, aucun appel externe).
 * Chaque scène se charge à l'approche de son conteneur : three.js n'est téléchargé que si une
 * scène va s'afficher, et jamais sans WebGL. Le contenu HTML reste la version de référence :
 * une scène qui échoue laisse sa figure telle quelle (classe .is-3d jamais posée). */
const here = new URL(import.meta.url);
const v = here.searchParams.get("v") || "";
const at = (p) => new URL(p + (v ? "?v=" + v : ""), here).href;

function webgl() {
  try {
    const c = document.createElement("canvas");
    return !!(window.WebGLRenderingContext && (c.getContext("webgl2") || c.getContext("webgl")));
  } catch (e) { return false; }
}

/* id du conteneur → module de la scène. */
const SCENES = [
  ["h-hero-3d", "hero.js"],
  ["h-stage", "harness.js"],
];

if (!webgl()) {
  document.documentElement.classList.add("no-webgl");
} else {
  let lib = null, core = null;
  const three = () => lib || (lib = import(at("../vendor/three/three.module.min.js")));
  const base = () => core || (core = import(at("core.js")));
  const start = (el, file) =>
    Promise.all([three(), base(), import(at(file))])
      .then(([THREE, C, m]) => m.mount(el, THREE, C))
      .catch((e) => {
        el.classList.remove("is-3d"); el.classList.add("is-3d-failed");
        if (el.id === "h-stage") el.classList.add("is-fallback");
        console.warn("tenon 3D:", e);
      });

  const io = "IntersectionObserver" in window
    ? new IntersectionObserver((entries) => {
        entries.forEach((en) => {
          if (!en.isIntersecting) return;
          io.unobserve(en.target);
          start(en.target, en.target.getAttribute("data-scene"));
        });
      }, { rootMargin: "400px 0px" })
    : null;

  /* Le héros attend la fin du chargement et un moment calme : le titre HTML reste le premier rendu. */
  const idle = (fn) => ("requestIdleCallback" in window ? requestIdleCallback(fn, { timeout: 2500 }) : setTimeout(fn, 600));
  const whenLoaded = (fn) => (document.readyState === "complete" ? idle(fn) : addEventListener("load", () => idle(fn), { once: true }));

  SCENES.forEach(([id, file]) => {
    const el = document.getElementById(id);
    if (!el) return;
    el.setAttribute("data-scene", file);
    if (id === "h-hero-3d") whenLoaded(() => start(el, file));
    else if (io) io.observe(el);
    else start(el, file);
  });
}
