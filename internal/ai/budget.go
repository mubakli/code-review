package ai

import "fmt"

const estimatedBytesPerToken = 3

type Budget struct {
	MaxInputTokens         int
	MaxDiffTokens          int
	MaxStaticFindingTokens int
}

func DefaultBudget() Budget {
	return Budget{
		MaxInputTokens:         8000,
		MaxDiffTokens:          5000,
		MaxStaticFindingTokens: 1000,
	}
}

// EstimateTokens is a conservative provider-independent approximation. A
// provider-specific tokenizer can replace it later without changing batching.
func EstimateTokens(value string) int {
	if value == "" {
		return 0
	}
	return (len(value) + estimatedBytesPerToken - 1) / estimatedBytesPerToken
}

func (b Budget) diffLimit(instructionTokens int) (int, error) {
	if b.MaxInputTokens <= 0 {
		return 0, fmt.Errorf("max input tokens must be positive")
	}
	if b.MaxDiffTokens <= 0 {
		return 0, fmt.Errorf("max diff tokens must be positive")
	}
	if b.MaxStaticFindingTokens < 0 {
		return 0, fmt.Errorf("max static finding tokens cannot be negative")
	}

	available := b.MaxInputTokens - instructionTokens - b.MaxStaticFindingTokens
	if available <= 0 {
		return 0, fmt.Errorf("input budget is too small for instructions and static findings")
	}
	if available > b.MaxDiffTokens {
		available = b.MaxDiffTokens
	}
	if available < 32 {
		return 0, fmt.Errorf("effective diff budget must be at least 32 tokens")
	}
	return available, nil
}
