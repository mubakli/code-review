package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"

	"code-review/internal/ai"
	"code-review/internal/findings"
)

const (
	defaultEndpoint        = "https://api.openai.com/v1/responses"
	defaultMaxOutputTokens = 4096
	triageMaxOutputTokens  = 512
	maxResponseBytes       = 4 << 20
)

type Options struct {
	APIKey          string
	Model           string
	Endpoint        string
	MaxOutputTokens int
	HTTPClient      *http.Client
}

type Provider struct {
	apiKey          string
	model           string
	endpoint        string
	maxOutputTokens int
	client          *http.Client
}

var _ ai.Provider = (*Provider)(nil)

func New(options Options) (*Provider, error) {
	apiKey := strings.TrimSpace(options.APIKey)
	if apiKey == "" {
		return nil, fmt.Errorf("OpenAI API key is required")
	}
	model := strings.TrimSpace(options.Model)
	if model == "" {
		return nil, fmt.Errorf("OpenAI model is required")
	}
	endpoint := strings.TrimSpace(options.Endpoint)
	if endpoint == "" {
		endpoint = defaultEndpoint
	}
	if err := validateEndpoint(endpoint); err != nil {
		return nil, err
	}
	maxOutputTokens := options.MaxOutputTokens
	if maxOutputTokens == 0 {
		maxOutputTokens = defaultMaxOutputTokens
	}
	if maxOutputTokens < 1 {
		return nil, fmt.Errorf("OpenAI max output tokens must be positive")
	}
	client := options.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 60 * time.Second}
	}
	return &Provider{
		apiKey:          apiKey,
		model:           model,
		endpoint:        endpoint,
		maxOutputTokens: maxOutputTokens,
		client:          client,
	}, nil
}

func (p *Provider) Analyze(ctx context.Context, request ai.AnalysisRequest) (*ai.AnalysisResponse, error) {
	return p.analyze(ctx, request)
}

func (p *Provider) analyze(ctx context.Context, request ai.AnalysisRequest) (*ai.AnalysisResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	input, err := json.Marshal(struct {
		Diff           string             `json:"diff"`
		StaticFindings []findings.Finding `json:"staticFindings,omitempty"`
		RelatedContext []ai.ContextFile   `json:"relatedContext,omitempty"`
	}{
		Diff:           request.Diff(),
		StaticFindings: request.StaticFindings(),
		RelatedContext: request.ContextFiles(),
	})
	if err != nil {
		return nil, fmt.Errorf("encode OpenAI review input: %w", err)
	}
	payload, err := json.Marshal(responsesRequest{
		Model:           p.model,
		Instructions:    request.Instructions(),
		Input:           string(input),
		Store:           false,
		MaxOutputTokens: p.maxOutputTokens,
		Text: textConfig{Format: formatConfig{
			Type:   "json_schema",
			Name:   "code_review_findings",
			Strict: true,
			Schema: responseSchema(),
		}},
	})
	if err != nil {
		return nil, fmt.Errorf("encode OpenAI request: %w", err)
	}
	outputText, err := p.post(ctx, payload)
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(strings.NewReader(outputText))
	decoder.DisallowUnknownFields()
	var analysis ai.AnalysisResponse
	if err := decoder.Decode(&analysis); err != nil {
		return nil, fmt.Errorf("decode OpenAI structured review: %w", err)
	}
	if err := ensureJSONEnd(decoder); err != nil {
		return nil, err
	}
	return &analysis, nil
}

func (p *Provider) Triage(ctx context.Context, request ai.AnalysisRequest) (*ai.TriageResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	input, err := json.Marshal(struct {
		Diff           string             `json:"diff"`
		StaticFindings []findings.Finding `json:"staticFindings,omitempty"`
		RelatedContext []ai.ContextFile   `json:"relatedContext,omitempty"`
	}{
		Diff:           request.Diff(),
		StaticFindings: request.StaticFindings(),
		RelatedContext: request.ContextFiles(),
	})
	if err != nil {
		return nil, fmt.Errorf("encode OpenAI triage input: %w", err)
	}
	payload, err := json.Marshal(responsesRequest{
		Model:           p.model,
		Instructions:    request.Instructions(),
		Input:           string(input),
		Store:           false,
		MaxOutputTokens: triageMaxOutputTokens,
		Text: textConfig{Format: formatConfig{
			Type:   "json_schema",
			Name:   "code_review_triage",
			Strict: true,
			Schema: triageSchema(),
		}},
	})
	if err != nil {
		return nil, fmt.Errorf("encode OpenAI triage request: %w", err)
	}
	outputText, err := p.post(ctx, payload)
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(strings.NewReader(outputText))
	decoder.DisallowUnknownFields()
	var triage ai.TriageResponse
	if err := decoder.Decode(&triage); err != nil {
		return nil, fmt.Errorf("decode OpenAI structured triage: %w", err)
	}
	if err := ensureJSONEnd(decoder); err != nil {
		return nil, err
	}
	return &triage, nil
}

func (p *Provider) post(ctx context.Context, payload []byte) (string, error) {
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint, bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("create OpenAI request: %w", err)
	}
	httpRequest.Header.Set("Authorization", "Bearer "+p.apiKey)
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("User-Agent", "local-code-reviewer/0")

	response, err := p.client.Do(httpRequest)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return "", ctxErr
		}
		return "", fmt.Errorf("call OpenAI Responses API: %w", err)
	}
	defer response.Body.Close()
	body, err := readBounded(response.Body)
	if err != nil {
		return "", fmt.Errorf("read OpenAI response: %w", err)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return "", apiError(response.StatusCode, body, p.apiKey)
	}

	var envelope responsesEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		return "", fmt.Errorf("decode OpenAI response envelope: %w", err)
	}
	outputText := envelope.OutputText
	if outputText == "" {
		for _, output := range envelope.Output {
			for _, content := range output.Content {
				if content.Type == "output_text" && content.Text != "" {
					outputText = content.Text
					break
				}
			}
			if outputText != "" {
				break
			}
		}
	}
	if strings.TrimSpace(outputText) == "" {
		return "", fmt.Errorf("OpenAI response contains no output text")
	}
	return outputText, nil
}

type responsesRequest struct {
	Model           string     `json:"model"`
	Instructions    string     `json:"instructions"`
	Input           string     `json:"input"`
	Store           bool       `json:"store"`
	MaxOutputTokens int        `json:"max_output_tokens"`
	Text            textConfig `json:"text"`
}

type textConfig struct {
	Format formatConfig `json:"format"`
}

type formatConfig struct {
	Type   string         `json:"type"`
	Name   string         `json:"name"`
	Strict bool           `json:"strict"`
	Schema map[string]any `json:"schema"`
}

type responsesEnvelope struct {
	OutputText string `json:"output_text"`
	Output     []struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"output"`
}

func responseSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"status", "findings"},
		"properties": map[string]any{
			"status": map[string]any{
				"type": "string",
				"enum": []string{string(ai.ResponseStatusComplete)},
			},
			"findings": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"required": []string{
						"file", "startLine", "endLine", "severity", "category",
						"title", "message", "suggestion", "proposedFix", "confidence",
					},
					"properties": map[string]any{
						"file":       map[string]any{"type": "string"},
						"startLine":  map[string]any{"type": "integer", "minimum": 1},
						"endLine":    map[string]any{"type": "integer", "minimum": 1},
						"severity":   map[string]any{"type": "string", "enum": []string{"critical", "high", "medium", "low", "info"}},
						"category":   map[string]any{"type": "string", "enum": []string{"security", "correctness", "performance", "database", "maintainability", "quality"}},
						"title":      map[string]any{"type": "string"},
						"message":    map[string]any{"type": "string"},
						"suggestion": map[string]any{"type": "string"},
						"proposedFix": map[string]any{
							"anyOf": []any{
								map[string]any{
									"type":                 "object",
									"additionalProperties": false,
									"required":             []string{"description", "startLine", "endLine", "replacement"},
									"properties": map[string]any{
										"description": map[string]any{"type": "string", "minLength": 1, "maxLength": findings.MaxFixDescriptionBytes},
										"startLine":   map[string]any{"type": "integer", "minimum": 1},
										"endLine":     map[string]any{"type": "integer", "minimum": 1},
										"replacement": map[string]any{"type": "string", "maxLength": findings.MaxFixReplacementBytes},
									},
								},
								map[string]any{"type": "null"},
							},
						},
						"confidence": map[string]any{"type": "number", "minimum": 0, "maximum": 1},
					},
				},
			},
		},
	}
}

func triageSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"status", "escalate", "surfaces", "rationale"},
		"properties": map[string]any{
			"status": map[string]any{
				"type": "string",
				"enum": []string{string(ai.ResponseStatusComplete)},
			},
			"escalate": map[string]any{
				"type": "boolean",
			},
			"surfaces": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type":      "string",
					"maxLength": 500,
				},
			},
			"rationale": map[string]any{
				"type":      "string",
				"maxLength": 2000,
			},
		},
	}
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

func apiError(status int, body []byte, apiKey string) error {
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
	return fmt.Errorf("OpenAI API returned HTTP %d: %s", status, message)
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

func ensureJSONEnd(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return fmt.Errorf("OpenAI structured review contains trailing JSON")
		}
		return fmt.Errorf("decode trailing OpenAI structured review: %w", err)
	}
	return nil
}

func validateEndpoint(value string) error {
	parsed, err := url.Parse(value)
	if err != nil {
		return fmt.Errorf("parse OpenAI endpoint: %w", err)
	}
	if parsed.Scheme == "https" && parsed.Host != "" {
		return nil
	}
	hostname := parsed.Hostname()
	if parsed.Scheme == "http" && (hostname == "localhost" || hostname == "127.0.0.1" || hostname == "::1") {
		return nil
	}
	return fmt.Errorf("OpenAI endpoint must use HTTPS or local HTTP")
}
