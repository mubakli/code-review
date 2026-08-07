package deepseek

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"code-review/internal/ai"
	"code-review/internal/change"
)

func TestProviderUsesStructuredChatCompletionWithoutLeakingSecret(t *testing.T) {
	t.Parallel()

	apiKey := "deepseek-test-key"
	secret := "source-secret-value"
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer "+apiKey {
			t.Errorf("Authorization = %q", request.Header.Get("Authorization"))
		}
		var payload map[string]any
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		encoded, _ := json.Marshal(payload)
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("request contains raw secret: %s", encoded)
		}
		format, _ := payload["response_format"].(map[string]any)
		if format["type"] != "json_object" || payload["stream"] != false {
			t.Fatalf("request settings = %#v", payload)
		}
		messages, _ := payload["messages"].([]any)
		system, _ := messages[0].(map[string]any)
		instructions, _ := system["content"].(string)
		for _, expected := range []string{"proposedFix", "exactly equal", "added diff line", "Do not return ruleId or findingId"} {
			if !strings.Contains(instructions, expected) {
				t.Errorf("structured instructions do not contain %q: %s", expected, instructions)
			}
		}
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(map[string]any{
			"choices": []any{map[string]any{
				"message": map[string]any{
					"role":    "assistant",
					"content": `{"status":"complete","findings":[]}`,
				},
			}},
		})
	}))
	defer server.Close()

	provider, err := New(Options{
		APIKey:     apiKey,
		Model:      "deepseek-chat",
		Endpoint:   server.URL + "/chat/completions",
		HTTPClient: server.Client(),
		AllowHTTP:  true,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	response, err := provider.Analyze(context.Background(), preparedRequest(t, secret))
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	if response.Status != ai.ResponseStatusComplete || len(response.Findings) != 0 {
		t.Fatalf("response = %#v", response)
	}
}

func TestProviderRejectsUnsafeOptionsAndRedactsAPIError(t *testing.T) {
	t.Parallel()

	if _, err := New(Options{APIKey: "key", Model: "model", Endpoint: "http://example.com"}); err == nil {
		t.Fatal("New() accepted HTTP endpoint")
	}
	apiKey := "must-not-leak"
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusTooManyRequests)
		_ = json.NewEncoder(writer).Encode(map[string]any{"error": map[string]any{"message": "failed for " + apiKey}})
	}))
	defer server.Close()
	provider, err := New(Options{APIKey: apiKey, Model: "deepseek-chat", Endpoint: server.URL, HTTPClient: server.Client(), AllowHTTP: true})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	_, err = provider.Analyze(context.Background(), preparedRequest(t, "secret"))
	if err == nil || strings.Contains(err.Error(), apiKey) {
		t.Fatalf("Analyze() error = %v", err)
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
		Hunks: []change.Hunk{{Lines: []change.Line{
			{Kind: change.LineAdded, NewLine: 1, Content: `password = "` + secret + `"`},
		}}},
	}}}
	batches, err := builder.Build(context.Background(), changes, nil)
	if err != nil || len(batches) != 1 {
		t.Fatalf("Build() = %#v, %v", batches, err)
	}
	return batches[0].Request
}
