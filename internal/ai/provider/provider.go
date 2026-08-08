package provider

import (
	stdcontext "context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"unicode"

	"code-review/internal/ai/request"
	"code-review/internal/findings"
)

const (
	// defaultMaxOutputTokens bounds analysis responses unless an option says
	// otherwise.
	defaultMaxOutputTokens = 4096
	// triageMaxOutputTokens bounds the lightweight routing response.
	triageMaxOutputTokens = 512
	maxResponseBytes      = 4 << 20
)

// Provider is the vendor-specific execution boundary. Execution never
// prepares context or prompts: it receives a ready AnalysisRequest, calls the
// model, and normalizes the raw response into the common structured form.
type Provider interface {
	Analyze(ctx stdcontext.Context, request request.AnalysisRequest) (*AnalysisResponse, error)
	Triage(ctx stdcontext.Context, request request.AnalysisRequest) (*TriageResponse, error)
}

type ResponseStatus string

const ResponseStatusComplete ResponseStatus = "complete"

// AnalysisResponse is the normalized review response. Source and identity are
// never accepted from providers; orchestration assigns them after validation.
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

// TriageResponse is the normalized routing decision. Triage routes, it never
// diagnoses: surfaces describe what the deep security agent must examine,
// never confirmed vulnerabilities.
type TriageResponse struct {
	Status    ResponseStatus `json:"status"`
	Escalate  bool           `json:"escalate"`
	Surfaces  []string       `json:"surfaces,omitempty"`
	Rationale string         `json:"rationale,omitempty"`
}

func readBounded(reader io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(reader, maxResponseBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxResponseBytes {
		return nil, fmt.Errorf("response exceeds %d byte limit", maxResponseBytes)
	}
	return data, nil
}

// apiError sanitizes a provider API error so an API key can never leak into
// user-visible output.
func apiError(providerName string, status int, body []byte, apiKey string) error {
	var envelope struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	message := "request failed"
	if json.Unmarshal(body, &envelope) == nil && strings.TrimSpace(envelope.Error.Message) != "" {
		message = sanitizeMessage(envelope.Error.Message)
	}
	message = strings.ReplaceAll(message, apiKey, "[REDACTED_SECRET]")
	return fmt.Errorf("%s API returned HTTP %d: %s", providerName, status, message)
}

func sanitizeMessage(value string) string {
	value = strings.Map(func(character rune) rune {
		if unicode.IsControl(character) {
			return ' '
		}
		return character
	}, value)
	value = strings.TrimSpace(value)
	if len(value) > 500 {
		value = value[:500] + "..."
	}
	return value
}

func ensureJSONEnd(decoder *json.Decoder, providerName string) error {
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return fmt.Errorf("%s structured output contains trailing JSON", providerName)
		}
		return fmt.Errorf("decode trailing %s structured output: %w", providerName, err)
	}
	return nil
}
