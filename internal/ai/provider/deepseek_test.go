package provider

import (
	stdcontext "context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"code-review/internal/ai/routing"
)

func TestDeepSeekUsesStructuredChatCompletionWithoutLeakingSecret(t *testing.T) {
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

	instance, err := NewDeepSeek(DeepSeekOptions{
		APIKey:     apiKey,
		Model:      "deepseek-chat",
		Endpoint:   server.URL + "/chat/completions",
		HTTPClient: server.Client(),
		AllowHTTP:  true,
	})
	if err != nil {
		t.Fatalf("NewDeepSeek() error = %v", err)
	}
	response, err := instance.Analyze(stdcontext.Background(), preparedRequest(t, secret))
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	if response.Status != ResponseStatusComplete || len(response.Findings) != 0 {
		t.Fatalf("response = %#v", response)
	}
}

func TestDeepSeekRejectsUnsafeOptionsAndRedactsAPIError(t *testing.T) {
	t.Parallel()

	if _, err := NewDeepSeek(DeepSeekOptions{APIKey: "key", Model: "model", Endpoint: "http://example.com"}); err == nil {
		t.Fatal("NewDeepSeek() accepted HTTP endpoint")
	}
	apiKey := "must-not-leak"
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusTooManyRequests)
		_ = json.NewEncoder(writer).Encode(map[string]any{"error": map[string]any{"message": "failed for " + apiKey}})
	}))
	defer server.Close()
	instance, err := NewDeepSeek(DeepSeekOptions{APIKey: apiKey, Model: "deepseek-chat", Endpoint: server.URL, HTTPClient: server.Client(), AllowHTTP: true})
	if err != nil {
		t.Fatalf("NewDeepSeek() error = %v", err)
	}
	_, err = instance.Analyze(stdcontext.Background(), preparedRequest(t, "secret"))
	if err == nil || strings.Contains(err.Error(), apiKey) {
		t.Fatalf("Analyze() error = %v", err)
	}
}

func TestDeepSeekTriageProducesStructuredDecision(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		structured, err := json.Marshal(routing.SecurityAssessment{
			Escalate:   true,
			Confidence: routing.ConfidenceHigh,
			Surfaces:   []routing.SecuritySurface{"command-execution"},
			Reasons:    []string{"User input reaches a command."},
		})
		if err != nil {
			t.Fatalf("encode structured triage fixture: %v", err)
		}
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(map[string]any{
			"choices": []any{map[string]any{
				"message": map[string]any{"content": string(structured)},
			}},
		})
	}))
	defer server.Close()

	instance, err := NewDeepSeek(DeepSeekOptions{APIKey: "test-key", Model: "deepseek-chat", Endpoint: server.URL, HTTPClient: server.Client(), AllowHTTP: true})
	if err != nil {
		t.Fatalf("NewDeepSeek() error = %v", err)
	}
	response, err := instance.Triage(stdcontext.Background(), preparedRequest(t, "test-secret"))
	if err != nil {
		t.Fatalf("Triage() error = %v", err)
	}
	if !response.Escalate || response.Confidence != routing.ConfidenceHigh || len(response.Surfaces) != 1 || len(response.Reasons) != 1 {
		t.Fatalf("triage response = %#v", response)
	}
}
