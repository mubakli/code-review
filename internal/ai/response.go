package ai

import "code-review/internal/findings"

type ResponseStatus string

const ResponseStatusComplete ResponseStatus = "complete"

// AnalysisResponse is the provider-neutral structured response. Source is not
// accepted from providers; orchestration assigns SourceAI after validation.
type AnalysisResponse struct {
	Status   ResponseStatus    `json:"status"`
	Findings []ResponseFinding `json:"findings"`
}

type ResponseFinding struct {
	File        string                `json:"file"`
	StartLine   int                   `json:"startLine"`
	EndLine     int                   `json:"endLine"`
	Severity    findings.Severity     `json:"severity"`
	Category    findings.Category     `json:"category"`
	Title       string                `json:"title"`
	Message     string                `json:"message"`
	Suggestion  string                `json:"suggestion"`
	ProposedFix *findings.ProposedFix `json:"proposedFix"`
	Confidence  float64               `json:"confidence"`
}

// TriageResponse is the lightweight security-triage decision. Triage never
// produces findings; it only decides whether deep security review is needed.
type TriageResponse struct {
	Status    ResponseStatus `json:"status"`
	Escalate  bool           `json:"escalate"`
	Surfaces  []string       `json:"surfaces,omitempty"`
	Rationale string         `json:"rationale,omitempty"`
}
