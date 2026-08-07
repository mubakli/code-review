package config

import "testing"

func TestAIValidate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		config  AI
		wantErr bool
	}{
		{name: "disabled", config: DefaultAI()},
		{name: "openai", config: AI{Provider: AIProviderOpenAI, Model: "review-model", MaxOutputTokens: 1000, Agents: []string{"correctness"}}},
		{name: "deepseek", config: AI{Provider: AIProviderDeepSeek, Model: "deepseek-chat", MaxOutputTokens: 1000, Agents: []string{"security"}}},
		{name: "model without provider", config: AI{Provider: AIProviderNone, Model: "review-model", MaxOutputTokens: 1000}, wantErr: true},
		{name: "missing model", config: AI{Provider: AIProviderOpenAI, MaxOutputTokens: 1000}, wantErr: true},
		{name: "unsupported provider", config: AI{Provider: "unknown", Model: "review-model", MaxOutputTokens: 1000}, wantErr: true},
		{name: "invalid output budget", config: AI{Provider: AIProviderOpenAI, Model: "review-model"}, wantErr: true},
		{name: "unsafe model", config: AI{Provider: AIProviderOpenAI, Model: "review\u202Emodel", MaxOutputTokens: 1000}, wantErr: true},
		{name: "missing agents", config: AI{Provider: AIProviderOpenAI, Model: "review-model", MaxOutputTokens: 1000}, wantErr: true},
		{name: "unsupported agent", config: AI{Provider: AIProviderOpenAI, Model: "review-model", MaxOutputTokens: 1000, Agents: []string{"performance"}}, wantErr: true},
		{name: "duplicate agent", config: AI{Provider: AIProviderOpenAI, Model: "review-model", MaxOutputTokens: 1000, Agents: []string{"security", "security"}}, wantErr: true},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			err := test.config.Validate()
			if (err != nil) != test.wantErr {
				t.Fatalf("Validate() error = %v, wantErr %v", err, test.wantErr)
			}
		})
	}
}
