package ai

import (
	"context"
	"encoding/json"

	"code-review/internal/findings"
	"code-review/internal/redact"
)

// Provider is the vendor-neutral boundary for AI review integrations.
// AnalysisRequest fields are private and requests are created by Builder after
// local secret redaction, before a provider can observe code.
type Provider interface {
	Analyze(ctx context.Context, request AnalysisRequest) (*AnalysisResponse, error)
	Triage(ctx context.Context, request AnalysisRequest) (*TriageResponse, error)
}

type AnalysisRequest struct {
	instructions   string
	diff           string
	staticFindings []findings.Finding
	contextFiles   []ContextFile
	redactionCount int
}

func newAnalysisRequest(instructions, rawDiff string, staticFindings []findings.Finding, rawContext []ContextFile, priorRedactions int) AnalysisRequest {
	redacted := redact.Secrets(rawDiff)
	redactionCount := priorRedactions + redacted.Count
	contextFiles := make([]ContextFile, 0, len(rawContext))
	for _, file := range rawContext {
		redactedContent := redact.Secrets(file.Content)
		redactionCount += redactedContent.Count
		contextFiles = append(contextFiles, ContextFile{Path: file.Path, Content: redactedContent.Text})
	}
	return AnalysisRequest{
		instructions:   instructions,
		diff:           redacted.Text,
		staticFindings: findings.Clone(staticFindings),
		contextFiles:   contextFiles,
		redactionCount: redactionCount,
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

// ContextFiles returns an independent copy of the related staged context.
func (r AnalysisRequest) ContextFiles() []ContextFile {
	cloned := make([]ContextFile, len(r.contextFiles))
	copy(cloned, r.contextFiles)
	return cloned
}

func (r AnalysisRequest) RedactionCount() int {
	return r.redactionCount
}

func (r AnalysisRequest) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Instructions   string             `json:"instructions"`
		Diff           string             `json:"diff"`
		StaticFindings []findings.Finding `json:"staticFindings,omitempty"`
		RelatedContext []ContextFile      `json:"relatedContext,omitempty"`
	}{
		Instructions:   r.instructions,
		Diff:           r.diff,
		StaticFindings: r.staticFindings,
		RelatedContext: r.contextFiles,
	})
}
