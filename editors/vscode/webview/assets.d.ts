// Files imported as text (esbuild.mjs and vitest bundle them with `?raw`).
declare module "*.svg?raw" {
  const text: string;
  export default text;
}
