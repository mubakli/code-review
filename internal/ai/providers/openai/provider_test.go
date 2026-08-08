package openai

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"code-review/internal/ai"
	"code-review/internal/change"
	"code-review/internal/findings"
	"code-review/internal/redact"
)

func TestProviderAnalyzeUsesPrivateStructuredRequest(t *testing.T) {
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

		structured, err := json.Marshal(ai.AnalysisResponse{
			Status: ai.ResponseStatusComplete,
			Findings: []ai.ResponseFinding{{
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

	provider, err := New(Options{
		APIKey:          apiKey,
		Model:           "review-model",
		Endpoint:        server.URL + "/v1/responses",
		MaxOutputTokens: 1200,
		HTTPClient:      server.Client(),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	response, err := provider.Analyze(context.Background(), preparedRequest(t, secret))
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	if response.Status != ai.ResponseStatusComplete || len(response.Findings) != 1 {
		t.Fatalf("response = %#v", response)
	}
	if response.Findings[0].ProposedFix == nil || response.Findings[0].ProposedFix.Replacement != "password = loadSecret()" {
		t.Fatalf("proposed fix = %#v", response.Findings[0].ProposedFix)
	}
}

func TestProviderReturnsBoundedAPIErrorWithoutKey(t *testing.T) {
	t.Parallel()

	apiKey := "must-not-appear-in-error"
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusTooManyRequests)
		_ = json.NewEncoder(writer).Encode(map[string]any{"error": map[string]any{"message": "rate limit exceeded for " + apiKey}})
	}))
	defer server.Close()
	provider, err := New(Options{APIKey: apiKey, Model: "review-model", Endpoint: server.URL, HTTPClient: server.Client()})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	_, err = provider.Analyze(context.Background(), preparedRequest(t, "secret-value"))
	if err == nil || !strings.Contains(err.Error(), "HTTP 429") || strings.Contains(err.Error(), apiKey) {
		t.Fatalf("Analyze() error = %v", err)
	}
}

func TestProviderRejectsMalformedStructuredOutput(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(writer).Encode(map[string]any{"output_text": `{"status":"complete","findings":[],"unexpected":true}`})
	}))
	defer server.Close()
	provider, err := New(Options{APIKey: "test-key", Model: "review-model", Endpoint: server.URL, HTTPClient: server.Client()})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if _, err := provider.Analyze(context.Background(), preparedRequest(t, "secret-value")); err == nil {
		t.Fatal("Analyze() error = nil")
	}
}

func TestProviderHonorsCancellation(t *testing.T) {
	t.Parallel()

	provider, err := New(Options{APIKey: "test-key", Model: "review-model"})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = provider.Analyze(ctx, preparedRequest(t, "secret-value"))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Analyze() error = %v, want context.Canceled", err)
	}
}

func TestNewRejectsUnsafeOrIncompleteOptions(t *testing.T) {
	t.Parallel()

	tests := []Options{
		{Model: "review-model"},
		{APIKey: "test-key"},
		{APIKey: "test-key", Model: "review-model", Endpoint: "http://example.com/v1/responses"},
		{APIKey: "test-key", Model: "review-model", MaxOutputTokens: -1},
	}
	for _, options := range tests {
		if _, err := New(options); err == nil {
			t.Errorf("New(%+v) error = nil", options)
		}
	}
}

func preparedRequest(t *testing.T, secret string) ai.AnalysisRequest {
	t.Helper()
	builder, err := ai.New(ai.DefaultBudget())
	if err != nil {
		t.Fatalf("ai.New() error = %v", err)
	}
	changes := change.ChangeSet{Files: []change.FileChange{{
		NewPath: "service.go",
		Status:  change.StatusAdded,
		Hunks: []change.Hunk{{
			OldStart: 0,
			OldLines: 0,
			NewStart: 1,
			NewLines: 2,
			Lines: []change.Line{
				{Kind: change.LineAdded, NewLine: 1, Content: "package service"},
				{Kind: change.LineAdded, NewLine: 2, Content: `password = "` + secret + `"`},
			},
		}},
	}}}
	batches, err := builder.Build(context.Background(), changes, nil, nil)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if len(batches) != 1 {
		t.Fatalf("len(batches) = %d, want 1", len(batches))
	}
	return batches[0].Request
}

func TestProviderTriageProducesStructuredDecision(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		structured, err := json.Marshal(ai.TriageResponse{
			Status:    ai.ResponseStatusComplete,
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

	provider, err := New(Options{APIKey: "test-key", Model: "review-model", Endpoint: server.URL, HTTPClient: server.Client()})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	response, err := provider.Triage(context.Background(), preparedRequest(t, "test-secret"))
	if err != nil {
		t.Fatalf("Triage() error = %v", err)
	}
	if !response.Escalate || len(response.Surfaces) != 2 || response.Rationale != "User input reaches an exec call." {
		t.Fatalf("triage response = %#v", response)
	}
}

func TestProviderRejectsMalformedTriageOutput(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(writer).Encode(map[string]any{"output_text": `{"status":"complete","escalate":true,"unexpected":true}`})
	}))
	defer server.Close()
	provider, err := New(Options{APIKey: "test-key", Model: "review-model", Endpoint: server.URL, HTTPClient: server.Client()})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	_, err = provider.Triage(context.Background(), preparedRequest(t, "test-secret"))
	if err == nil {
		t.Fatal("Triage() error = nil for malformed output")
	}
}
