package output

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"code-review/internal/findings"
	"code-review/internal/review"
)

func TestWriteJSON(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	if err := WriteJSON(&output, sampleResult()); err != nil {
		t.Fatalf("WriteJSON() error = %v", err)
	}
	var decoded review.Result
	if err := json.Unmarshal(output.Bytes(), &decoded); err != nil {
		t.Fatalf("decode JSON: %v\n%s", err, output.String())
	}
	if decoded.SchemaVersion != review.SchemaVersion || decoded.Summary.FindingCount != 1 || len(decoded.Findings) != 1 {
		t.Fatalf("decoded result = %#v", decoded)
	}
	var document map[string]any
	if err := json.Unmarshal(output.Bytes(), &document); err != nil {
		t.Fatalf("decode generic JSON: %v", err)
	}
	encodedFindings, _ := document["findings"].([]any)
	encodedFinding, _ := encodedFindings[0].(map[string]any)
	for _, required := range []string{"ruleId", "findingId"} {
		if _, exists := encodedFinding[required]; !exists {
			t.Errorf("canonical finding JSON omits %q: %s", required, output.String())
		}
	}
	if _, exists := encodedFinding["proposedFix"]; !exists {
		t.Errorf("canonical finding JSON omits present proposedFix: %s", output.String())
	}
}

func TestWriteJSONIncludesOptionalAISummary(t *testing.T) {
	t.Parallel()

	result := sampleResult()
	result.AI = &review.AISummary{Provider: "openai", Model: "review-model", Agents: []string{"correctness"}, ReviewedFiles: []string{"main.go"}, BatchCount: 2, SuccessfulBatches: 1, FailedBatches: 1}
	var output bytes.Buffer
	if err := WriteJSON(&output, result); err != nil {
		t.Fatalf("WriteJSON() error = %v", err)
	}
	var decoded review.Result
	if err := json.Unmarshal(output.Bytes(), &decoded); err != nil {
		t.Fatalf("decode JSON: %v", err)
	}
	if decoded.AI == nil || decoded.AI.Provider != "openai" || decoded.AI.FailedBatches != 1 || len(decoded.AI.ReviewedFiles) != 1 || len(decoded.AI.Agents) != 1 {
		t.Fatalf("decoded AI summary = %#v", decoded.AI)
	}
}

func TestWriteHuman(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	if err := WriteHuman(&output, sampleResult()); err != nil {
		t.Fatalf("WriteHuman() error = %v", err)
	}
	for _, expected := range []string{
		"1 file changed, 1 reviewed, 0 skipped (1 addition, 0 deletions).",
		"HIGH [security]",
		"config.go:3",
		"Potential hardcoded secret",
		"Source: local-rule | Confidence: 98%",
	} {
		if !strings.Contains(output.String(), expected) {
			t.Errorf("output does not contain %q:\n%s", expected, output.String())
		}
	}
}

func TestWriteHumanIncludesAISummary(t *testing.T) {
	t.Parallel()

	result := sampleResult()
	result.AI = &review.AISummary{
		Provider:          "openai",
		Model:             "review-model",
		Agents:            []string{"correctness"},
		BatchCount:        3,
		SuccessfulBatches: 2,
		FailedBatches:     1,
		Failures:          []review.AIFailure{{AgentID: "correctness", Batch: 2, Files: []string{"config.go"}, Message: "rate limit\u001b[31m"}},
	}
	var output bytes.Buffer
	if err := WriteHuman(&output, result); err != nil {
		t.Fatalf("WriteHuman() error = %v", err)
	}
	if !strings.Contains(output.String(), "AI review (openai/review-model): 2 of 3 batches succeeded, 1 failed.") {
		t.Fatalf("AI summary is missing:\n%s", output.String())
	}
	if !strings.Contains(output.String(), "AI agents: correctness.") {
		t.Fatalf("AI agents are missing:\n%s", output.String())
	}
	if !strings.Contains(output.String(), "AI agent correctness batch 2 failed for config.go: rate limit�[31m") || strings.Contains(output.String(), "\u001b") {
		t.Fatalf("AI failure was not safely rendered:\n%s", output.String())
	}
}

func TestWriteHumanEscapesControlCharactersInPath(t *testing.T) {
	t.Parallel()

	result := sampleResult()
	result.Findings[0].File = "bad\npath.go"
	var output bytes.Buffer
	if err := WriteHuman(&output, result); err != nil {
		t.Fatalf("WriteHuman() error = %v", err)
	}
	if !strings.Contains(output.String(), `"bad\npath.go":3`) {
		t.Fatalf("path was not escaped:\n%s", output.String())
	}
}

func TestWriteHumanEscapesBidirectionalControlsInPath(t *testing.T) {
	t.Parallel()

	result := sampleResult()
	result.Findings[0].File = "safe\u202Eog.niam"
	var output bytes.Buffer
	if err := WriteHuman(&output, result); err != nil {
		t.Fatalf("WriteHuman() error = %v", err)
	}
	if !strings.Contains(output.String(), `"safe\u202eog.niam":3`) {
		t.Fatalf("bidirectional control was not escaped:\n%s", output.String())
	}
}

func TestWritersReturnOutputErrors(t *testing.T) {
	t.Parallel()

	writer := failingWriter{}
	if err := WriteJSON(writer, sampleResult()); err == nil {
		t.Fatal("WriteJSON() error = nil")
	}
	if err := WriteHuman(writer, sampleResult()); err == nil {
		t.Fatal("WriteHuman() error = nil")
	}
}

func sampleResult() review.Result {
	finding := findings.Finding{
		RuleID:     "secrets/hardcoded-secret",
		File:       "config.go",
		StartLine:  3,
		EndLine:    3,
		Severity:   findings.SeverityHigh,
		Category:   findings.CategorySecurity,
		Title:      "Potential hardcoded secret",
		Message:    "A credential may be hardcoded.",
		Suggestion: "Use a credential store.",
		ProposedFix: &findings.ProposedFix{
			Description: "Read the credential from secure storage.",
			StartLine:   3,
			EndLine:     3,
			Replacement: "const apiKey = loadSecret()",
		},
		Confidence: 0.98,
		Source:     findings.SourceLocalRule,
	}
	finding.FinalizeID()
	return review.Result{
		SchemaVersion: review.SchemaVersion,
		Summary: review.Summary{
			FilesChanged:  1,
			FilesReviewed: 1,
			AddedLines:    1,
			FindingCount:  1,
		},
		Findings: []findings.Finding{finding},
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) {
	return 0, errors.New("write failed")
}
