// Rendu des formules (pymdownx.arithmatex, mode generic) avec KaTeX auto-hébergé.
// arithmatex enveloppe chaque formule dans .arithmatex, avec \( \) ou \[ \] :
// on ne rend que ces éléments, jamais un « $ » isolé ailleurs dans la page.
// document$ : émis par Material à chaque chargement de page, y compris en navigation instantanée.
document$.subscribe(({ body }) => {
  if (typeof renderMathInElement !== "function") return;
  for (const el of body.querySelectorAll(".arithmatex")) {
    renderMathInElement(el, {
      delimiters: [
        { left: "\\(", right: "\\)", display: false },
        { left: "\\[", right: "\\]", display: true },
      ],
      throwOnError: false,
    });
  }
});
