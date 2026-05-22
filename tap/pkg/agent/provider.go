package agent

import (
	"fmt"
	"os"
	"strings"

	"github.com/openai/openai-go/v3/option"
)

// APIParadigm categorises the API surface a provider exposes.
type APIParadigm string

const (
	ParadigmResponses APIParadigm = "responses" // OpenAI Responses API
	ParadigmChat      APIParadigm = "chat"      // OpenAI-compatible Chat Completions
	ParadigmAnthropic APIParadigm = "anthropic" // Anthropic Messages API (stub)
	ParadigmGoogle    APIParadigm = "google"    // Google Gemini native API (stub)
)

// ProviderConfig holds the resolved settings for a model.
type ProviderConfig struct {
	Name     string
	BaseURL  string
	APIKey   string
	Paradigm APIParadigm
}

// ClientOptions builds the option.RequestOption slice for openai.NewClient.
func (pc ProviderConfig) ClientOptions() []option.RequestOption {
	var opts []option.RequestOption
	if pc.APIKey != "" {
		opts = append(opts, option.WithAPIKey(pc.APIKey))
	}
	if pc.BaseURL != "" {
		opts = append(opts, option.WithBaseURL(pc.BaseURL))
	}
	return opts
}

type knownProvider struct {
	prefix   string
	name     string
	baseURL  string
	paradigm APIParadigm
}

var knownProviders = []knownProvider{
	{"gpt-", "openai", "https://api.openai.com/v1", ParadigmResponses},
	{"deepseek-", "deepseek", "https://api.deepseek.com/v1", ParadigmChat},
	{"claude-", "anthropic", "https://api.anthropic.com/v1", ParadigmAnthropic},
	{"gemini-", "google", "https://generativelanguage.googleapis.com/v1beta", ParadigmGoogle},
}

// ResolveModel returns the ProviderConfig for the given model name.
// Known model prefixes (gpt-, deepseek-, claude-, gemini-) map to hardcoded providers.
// Unknown models default to chat paradigm with {PREFIX}_API_KEY / {PREFIX}_BASE_URL env vars.
func ResolveModel(model string) (ProviderConfig, error) {
	m := strings.TrimSpace(model)
	if m == "" {
		return ProviderConfig{}, fmt.Errorf("model name is empty")
	}

	for _, kp := range knownProviders {
		if strings.HasPrefix(m, kp.prefix) {
			return ProviderConfig{
				Name:     kp.name,
				BaseURL:  kp.baseURL,
				APIKey:   os.Getenv(strings.ToUpper(kp.name) + "_API_KEY"),
				Paradigm: kp.paradigm,
			}, nil
		}
	}

	// Unknown model — derive provider name from the first segment before any "-" or "/".
	// Fall back to chat paradigm with explicit env vars.
	name := modelPrefix(m)
	baseURL := os.Getenv(strings.ToUpper(name) + "_BASE_URL")
	if baseURL == "" {
		// Last resort: check for a generic OPENAI-compatible base URL.
		baseURL = os.Getenv("LLM_BASE_URL")
	}
	if baseURL == "" {
		return ProviderConfig{}, fmt.Errorf(
			"model %q: unknown provider — set %s_BASE_URL or LLM_BASE_URL",
			m, strings.ToUpper(name),
		)
	}

	return ProviderConfig{
		Name:     name,
		BaseURL:  baseURL,
		APIKey:   os.Getenv(strings.ToUpper(name) + "_API_KEY"),
		Paradigm: ParadigmChat,
	}, nil
}

func modelPrefix(model string) string {
	// Take the first segment: "llama-3-70b" → "llama", "mixtral-8x7b" → "mixtral"
	if idx := strings.IndexAny(model, "-/"); idx > 0 {
		return model[:idx]
	}
	return model
}
