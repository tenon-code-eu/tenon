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
		// Locaux : le modèle servi et la fenêtre appliquée sont fixés par le
		// serveur (Ollama : OLLAMA_CONTEXT_LENGTH ; vLLM : --served-model-name,
		// --max-model-len). Ils sont lus au démarrage (Detect), sauf si la
		// configuration les fixe.
		{ID: "ollama", Name: "Ollama", BaseURL: "http://localhost:11434/v1",
			Jurisdiction: "local", Detect: "ollama"},
		{ID: "vllm", Name: "vLLM", BaseURL: "http://localhost:8000/v1",
			Jurisdiction: "local", Detect: "vllm"},
	}
}
