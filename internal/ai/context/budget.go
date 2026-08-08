package context

import "fmt"

const estimatedBytesPerToken = 3

// Budget bounds one provider request: the full input (instructions plus diff
// plus selected static findings) and how diff content may trade against it.
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

func (b Budget) Validate() error {
	if b.MaxInputTokens <= 0 {
		return fmt.Errorf("max input tokens must be positive")
	}
	if b.MaxDiffTokens <= 0 {
		return fmt.Errorf("max diff tokens must be positive")
	}
	if b.MaxStaticFindingTokens < 0 {
		return fmt.Errorf("max static finding tokens cannot be negative")
	}
	return nil
}

// DiffLimit derives the per-batch diff token ceiling for a prompt cost.
func (b Budget) DiffLimit(instructionTokens int) (int, error) {
	if err := b.Validate(); err != nil {
		return 0, err
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
