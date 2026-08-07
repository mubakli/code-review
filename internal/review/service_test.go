package review

import (
	"context"
	"strings"
	"testing"

	"code-review/internal/change"
	"code-review/internal/findings"
	"code-review/internal/pathfilter"
)

func TestReviewChangesFiltersExcludedBinaryAndPathlessFiles(t *testing.T) {
	t.Parallel()

	changes := change.ChangeSet{Files: []change.FileChange{
		{
			OldPath: "main.go",
			NewPath: "main.go",
			Status:  change.StatusModified,
			Hunks: []change.Hunk{{Lines: []change.Line{
				{Kind: change.LineDeleted, OldLine: 3, Content: "old"},
				{Kind: change.LineAdded, NewLine: 3, Content: "new"},
			}}},
		},
		{NewPath: ".env", Status: change.StatusAdded},
		{NewPath: "logo.png", Status: change.StatusAdded, Binary: true},
		{Status: change.StatusModified},
	}}
	service := New(pathfilter.New(pathfilter.DefaultPatterns()))
	scope, err := service.ScopeChanges(context.Background(), changes)
	if err != nil {
		t.Fatalf("ScopeChanges() error = %v", err)
	}
	if got := scope.Changes(); len(got.Files) != 2 || got.Files[0].Path() != "main.go" || got.Files[1].Path() != ".env" {
		t.Fatalf("scoped changes = %#v", got)
	}
	mutableCopy := scope.Changes()
	mutableCopy.Files[0].Hunks[0].Lines[0].Content = "mutated"
	if scope.Changes().Files[0].Hunks[0].Lines[0].Content == "mutated" {
		t.Fatal("Scope.Changes() exposed mutable internal storage")
	}
	result, err := service.ReviewScope(context.Background(), scope)
	if err != nil {
		t.Fatalf("ReviewChanges() error = %v", err)
	}
	if result.Summary.FilesChanged != 4 || result.Summary.FilesReviewed != 2 || result.Summary.FilesSkipped != 2 {
		t.Fatalf("Summary = %#v", result.Summary)
	}
	if result.Summary.AddedLines != 1 || result.Summary.DeletedLines != 1 || result.Summary.HunksReviewed != 1 {
		t.Errorf("Summary change counts = %#v", result.Summary)
	}
	if result.Findings == nil || len(result.Findings) != 0 {
		t.Fatalf("Findings = %#v, want non-nil empty slice", result.Findings)
	}
}

func TestReviewChangesRejectsInvalidAnalyzerFinding(t *testing.T) {
	t.Parallel()

	service := New(
		pathfilter.New(nil),
		staticAnalyzer{findings: []findings.Finding{{Title: "missing required fields"}}},
	)
	_, err := service.ReviewChanges(context.Background(), change.ChangeSet{})
	if err == nil || !strings.Contains(err.Error(), "finding 1 is invalid") {
		t.Fatalf("ReviewChanges() error = %v", err)
	}
}

func TestReviewChangesRejectsFindingOutsideReviewedFiles(t *testing.T) {
	t.Parallel()

	finding := findings.Finding{
		RuleID:     "static/out-of-scope",
		File:       "../other.go",
		StartLine:  1,
		EndLine:    1,
		Severity:   findings.SeverityHigh,
		Category:   findings.CategorySecurity,
		Title:      "Out-of-scope finding",
		Message:    "This finding does not refer to a reviewed file.",
		Confidence: 0.9,
		Source:     findings.SourceLocalRule,
	}
	service := New(pathfilter.New(nil), staticAnalyzer{findings: []findings.Finding{finding}})
	changes := change.ChangeSet{Files: []change.FileChange{{
		OldPath: "main.go",
		NewPath: "main.go",
		Status:  change.StatusModified,
	}}}
	_, err := service.ReviewChanges(context.Background(), changes)
	if err == nil || !strings.Contains(err.Error(), "outside the reviewed changes") {
		t.Fatalf("ReviewChanges() error = %v", err)
	}
}

func TestReviewChangesFinalizesFindingIDBeforeValidation(t *testing.T) {
	t.Parallel()

	finding := findings.Finding{
		RuleID:      "static/test-rule",
		FindingID:   "provider-controlled-value",
		File:        "main.go",
		StartLine:   1,
		EndLine:     1,
		Severity:    findings.SeverityLow,
		Category:    findings.CategoryQuality,
		Title:       "Test finding",
		Message:     "A test finding.",
		ProposedFix: &findings.ProposedFix{Description: "Replace the line.", StartLine: 1, EndLine: 1, Replacement: "replacement"},
		Confidence:  0.8,
		Source:      findings.SourceStaticAnalysis,
	}
	service := New(pathfilter.New(nil), staticAnalyzer{findings: []findings.Finding{finding}})
	changes := change.ChangeSet{Files: []change.FileChange{{NewPath: "main.go", Status: change.StatusAdded}}}
	result, err := service.ReviewChanges(context.Background(), changes)
	if err != nil {
		t.Fatalf("ReviewChanges() error = %v", err)
	}
	if len(result.Findings) != 1 || !strings.HasPrefix(result.Findings[0].FindingID, "sha256:") || result.Findings[0].FindingID == finding.FindingID {
		t.Fatalf("finding was not finalized: %#v", result.Findings)
	}
	if result.SchemaVersion != 3 {
		t.Fatalf("SchemaVersion = %d, want 3", result.SchemaVersion)
	}
	result.Findings[0].ProposedFix.Replacement = "mutated"
	if finding.ProposedFix.Replacement == "mutated" {
		t.Fatal("review result aliases analyzer proposed fix")
	}
}

func TestReviewChangesHonorsCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := New(pathfilter.New(nil)).ReviewChanges(ctx, change.ChangeSet{})
	if err != context.Canceled {
		t.Fatalf("ReviewChanges() error = %v, want context.Canceled", err)
	}
}

type staticAnalyzer struct {
	findings []findings.Finding
	err      error
}

var _ Analyzer = staticAnalyzer{}

func (staticAnalyzer) Name() string {
	return "static"
}

func (a staticAnalyzer) Analyze(context.Context, change.ChangeSet) ([]findings.Finding, error) {
	return a.findings, a.err
}
