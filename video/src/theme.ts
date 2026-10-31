// Design system of the tenon site (docs/assets/site/tokens.css): same colours, same fonts.
import * as THREE from "three";

export const C = {
  night: "oklch(17% 0.045 264)",
  night2: "oklch(22% 0.055 264)",
  rule: "oklch(34% 0.05 264)",
  on: "oklch(95% 0.01 264)",
  muted: "oklch(78% 0.025 264)",
  gold: "oklch(86% 0.17 92)",
  goldInk: "oklch(20% 0.04 264)",
  accent: "oklch(44% 0.19 264)",
  danger: "oklch(74% 0.15 35)",
  post: "oklch(36% 0.13 264)",
  rail: "oklch(93% 0.02 85)",
  edge: "oklch(72% 0.08 264)",
  // Logo colours, from docs/assets/site/mark.svg.
  markTop: "#1f4bb0",
  markBottom: "#0c1f52",
  markTrace: "#5fd8ff",
  markNode: "#7fe3ff",
} as const;

export const FONT = {
  display: "Bricolage Grotesque",
  body: "IBM Plex Sans",
  mono: "IBM Plex Mono",
};

const clamp = (x: number, a: number, b: number) => Math.max(a, Math.min(b, x));

// oklch → linear sRGB, for three.js materials (same conversion as the site).
export function color3(token: keyof typeof C): THREE.Color {
  const v = C[token];
  if (v.startsWith("#")) return new THREE.Color(v);
  const m = /oklch\(\s*([\d.]+)%\s+([\d.]+)\s+([\d.]+)/.exec(v)!;
  const L = parseFloat(m[1]) / 100, Ch = parseFloat(m[2]), h = (parseFloat(m[3]) * Math.PI) / 180;
  const a = Ch * Math.cos(h), b = Ch * Math.sin(h);
  const l = Math.pow(L + 0.3963377774 * a + 0.2158037573 * b, 3);
  const mm = Math.pow(L - 0.1055613458 * a - 0.0638541728 * b, 3);
  const s = Math.pow(L - 0.0894841775 * a - 1.291485548 * b, 3);
  return new THREE.Color().setRGB(
    clamp(4.0767416621 * l - 3.3077115913 * mm + 0.2309699292 * s, 0, 1),
    clamp(-1.2684380046 * l + 2.6097574011 * mm - 0.3413193965 * s, 0, 1),
    clamp(-0.0041960863 * l - 0.7034186147 * mm + 1.707614701 * s, 0, 1),
    THREE.LinearSRGBColorSpace,
  );
}

// sRGB hex string of a token (canvas 2D drawing).
export const hex = (token: keyof typeof C) => "#" + color3(token).getHexString(THREE.SRGBColorSpace);
