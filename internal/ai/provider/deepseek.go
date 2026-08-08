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

	"code-review/internal/ai/request"
	"code-review/internal/ai/routing"
)

const deepSeekEndpoint = "https://api.deepseek.com/chat/completions"

type DeepSeekOptions struct {
	APIKey          string
	Model           string
	Endpoint        string
	MaxOutputTokens int
	HTTPClient      *http.Client
	AllowHTTP       bool
}

// DeepSeek executes analysis and triage requests against the DeepSeek Chat
// Completions API and normalizes the raw output into the common structured
// responses.
type DeepSeek struct {
	apiKey          string
	model           string
	endpoint        string
	maxOutputTokens int
	client          *http.Client
}

var _ Provider = (*DeepSeek)(nil)

func NewDeepSeek(options DeepSeekOptions) (*DeepSeek, error) {
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
		endpoint = deepSeekEndpoint
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
	return &DeepSeek{
		apiKey:          apiKey,
		model:           model,
		endpoint:        endpoint,
		maxOutputTokens: maxOutputTokens,
		client:          client,
	}, nil
}

func (p *DeepSeek) Analyze(ctx stdcontext.Context, request request.AnalysisRequest) (*AnalysisResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	input, err := json.Marshal(requestInput{
		Diff:           request.Diff(),
		StaticFindings: request.StaticFindings(),
		RelatedContext: request.ContextFiles(),
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
	content, err := p.post(ctx, payload)
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(strings.NewReader(content))
	decoder.DisallowUnknownFields()
	var analysis AnalysisResponse
	if err := decoder.Decode(&analysis); err != nil {
		return nil, fmt.Errorf("decode DeepSeek structured review: %w", err)
	}
	if err := ensureJSONEnd(decoder, "DeepSeek"); err != nil {
		return nil, err
	}
	return &analysis, nil
}

func (p *DeepSeek) Triage(ctx stdcontext.Context, request request.AnalysisRequest) (*routing.SecurityAssessment, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	input, err := json.Marshal(requestInput{
		Diff:           request.Diff(),
		StaticFindings: request.StaticFindings(),
		RelatedContext: request.ContextFiles(),
	})
	if err != nil {
		return nil, fmt.Errorf("encode DeepSeek triage input: %w", err)
	}
	payload, err := json.Marshal(chatRequest{
		Model: p.model,
		Messages: []message{
			{Role: "system", Content: request.Instructions() + triageStructuredOutputInstructions},
			{Role: "user", Content: string(input)},
		},
		ResponseFormat:  responseFormat{Type: "json_object"},
		MaxOutputTokens: triageMaxOutputTokens,
		Stream:          false,
	})
	if err != nil {
		return nil, fmt.Errorf("encode DeepSeek triage request: %w", err)
	}
	content, err := p.post(ctx, payload)
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(strings.NewReader(content))
	decoder.DisallowUnknownFields()
	var assessment routing.SecurityAssessment
	if err := decoder.Decode(&assessment); err != nil {
		return nil, fmt.Errorf("decode DeepSeek structured triage: %w", err)
	}
	if err := ensureJSONEnd(decoder, "DeepSeek"); err != nil {
		return nil, err
	}
	return &assessment, nil
}

func (p *DeepSeek) post(ctx stdcontext.Context, payload []byte) (string, error) {
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint, bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("create DeepSeek request: %w", err)
	}
	httpRequest.Header.Set("Authorization", "Bearer "+p.apiKey)
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("User-Agent", "code-review/0")

	response, err := p.client.Do(httpRequest)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return "", ctxErr
		}
		return "", fmt.Errorf("call DeepSeek API: %w", err)
	}
	defer response.Body.Close()
	body, err := readBounded(response.Body)
	if err != nil {
		return "", fmt.Errorf("read DeepSeek response: %w", err)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return "", apiError("DeepSeek", response.StatusCode, body, p.apiKey)
	}
	var envelope chatResponse
	if err := json.Unmarshal(body, &envelope); err != nil {
		return "", fmt.Errorf("decode DeepSeek response envelope: %w", err)
	}
	if len(envelope.Choices) == 0 || strings.TrimSpace(envelope.Choices[0].Message.Content) == "" {
		return "", fmt.Errorf("DeepSeek response contains no output text")
	}
	return envelope.Choices[0].Message.Content, nil
}

const structuredOutputInstructions = ` Return exactly one JSON object with this shape and no markdown: {"status":"complete","findings":[{"file":"relative/path","startLine":1,"endLine":1,"severity":"critical|high|medium|low|info","category":"security|correctness|performance|database|maintainability|quality","title":"short title","message":"evidence-based explanation","suggestion":"concise remediation or empty string","proposedFix":null,"confidence":0.0}]}. Every finding must include proposedFix as null or {"description":"nonblank description","startLine":1,"endLine":1,"replacement":"complete replacement text without diff prefixes"}. A proposed fix replaces complete lines, its range must exactly equal the finding range, and every line in both ranges must be an added diff line. Use null unless an exact safe replacement is possible. Never use redaction or truncation placeholders in replacement text. Do not return ruleId or findingId; those are assigned by the reviewer. Use an empty findings array when no issue exists.`

const triageStructuredOutputInstructions = ` Return exactly one JSON object with this shape and no markdown: {"escalate":true,"confidence":"high|medium|low","surfaces":["user-controlled-input reaches a database query built by string concatenation"],"reasons":["The surface awaits confirmation: the surrounding enforcement may live outside this diff"]}. Keep the payload tiny: at most 3 short surfaces and 2 short reasons. This is a routing decision, not a diagnosis: never claim that a vulnerability exists or is absent. surfaces is an array of short labels describing what the deep security agent must examine, phrased as observables from this diff (data flows, control-flow choices, changed boundaries); never label them as confirmed flaws. reasons states in at most two short sentences why deep analysis is warranted, noting that enforcement may live outside the diff when relevant. escalate must be true whenever such a surface is plausible, including when you are uncertain or enforcement is elsewhere; escalate must be false only when the change is clearly unrelated to security.`

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
