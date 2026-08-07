package mock

import (
	"context"
	"fmt"

	"code-review/internal/ai"
)

type Provider struct {
	AnalyzeFunc func(context.Context, ai.AnalysisRequest) (*ai.AnalysisResponse, error)
}

var _ ai.Provider = Provider{}

func (p Provider) Analyze(ctx context.Context, request ai.AnalysisRequest) (*ai.AnalysisResponse, error) {
	if p.AnalyzeFunc == nil {
		return nil, fmt.Errorf("mock provider AnalyzeFunc is not configured")
	}
	return p.AnalyzeFunc(ctx, request)
}
