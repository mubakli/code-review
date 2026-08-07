package deepseek

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
	defaultEndpoint        = "https://api.deepseek.com/chat/completions"
	defaultMaxOutputTokens = 4096
	maxResponseBytes       = 4 << 20
)

type Options struct {
	APIKey          string
	Model           string
	Endpoint        string
	MaxOutputTokens int
	HTTPClient      *http.Client
	AllowHTTP       bool
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
		return nil, fmt.Errorf("DeepSeek API key is required")
	}
	model := strings.TrimSpace(options.Model)
	if model == "" {
		return nil, fmt.Errorf("DeepSeek model is required")
	}
	endpoint := strings.TrimSpace(options.Endpoint)
	if endpoint == "" {
		endpoint = defaultEndpoint
	}
	parsed, err := url.Parse(endpoint)
	if err != nil || (parsed.Scheme != "https" && !(options.AllowHTTP && parsed.Scheme == "http")) || parsed.Host == "" || parsed.User != nil {
		return nil, fmt.Errorf("DeepSeek endpoint must be an HTTPS URL without credentials")
	}
	maxOutputTokens := options.MaxOutputTokens
	if maxOutputTokens == 0 {
		maxOutputTokens = defaultMaxOutputTokens
	}
	if maxOutputTokens < 1 {
		return nil, fmt.Errorf("DeepSeek max output tokens must be positive")
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
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	input, err := json.Marshal(struct {
		Diff           string             `json:"diff"`
		StaticFindings []findings.Finding `json:"staticFindings,omitempty"`
	}{
		Diff:           request.Diff(),
		StaticFindings: request.StaticFindings(),
	})
	if err != nil {
		return nil, fmt.Errorf("encode DeepSeek review input: %w", err)
	}
	payload, err := json.Marshal(chatRequest{
		Model: p.model,
		Messages: []message{
			{Role: "system", Content: request.Instructions() + structuredOutputInstructions},
			{Role: "user", Content: string(input)},
		},
		ResponseFormat:  responseFormat{Type: "json_object"},
		MaxOutputTokens: p.maxOutputTokens,
		Stream:          false,
	})
	if err != nil {
		return nil, fmt.Errorf("encode DeepSeek request: %w", err)
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("create DeepSeek request: %w", err)
	}
	httpRequest.Header.Set("Authorization", "Bearer "+p.apiKey)
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("User-Agent", "code-review/0")

	response, err := p.client.Do(httpRequest)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, fmt.Errorf("call DeepSeek API: %w", err)
	}
	defer response.Body.Close()
	body, err := readBounded(response.Body)
	if err != nil {
		return nil, fmt.Errorf("read DeepSeek response: %w", err)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, apiError(response.StatusCode, body, p.apiKey)
	}
	var envelope chatResponse
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, fmt.Errorf("decode DeepSeek response envelope: %w", err)
	}
	if len(envelope.Choices) == 0 || strings.TrimSpace(envelope.Choices[0].Message.Content) == "" {
		return nil, fmt.Errorf("DeepSeek response contains no output text")
	}
	decoder := json.NewDecoder(strings.NewReader(envelope.Choices[0].Message.Content))
	decoder.DisallowUnknownFields()
	var analysis ai.AnalysisResponse
	if err := decoder.Decode(&analysis); err != nil {
		return nil, fmt.Errorf("decode DeepSeek structured review: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, fmt.Errorf("DeepSeek structured review contains trailing JSON")
	}
	return &analysis, nil
}

const structuredOutputInstructions = ` Return exactly one JSON object with this shape and no markdown: {"status":"complete","findings":[{"file":"relative/path","startLine":1,"endLine":1,"severity":"critical|high|medium|low|info","category":"security|correctness|performance|database|maintainability|quality","title":"short title","message":"evidence-based explanation","suggestion":"concise remediation or empty string","proposedFix":null,"confidence":0.0}]}. Every finding must include proposedFix as null or {"description":"nonblank description","startLine":1,"endLine":1,"replacement":"complete replacement text without diff prefixes"}. A proposed fix replaces complete lines, its range must exactly equal the finding range, and every line in both ranges must be an added diff line. Use null unless an exact safe replacement is possible. Never use redaction or truncation placeholders in replacement text. Do not return ruleId or findingId; those are assigned by the reviewer. Use an empty findings array when no issue exists.`

type chatRequest struct {
	Model           string         `json:"model"`
	Messages        []message      `json:"messages"`
	ResponseFormat  responseFormat `json:"response_format"`
	MaxOutputTokens int            `json:"max_tokens"`
	Stream          bool           `json:"stream"`
}

type message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type responseFormat struct {
	Type string `json:"type"`
}

type chatResponse struct {
	Choices []struct {
		Message message `json:"message"`
	} `json:"choices"`
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
	return fmt.Errorf("DeepSeek API returned HTTP %d: %s", status, message)
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
