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
	return review.Result{
		SchemaVersion: review.SchemaVersion,
		Summary: review.Summary{
			FilesChanged:  1,
			FilesReviewed: 1,
			AddedLines:    1,
			FindingCount:  1,
		},
		Findings: []findings.Finding{{
			File:       "config.go",
			StartLine:  3,
			EndLine:    3,
			Severity:   findings.SeverityHigh,
			Category:   findings.CategorySecurity,
			Title:      "Potential hardcoded secret",
			Message:    "A credential may be hardcoded.",
			Suggestion: "Use a credential store.",
			Confidence: 0.98,
			Source:     findings.SourceLocalRule,
		}},
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) {
	return 0, errors.New("write failed")
}
