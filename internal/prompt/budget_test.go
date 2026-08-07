package prompt

import "testing"

func TestEstimateTokens(t *testing.T) {
	t.Parallel()

	tests := []struct {
		value string
		want  int
	}{
		{value: "", want: 0},
		{value: "a", want: 1},
		{value: "abc", want: 1},
		{value: "abcd", want: 2},
	}
	for _, test := range tests {
		if got := EstimateTokens(test.value); got != test.want {
			t.Errorf("EstimateTokens(%q) = %d, want %d", test.value, got, test.want)
		}
	}
}

func TestBudgetValidation(t *testing.T) {
	t.Parallel()

	instructionTokens := EstimateTokens(ReviewInstructions)
	tests := []Budget{
		{MaxInputTokens: 0, MaxDiffTokens: 100, MaxStaticFindingTokens: 10},
		{MaxInputTokens: 500, MaxDiffTokens: 0, MaxStaticFindingTokens: 10},
		{MaxInputTokens: 500, MaxDiffTokens: 100, MaxStaticFindingTokens: -1},
		{MaxInputTokens: instructionTokens + 10, MaxDiffTokens: 100, MaxStaticFindingTokens: 20},
		{MaxInputTokens: instructionTokens + 40, MaxDiffTokens: 20, MaxStaticFindingTokens: 0},
	}
	for _, budget := range tests {
		if _, err := New(budget, pathMatcherForTest()); err == nil {
			t.Errorf("New(%+v) error = nil", budget)
		}
	}
}
