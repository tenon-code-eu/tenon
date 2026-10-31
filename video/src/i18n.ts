// Every on-screen text, EN and FR. Event types and commands stay as tenon prints them.
export type Lang = "en" | "fr";

export const EVENTS = [
  "SessionStarted",
  "UserMessage",
  "ProviderSelected",
  "ToolCallRequested",
  "PermissionRequested",
  "PermissionResolved",
  "ToolResult",
  "TurnCompleted",
] as const;

const en = {
  a_sub: "The open-source coding agent built for Europe.",
  b_kicker: "Under your control",
  b_title: "Nothing changes until you say yes.",
  b_text: "Writes, commands and MCP calls wait for your approval. Commands run in a system sandbox.",
  b_prompt: "fix the failing test in billing/",
  // internal/i18n/catalog.go: tui.perm.title, tui.perm.keys_tool, tui.perm.sandboxed_write
  b_perm: "Allow this action?",
  b_perm_d: "edit billing/invoice.go · restore point created, /rewind to undo",
  b_keys: ["y: yes once", "a: always for this session", "n: no"],
  b_sandboxed: "sandboxed",
  b_badge: "Mistral AI · FR · EU",
  c_kicker: "Sovereign by default",
  c_title: "Your data stays in Europe.",
  c_project: ["Your project", "code, files, commands"],
  c_mask: ["Masking", "secrets, personal data"],
  c_perm: ["Your approval", "you decide"],
  c_log: ["Audit log", "hash-chained"],
  c_local: ["Local model", "Ollama · vLLM"],
  c_eu: ["EU providers", "Mistral · Scaleway · OVHcloud · Albert"],
  c_out: ["Outside the EU", "only with your explicit consent"],
  d_kicker: "Proof, not promises",
  d_title: "Every step, on the record.",
  d_text: "Hash-chained audit log · RFC 3161 timestamps · a report for your DPO.",
  d_ok: "chain intact",
  e_tag: "Your code. Your rules. Your jurisdiction.",
  e_foot: ["github.com/tenon-code-eu/tenon", "open source · EUPL-1.2", "made in Europe"],
};

const fr: typeof en = {
  a_sub: "L'agent de code open source conçu pour l'Europe.",
  b_kicker: "Sous votre contrôle",
  b_title: "Rien ne change sans votre accord.",
  b_text: "Écritures, commandes et appels MCP attendent votre accord. Les commandes tournent dans une isolation système.",
  b_prompt: "corrige le test qui échoue dans billing/",
  b_perm: "Autoriser cette action ?",
  b_perm_d: "edit billing/invoice.go · point de reprise créé, /rewind pour annuler",
  b_keys: ["o : oui une fois", "s : oui pour la session", "n : non"],
  b_sandboxed: "isolé",
  b_badge: "Mistral AI · FR · UE",
  c_kicker: "Souverain par défaut",
  c_title: "Vos données restent en Europe.",
  c_project: ["Votre projet", "code, fichiers, commandes"],
  c_mask: ["Masquage", "secrets, données personnelles"],
  c_perm: ["Votre accord", "vous décidez"],
  c_log: ["Journal d'audit", "chaîné par hachage"],
  c_local: ["Modèle local", "Ollama · vLLM"],
  c_eu: ["Fournisseurs UE", "Mistral · Scaleway · OVHcloud · Albert"],
  c_out: ["Hors UE", "seulement avec votre consentement explicite"],
  d_kicker: "Des preuves, pas des promesses",
  d_title: "Chaque étape, au journal.",
  d_text: "Journal chaîné par hachage · horodatage RFC 3161 · un rapport pour votre DPO.",
  d_ok: "chaîne intacte",
  e_tag: "Votre code. Vos règles. Votre juridiction.",
  e_foot: ["github.com/tenon-code-eu/tenon", "open source · EUPL-1.2", "conçu en Europe"],
};

export const T = (lang: Lang) => (lang === "fr" ? fr : en);
