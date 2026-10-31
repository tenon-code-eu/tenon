// The site's self-hosted fonts (copied from docs/assets/site/fonts, OFL licences alongside).
import { loadFont } from "@remotion/fonts";
import { staticFile } from "remotion";
import { FONT } from "./theme";

const f = (family: string, file: string, weight: string) =>
  loadFont({ family, url: staticFile(`fonts/${file}`), weight, display: "block" });

export const fontsReady: Promise<unknown> = Promise.all([
  f(FONT.display, "bricolage-grotesque-latin-500-normal.woff2", "500"),
  f(FONT.display, "bricolage-grotesque-latin-700-normal.woff2", "700"),
  f(FONT.body, "ibm-plex-sans-latin-400-normal.woff2", "400"),
  f(FONT.body, "ibm-plex-sans-latin-600-normal.woff2", "600"),
  f(FONT.mono, "ibm-plex-mono-latin-400-normal.woff2", "400"),
  f(FONT.mono, "ibm-plex-mono-latin-500-normal.woff2", "500"),
]);
