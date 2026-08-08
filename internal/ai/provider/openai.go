package provider

import (
	"bytes"
	stdcontext "context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"code-review/internal/ai/context"
	"code-review/internal/ai/request"
	"code-review/internal/ai/routing"
	"code-review/internal/findings"
)

const openAIEndpoint = "https://api.openai.com/v1/responses"

type OpenAIOptions struct {
	APIKey          string
	Model           string
	Endpoint        string
	MaxOutputTokens int
	HTTPClient      *http.Client
}

// OpenAI executes analysis and triage requests against the OpenAI Responses
// API and normalizes the raw output into the common structured responses.
type OpenAI struct {
	apiKey          string
	model           string
	endpoint        string
	maxOutputTokens int
	client          *http.Client
}

var _ Provider = (*OpenAI)(nil)

func NewOpenAI(options OpenAIOptions) (*OpenAI, error) {
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
		endpoint = openAIEndpoint
	}
	if err := validateOpenAIEndpoint(endpoint); err != nil {
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
	return &OpenAI{
		apiKey:          apiKey,
		model:           model,
		endpoint:        endpoint,
		maxOutputTokens: maxOutputTokens,
		client:          client,
	}, nil
}

func (p *OpenAI) Analyze(ctx stdcontext.Context, request request.AnalysisRequest) (*AnalysisResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	input, err := json.Marshal(requestInput{
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
	var analysis AnalysisResponse
	if err := decoder.Decode(&analysis); err != nil {
		return nil, fmt.Errorf("decode OpenAI structured review: %w", err)
	}
	if err := ensureJSONEnd(decoder, "OpenAI"); err != nil {
		return nil, err
	}
	return &analysis, nil
}

func (p *OpenAI) Triage(ctx stdcontext.Context, request request.AnalysisRequest) (*routing.SecurityAssessment, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	input, err := json.Marshal(requestInput{
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
	var assessment routing.SecurityAssessment
	if err := decoder.Decode(&assessment); err != nil {
		return nil, fmt.Errorf("decode OpenAI structured triage: %w", err)
	}
	if err := ensureJSONEnd(decoder, "OpenAI"); err != nil {
		return nil, err
	}
	return &assessment, nil
}

func (p *OpenAI) post(ctx stdcontext.Context, payload []byte) (string, error) {
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
		return "", apiError("OpenAI", response.StatusCode, body, p.apiKey)
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

type requestInput struct {
	Diff           string                `json:"diff"`
	StaticFindings []findings.Finding    `json:"staticFindings,omitempty"`
	RelatedContext []context.ContextFile `json:"relatedContext,omitempty"`
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
				"enum": []string{string(ResponseStatusComplete)},
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
		"required":             []string{"escalate", "confidence", "surfaces", "reasons"},
		"properties": map[string]any{
			"escalate": map[string]any{
				"type":        "boolean",
				"description": "True when the deep security agent must examine the change.",
			},
			"confidence": map[string]any{
				"type":        "string",
				"enum":        []string{"high", "medium", "low"},
				"description": "How strongly the diff points at a security surface.",
			},
			"surfaces": map[string]any{
				"type":        "array",
				"description": "Observables from the diff the deep security agent must examine (data flows, control-flow choices, changed boundaries) as areas to inspect; never confirmed vulnerabilities. Keep this list small (1-3 entries).",
				"items": map[string]any{
					"type":      "string",
					"maxLength": 500,
				},
			},
			"reasons": map[string]any{
				"type":        "array",
				"description": "Why deep analysis is warranted, noting when enforcement may live outside this diff; never a vulnerability claim. Keep this list small (1-2 entries).",
				"items": map[string]any{
					"type":      "string",
					"maxLength": 300,
				},
			},
		},
	}
}

func validateOpenAIEndpoint(value string) error {
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
