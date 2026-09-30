package agent

import (
	"fmt"
	"os"
	"strings"

	"charm.land/fantasy"
	"charm.land/fantasy/providers/anthropic"
	"charm.land/fantasy/providers/google"
	"charm.land/fantasy/providers/openai"
	"charm.land/fantasy/providers/openaicompat"

	"github.com/Haslab-dev/RakitKode/internal/config"
)

// buildProvider constructs the fantasy provider for one configured
// provider, resolving the API key through the store.
func buildProvider(cfg *config.Store, providerID string) (fantasy.Provider, error) {
	provCfg, ok := cfg.Config().Providers[providerID]
	if !ok {
		return nil, fmt.Errorf("provider %q is not configured", providerID)
	}
	apiKey := provCfg.APIKey
	if provCfg.APIKeyEnv != "" {
		if fromEnv := os.Getenv(provCfg.APIKeyEnv); fromEnv != "" {
			apiKey = fromEnv
		}
	}
	if resolved, err := cfg.Resolve(apiKey); err == nil && resolved != "" {
		apiKey = resolved
	}
	baseURL := provCfg.BaseURL
	if resolved, err := cfg.Resolve(baseURL); err == nil && resolved != "" {
		baseURL = resolved
	}

	provType := provCfg.Type
	if provType == "" {
		provType = config.ProviderTypeOpenAICompat
		if strings.Contains(apiKey, "sk-ant") || strings.Contains(baseURL, "anthropic") {
			provType = config.ProviderTypeAnthropic
		}
	}

	switch provType {
	case config.ProviderTypeAnthropic:
		opts := []anthropic.Option{}
		if apiKey != "" {
			// Bearer-style tokens go through headers; keys via x-api-key.
			if strings.HasPrefix(apiKey, "Bearer ") {
				opts = append(opts, anthropic.WithHeaders(map[string]string{"Authorization": apiKey}))
			} else {
				opts = append(opts, anthropic.WithAPIKey(apiKey))
			}
		}
		if baseURL != "" {
			opts = append(opts, anthropic.WithBaseURL(baseURL))
		}
		return anthropic.New(opts...)
	case config.ProviderTypeOpenAI:
		opts := []openai.Option{openai.WithUseResponsesAPI()}
		if apiKey != "" {
			opts = append(opts, openai.WithAPIKey(apiKey))
		}
		if baseURL != "" {
			opts = append(opts, openai.WithBaseURL(baseURL))
		}
		return openai.New(opts...)
	case config.ProviderTypeGoogle:
		opts := []google.Option{}
		if apiKey != "" {
			opts = append(opts, google.WithGeminiAPIKey(apiKey))
		}
		if baseURL != "" {
			opts = append(opts, google.WithBaseURL(baseURL))
		}
		return google.New(opts...)
	case config.ProviderTypeOpenAICompat:
		fallthrough
	default:
		opts := []openaicompat.Option{}
		if apiKey != "" {
			opts = append(opts, openaicompat.WithAPIKey(apiKey))
		}
		if baseURL != "" {
			opts = append(opts, openaicompat.WithBaseURL(baseURL))
		}
		return openaicompat.New(opts...)
	}
}
