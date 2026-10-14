package config

func presets() []Provider {
	return []Provider{
		{ID: "mistral", Name: "Mistral AI", BaseURL: "https://api.mistral.ai/v1", APIKeyEnv: "MISTRAL_API_KEY",
			Model:   "devstral-latest", // TODO(release): vérifier l'ID du modèle
			Country: "FR", Jurisdiction: "eu",
			ContextWindow: 128000}, // TODO(release): vérifier context_window
		{ID: "scaleway", Name: "Scaleway", BaseURL: "https://api.scaleway.ai/v1", APIKeyEnv: "SCW_SECRET_KEY",
			Model:   "devstral-small-2505", // TODO(release): vérifier l'ID du modèle
			Country: "FR", Jurisdiction: "eu",
			ContextWindow: 128000}, // TODO(release): vérifier context_window
		{ID: "ovhcloud", Name: "OVHcloud AI Endpoints", BaseURL: "https://oai.endpoints.kepler.ai.cloud.ovh.net/v1", APIKeyEnv: "OVH_AI_ENDPOINTS_ACCESS_TOKEN",
			Model:   "Mistral-Small-3.2-24B-Instruct-2506", // TODO(release): vérifier l'ID du modèle
			Country: "FR", Jurisdiction: "eu",
			ContextWindow: 128000}, // TODO(release): vérifier context_window
		{ID: "ollama", Name: "Ollama", BaseURL: "http://localhost:11434/v1",
			Model:         "devstral", // TODO(release): vérifier l'ID du modèle
			Jurisdiction:  "local",
			ContextWindow: 32000}, // TODO(release): vérifier context_window
		{ID: "vllm", Name: "vLLM", BaseURL: "http://localhost:8000/v1",
			Model:         "default", // TODO(release): vérifier l'ID du modèle
			Jurisdiction:  "local",
			ContextWindow: 32000}, // TODO(release): vérifier context_window
	}
}
