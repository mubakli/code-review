package ai

import (
	"context"
	"encoding/json"

	"code-review/internal/findings"
	"code-review/internal/redact"
)

// Provider is the vendor-neutral boundary for future AI review integrations.
// AnalysisRequest fields are private and requests are created by Builder after
// local secret redaction, before a provider can observe code.
type Provider interface {
	Analyze(ctx context.Context, request AnalysisRequest) (*AnalysisResponse, error)
}

type AnalysisRequest struct {
	instructions   string
	diff           string
	staticFindings []findings.Finding
	redactionCount int
}

func newAnalysisRequest(instructions, rawDiff string, staticFindings []findings.Finding, priorRedactions int) AnalysisRequest {
	redacted := redact.Secrets(rawDiff)
	return AnalysisRequest{
		instructions:   instructions,
		diff:           redacted.Text,
		staticFindings: findings.Clone(staticFindings),
		redactionCount: priorRedactions + redacted.Count,
	}
}

func (r AnalysisRequest) Instructions() string {
	return r.instructions
}

func (r AnalysisRequest) Diff() string {
	return r.diff
}

func (r AnalysisRequest) StaticFindings() []findings.Finding {
	return findings.Clone(r.staticFindings)
}

func (r AnalysisRequest) RedactionCount() int {
	return r.redactionCount
}

func (r AnalysisRequest) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Instructions   string             `json:"instructions"`
		Diff           string             `json:"diff"`
		StaticFindings []findings.Finding `json:"staticFindings,omitempty"`
	}{
		Instructions:   r.instructions,
		Diff:           r.diff,
		StaticFindings: r.staticFindings,
	})
}
