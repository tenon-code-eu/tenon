package config

// Valeurs vérifiées le 2026-10-15 dans la documentation officielle de chaque
// fournisseur : sources dans RELEASE.md, section « Sources vérifiées ».
// Les fenêtres publiées en « k » sont lues comme des milliers de jetons
// (256k → 256000) : arrondi prudent, jamais au-dessus de la valeur publiée.
func presets() []Provider {
	return []Provider{
		{ID: "mistral", Name: "Mistral AI", BaseURL: "https://api.mistral.ai/v1", APIKeyEnv: "MISTRAL_API_KEY",
			Model:   "mistral-medium-3-5",
			Country: "FR", Jurisdiction: "eu",
			ContextWindow: 256000},
		{ID: "scaleway", Name: "Scaleway", BaseURL: "https://api.scaleway.ai/v1", APIKeyEnv: "SCW_SECRET_KEY",
			Model:   "mistral-medium-3.5-128b",
			Country: "FR", Jurisdiction: "eu",
			ContextWindow: 180000}, // limite Serverless pendant la préversion
		{ID: "ovhcloud", Name: "OVHcloud AI Endpoints", BaseURL: "https://oai.endpoints.kepler.ai.cloud.ovh.net/v1", APIKeyEnv: "OVH_AI_ENDPOINTS_ACCESS_TOKEN",
			Model:   "Qwen3.8-27B",
			Country: "FR", Jurisdiction: "eu",
			ContextWindow: 262000},
		{ID: "ollama", Name: "Ollama", BaseURL: "http://localhost:11434/v1",
			Model:        "devstral",
			Jurisdiction: "local",
			// TODO(release): fenêtre fixée par le serveur (OLLAMA_CONTEXT_LENGTH,
			// 4k/32k/256k selon la VRAM par défaut), pas par le modèle (128K).
			ContextWindow: 32000},
		{ID: "vllm", Name: "vLLM", BaseURL: "http://localhost:8000/v1",
			// TODO(release): nom fixé par le serveur (--served-model-name, sinon
			// --model) ; aucun nom par défaut documenté.
			Model:        "default",
			Jurisdiction: "local",
			// TODO(release): fenêtre fixée par le serveur (--max-model-len, sinon
			// la configuration du modèle).
			ContextWindow: 32000},
	}
}
