package config

import (
	"fmt"
	"strings"
	"unicode"
)

type AIProvider string

const (
	AIProviderNone     AIProvider = "none"
	AIProviderOpenAI   AIProvider = "openai"
	AIProviderDeepSeek AIProvider = "deepseek"
)

const DefaultMaxOutputTokens = 4096

// AI contains safe provider settings only. API keys must be supplied through a
// runtime secret channel and never stored in this configuration value.
type AI struct {
	Provider        AIProvider
	Model           string
	MaxOutputTokens int
	Agents          []string
}

func DefaultAI() AI {
	return AI{
		Provider:        AIProviderNone,
		MaxOutputTokens: DefaultMaxOutputTokens,
		Agents:          []string{"correctness", "security"},
	}
}

func (c AI) Enabled() bool {
	return c.Provider != AIProviderNone
}

func (c AI) Validate() error {
	switch c.Provider {
	case AIProviderNone:
		if strings.TrimSpace(c.Model) != "" {
			return fmt.Errorf("AI model requires an enabled provider")
		}
		return nil
	case AIProviderOpenAI, AIProviderDeepSeek:
		if strings.TrimSpace(c.Model) == "" {
			return fmt.Errorf("AI model is required for provider %q", c.Provider)
		}
		if strings.IndexFunc(c.Model, func(character rune) bool {
			return unicode.IsControl(character) || unicode.Is(unicode.Cf, character)
		}) >= 0 {
			return fmt.Errorf("AI model contains unsupported control characters")
		}
	default:
		return fmt.Errorf("unsupported AI provider %q", c.Provider)
	}
	if c.MaxOutputTokens <= 0 {
		return fmt.Errorf("AI max output tokens must be positive")
	}
	if c.Enabled() {
		if len(c.Agents) == 0 {
			return fmt.Errorf("at least one AI review agent is required")
		}
		seen := make(map[string]struct{}, len(c.Agents))
		for _, agent := range c.Agents {
			agent = strings.TrimSpace(agent)
			if agent != "correctness" && agent != "security" {
				return fmt.Errorf("unsupported AI review agent %q", agent)
			}
			if _, exists := seen[agent]; exists {
				return fmt.Errorf("duplicate AI review agent %q", agent)
			}
			seen[agent] = struct{}{}
		}
	}
	return nil
}
