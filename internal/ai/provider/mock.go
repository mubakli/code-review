package provider

import (
	stdcontext "context"
	"fmt"

	"code-review/internal/ai/request"
)

// Mock is the test double for Provider. Unconfigured function fields default
// to a benign triage-clear response; AnalyzeFunc is required.
type Mock struct {
	AnalyzeFunc func(stdcontext.Context, request.AnalysisRequest) (*AnalysisResponse, error)
	TriageFunc  func(stdcontext.Context, request.AnalysisRequest) (*TriageResponse, error)
}

var _ Provider = Mock{}

func (p Mock) Analyze(ctx stdcontext.Context, request request.AnalysisRequest) (*AnalysisResponse, error) {
	if p.AnalyzeFunc == nil {
		return nil, fmt.Errorf("mock provider AnalyzeFunc is not configured")
	}
	return p.AnalyzeFunc(ctx, request)
}

func (p Mock) Triage(ctx stdcontext.Context, request request.AnalysisRequest) (*TriageResponse, error) {
	if p.TriageFunc == nil {
		return &TriageResponse{Status: ResponseStatusComplete, Escalate: false}, nil
	}
	return p.TriageFunc(ctx, request)
}
