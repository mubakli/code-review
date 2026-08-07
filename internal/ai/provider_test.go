package ai

import (
	"encoding/json"
	"strings"
	"testing"

	"code-review/internal/findings"
	"code-review/internal/redact"
)

func TestAnalysisRequestRedactsAndCopiesInput(t *testing.T) {
	t.Parallel()

	secret := "actual-secret-value"
	staticFindings := []findings.Finding{{Title: "local finding"}}
	request := newAnalysisRequest("Review this diff.", `password = "`+secret+`"`, staticFindings, 0)
	staticFindings[0].Title = "mutated"

	if strings.Contains(request.Diff(), secret) || !strings.Contains(request.Diff(), redact.Placeholder) {
		t.Fatalf("Diff() was not redacted: %s", request.Diff())
	}
	if request.RedactionCount() != 1 {
		t.Fatalf("RedactionCount() = %d, want 1", request.RedactionCount())
	}
	if request.Instructions() != "Review this diff." {
		t.Fatalf("Instructions() = %q", request.Instructions())
	}
	if got := request.StaticFindings()[0].Title; got != "local finding" {
		t.Fatalf("StaticFindings()[0].Title = %q", got)
	}

	returned := request.StaticFindings()
	returned[0].Title = "changed again"
	if got := request.StaticFindings()[0].Title; got != "local finding" {
		t.Fatal("StaticFindings() exposed mutable request storage")
	}
}

func TestAnalysisRequestJSONNeverContainsRawSecret(t *testing.T) {
	t.Parallel()

	secret := "provider-secret-value"
	request := newAnalysisRequest("Review this diff.", `api_key: "`+secret+`"`, nil, 0)
	encoded, err := json.Marshal(request)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	if strings.Contains(string(encoded), secret) {
		t.Fatalf("JSON contains raw secret: %s", encoded)
	}
	if !strings.Contains(string(encoded), redact.Placeholder) {
		t.Fatalf("JSON does not contain redaction marker: %s", encoded)
	}
}
