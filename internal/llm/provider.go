package llm

import (
	"context"

	"code-review/internal/findings"
)

// Provider is the vendor-neutral boundary for future AI review integrations.
// Requests must be assembled from redacted, budgeted context before this
// method is called.
type Provider interface {
	Analyze(ctx context.Context, request AnalysisRequest) (*AnalysisResponse, error)
}

type AnalysisRequest struct {
	Diff           string             `json:"diff"`
	StaticFindings []findings.Finding `json:"staticFindings,omitempty"`
}

type AnalysisResponse struct {
	Findings []findings.Finding `json:"findings"`
}
