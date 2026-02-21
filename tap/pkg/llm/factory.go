package llm

import (
	"fmt"
	"os"

	"github.com/sashabaranov/go-openai"
)

// GetClient returns a client matching the Client interface.
// For now, only OpenAI is fully implemented, others will yield errors or fallbacks.
func GetClient(provider, apiKey string) (Client, error) {
	switch provider {
	case "openai", "":
		if apiKey == "" {
			apiKey = os.Getenv("OPENAI_API_KEY")
		}
		return openai.NewClient(apiKey), nil
	case "google":
		return NewGoogleAdapter(apiKey)
	case "anthropic":
		return NewAnthropicAdapter(apiKey)
	default:
		return nil, fmt.Errorf("unsupported provider: %s", provider)
	}
}
