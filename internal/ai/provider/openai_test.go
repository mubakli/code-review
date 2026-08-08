package provider

import (
	stdcontext "context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"code-review/internal/findings"
	"code-review/internal/redact"
)

func TestOpenAIAnalyzeUsesPrivateStructuredRequest(t *testing.T) {
	t.Parallel()

	apiKey := "test-api-key"
	secret := "source-secret-value"
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != "/v1/responses" {
			t.Errorf("request = %s %s", request.Method, request.URL.Path)
		}
		if got := request.Header.Get("Authorization"); got != "Bearer "+apiKey {
			t.Errorf("Authorization = %q", got)
		}
		var payload map[string]any
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if payload["model"] != "review-model" || payload["store"] != false {
			t.Errorf("unexpected request settings: %#v", payload)
		}
		if payload["max_output_tokens"] != float64(1200) {
			t.Errorf("max_output_tokens = %#v", payload["max_output_tokens"])
		}
		input, _ := payload["input"].(string)
		if strings.Contains(input, secret) || !strings.Contains(input, redact.Placeholder) {
			t.Errorf("provider request contains unredacted input: %s", input)
		}
		text, ok := payload["text"].(map[string]any)
		if !ok {
			t.Fatalf("text config = %#v", payload["text"])
		}
		format, ok := text["format"].(map[string]any)
		if !ok || format["type"] != "json_schema" || format["strict"] != true {
			t.Fatalf("structured format = %#v", text["format"])
		}
		schema, _ := format["schema"].(map[string]any)
		properties, _ := schema["properties"].(map[string]any)
		findingsSchema, _ := properties["findings"].(map[string]any)
		items, _ := findingsSchema["items"].(map[string]any)
		findingProperties, _ := items["properties"].(map[string]any)
		fixSchema, ok := findingProperties["proposedFix"].(map[string]any)
		if !ok || len(fixSchema["anyOf"].([]any)) != 2 {
			t.Fatalf("proposedFix schema = %#v", findingProperties["proposedFix"])
		}
		for _, forbidden := range []string{"ruleId", "findingId"} {
			if _, exists := findingProperties[forbidden]; exists {
				t.Fatalf("provider schema accepts trusted field %q", forbidden)
			}
		}

		structured, err := json.Marshal(AnalysisResponse{
			Status: ResponseStatusComplete,
			Findings: []ResponseFinding{{
				File:       "service.go",
				StartLine:  2,
				EndLine:    2,
				Severity:   findings.SeverityHigh,
				Category:   findings.CategorySecurity,
				Title:      "Potential credential",
				Message:    "A credential may be exposed.",
				Suggestion: "Use secret storage.",
				ProposedFix: &findings.ProposedFix{
					Description: "Read from secret storage.",
					StartLine:   2,
					EndLine:     2,
					Replacement: "password = loadSecret()",
				},
				Confidence: 0.95,
			}},
		})
		if err != nil {
			t.Fatalf("encode structured fixture: %v", err)
		}
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(map[string]any{
			"output": []any{map[string]any{
				"type": "message",
				"content": []any{map[string]any{
					"type": "output_text",
					"text": string(structured),
				}},
			}},
		})
	}))
	defer server.Close()

	instance, err := NewOpenAI(OpenAIOptions{
		APIKey:          apiKey,
		Model:           "review-model",
		Endpoint:        server.URL + "/v1/responses",
		MaxOutputTokens: 1200,
		HTTPClient:      server.Client(),
	})
	if err != nil {
		t.Fatalf("NewOpenAI() error = %v", err)
	}
	response, err := instance.Analyze(stdcontext.Background(), preparedRequest(t, secret))
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	if response.Status != ResponseStatusComplete || len(response.Findings) != 1 {
		t.Fatalf("response = %#v", response)
	}
	if response.Findings[0].ProposedFix == nil || response.Findings[0].ProposedFix.Replacement != "password = loadSecret()" {
		t.Fatalf("proposed fix = %#v", response.Findings[0].ProposedFix)
	}
}

func TestOpenAIReturnsBoundedAPIErrorWithoutKey(t *testing.T) {
	t.Parallel()

	apiKey := "must-not-appear-in-error"
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusTooManyRequests)
		_ = json.NewEncoder(writer).Encode(map[string]any{"error": map[string]any{"message": "rate limit exceeded for " + apiKey}})
	}))
	defer server.Close()
	instance, err := NewOpenAI(OpenAIOptions{APIKey: apiKey, Model: "review-model", Endpoint: server.URL, HTTPClient: server.Client()})
	if err != nil {
		t.Fatalf("NewOpenAI() error = %v", err)
	}
	_, err = instance.Analyze(stdcontext.Background(), preparedRequest(t, "secret-value"))
	if err == nil || !strings.Contains(err.Error(), "HTTP 429") || strings.Contains(err.Error(), apiKey) {
		t.Fatalf("Analyze() error = %v", err)
	}
}

func TestOpenAIRejectsMalformedStructuredOutput(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(writer).Encode(map[string]any{"output_text": `{"status":"complete","findings":[],"unexpected":true}`})
	}))
	defer server.Close()
	instance, err := NewOpenAI(OpenAIOptions{APIKey: "test-key", Model: "review-model", Endpoint: server.URL, HTTPClient: server.Client()})
	if err != nil {
		t.Fatalf("NewOpenAI() error = %v", err)
	}
	if _, err := instance.Analyze(stdcontext.Background(), preparedRequest(t, "secret-value")); err == nil {
		t.Fatal("Analyze() error = nil")
	}
}

func TestOpenAIHonorsCancellation(t *testing.T) {
	t.Parallel()

	instance, err := NewOpenAI(OpenAIOptions{APIKey: "test-key", Model: "review-model"})
	if err != nil {
		t.Fatalf("NewOpenAI() error = %v", err)
	}
	ctx, cancel := stdcontext.WithCancel(stdcontext.Background())
	cancel()
	_, err = instance.Analyze(ctx, preparedRequest(t, "secret-value"))
	if !errors.Is(err, stdcontext.Canceled) {
		t.Fatalf("Analyze() error = %v, want context.Canceled", err)
	}
}

func TestNewOpenAIRejectsUnsafeOrIncompleteOptions(t *testing.T) {
	t.Parallel()

	tests := []OpenAIOptions{
		{Model: "review-model"},
		{APIKey: "test-key"},
		{APIKey: "test-key", Model: "review-model", Endpoint: "http://example.com/v1/responses"},
		{APIKey: "test-key", Model: "review-model", MaxOutputTokens: -1},
	}
	for _, options := range tests {
		if _, err := NewOpenAI(options); err == nil {
			t.Errorf("NewOpenAI(%+v) error = nil", options)
		}
	}
}

func TestOpenAITriageProducesStructuredDecision(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		structured, err := json.Marshal(TriageResponse{
			Status:    ResponseStatusComplete,
			Escalate:  true,
			Surfaces:  []string{"input handling", "command execution"},
			Rationale: "User input reaches an exec call.",
		})
		if err != nil {
			t.Fatalf("encode structured triage fixture: %v", err)
		}
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(map[string]any{"output_text": string(structured)})
	}))
	defer server.Close()

	instance, err := NewOpenAI(OpenAIOptions{APIKey: "test-key", Model: "review-model", Endpoint: server.URL, HTTPClient: server.Client()})
	if err != nil {
		t.Fatalf("NewOpenAI() error = %v", err)
	}
	response, err := instance.Triage(stdcontext.Background(), preparedRequest(t, "test-secret"))
	if err != nil {
		t.Fatalf("Triage() error = %v", err)
	}
	if !response.Escalate || len(response.Surfaces) != 2 || response.Rationale != "User input reaches an exec call." {
		t.Fatalf("triage response = %#v", response)
	}
}

func TestOpenAIRejectsMalformedTriageOutput(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(writer).Encode(map[string]any{"output_text": `{"status":"complete","escalate":true,"unexpected":true}`})
	}))
	defer server.Close()
	instance, err := NewOpenAI(OpenAIOptions{APIKey: "test-key", Model: "review-model", Endpoint: server.URL, HTTPClient: server.Client()})
	if err != nil {
		t.Fatalf("NewOpenAI() error = %v", err)
	}
	_, err = instance.Triage(stdcontext.Background(), preparedRequest(t, "test-secret"))
	if err == nil {
		t.Fatal("Triage() error = nil for malformed output")
	}
}
