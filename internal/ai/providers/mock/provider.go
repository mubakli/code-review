package mock

import (
	"context"
	"fmt"

	"code-review/internal/ai"
)

type Provider struct {
	AnalyzeFunc func(context.Context, ai.AnalysisRequest) (*ai.AnalysisResponse, error)
	TriageFunc  func(context.Context, ai.AnalysisRequest) (*ai.TriageResponse, error)
}

var _ ai.Provider = Provider{}

func (p Provider) Analyze(ctx context.Context, request ai.AnalysisRequest) (*ai.AnalysisResponse, error) {
	if p.AnalyzeFunc == nil {
		return nil, fmt.Errorf("mock provider AnalyzeFunc is not configured")
	}
	return p.AnalyzeFunc(ctx, request)
}

func (p Provider) Triage(ctx context.Context, request ai.AnalysisRequest) (*ai.TriageResponse, error) {
	if p.TriageFunc == nil {
		return &ai.TriageResponse{Status: ai.ResponseStatusComplete, Escalate: false}, nil
	}
	return p.TriageFunc(ctx, request)
}
