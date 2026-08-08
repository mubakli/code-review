package request

import (
	"encoding/json"
	"fmt"
	"strings"

	"code-review/internal/ai/context"
	"code-review/internal/findings"
	"code-review/internal/redact"
)

const ReviewPrompt = `Review only issues introduced by these staged changes. Focus on security, correctness, performance, database usage, and maintainability. Report findings on changed lines with evidence from the supplied diff. Do not invent runtime behavior or missing context. Phrase database index and query recommendations as hypotheses that require measurement or EXPLAIN. Return structured findings only.`

// AnalysisRequest is the provider-neutral request object. Its fields are
// private and it is created by RequestBuilder from a prepared batch, so a
// provider can never observe anything that survived redaction or budgeting.
type AnalysisRequest struct {
	instructions   string
	diff           string
	staticFindings []findings.Finding
	contextFiles   []context.ContextFile
	redactionCount int
}

// RequestBuilder converts a prepared, budgeted context batch into an
// AnalysisRequest by attaching the agent prompt.
type RequestBuilder struct{}

// Build turns one prepared batch into a provider request. The final redaction
// pass is defense in depth: already-redacted content stays stable, and any
// text a future context extractor misses is removed here before a provider can
// observe it.
func (RequestBuilder) Build(batch context.PreparedBatch, prompt string) (AnalysisRequest, error) {
	if strings.TrimSpace(prompt) == "" {
		return AnalysisRequest{}, fmt.Errorf("agent prompt is required")
	}
	if batch.Diff == "" {
		return AnalysisRequest{}, fmt.Errorf("prepared batch is empty; nothing to request")
	}
	redacted := redact.Secrets(batch.Diff)
	redactionCount := batch.RedactionCount + redacted.Count
	contextFiles := make([]context.ContextFile, 0, len(batch.RelatedContext))
	for _, file := range batch.RelatedContext {
		redactedContent := redact.Secrets(file.Content)
		redactionCount += redactedContent.Count
		contextFiles = append(contextFiles, context.ContextFile{Path: file.Path, Content: redactedContent.Text})
	}
	return AnalysisRequest{
		instructions:   strings.TrimSpace(prompt),
		diff:           redacted.Text,
		staticFindings: findings.Clone(batch.StaticFindings),
		contextFiles:   contextFiles,
		redactionCount: redactionCount,
	}, nil
}

func (r AnalysisRequest) Instructions() string {
	return r.instructions
}

func (r AnalysisRequest) Diff() string {
	return r.diff
}

// StaticFindings returns an independent copy of the selected findings.
func (r AnalysisRequest) StaticFindings() []findings.Finding {
	return findings.Clone(r.staticFindings)
}

func (r AnalysisRequest) ContextFiles() []context.ContextFile {
	cloned := make([]context.ContextFile, len(r.contextFiles))
	copy(cloned, r.contextFiles)
	return cloned
}

func (r AnalysisRequest) RedactionCount() int {
	return r.redactionCount
}

func (r AnalysisRequest) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Instructions   string                `json:"instructions"`
		Diff           string                `json:"diff"`
		StaticFindings []findings.Finding    `json:"staticFindings,omitempty"`
		RelatedContext []context.ContextFile `json:"relatedContext,omitempty"`
	}{
		Instructions:   r.instructions,
		Diff:           r.diff,
		StaticFindings: r.staticFindings,
		RelatedContext: r.contextFiles,
	})
}
