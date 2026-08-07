package review

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"code-review/internal/analyzer"
	"code-review/internal/findings"
	"code-review/internal/gitdiff"
	"code-review/internal/pathfilter"
)

func TestReviewStagedEndToEnd(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}

	root := t.TempDir()
	runGit(t, root, "init", "--quiet")
	writeFile(t, filepath.Join(root, "config.go"), "package config\n\nconst apiKey = \"sk-livecredential123456\"\n")
	writeFile(t, filepath.Join(root, "node_modules", "dependency.js"), "const password = \"dependency-secret-value\";\n")
	runGit(t, root, "add", "--", ".")

	result, err := NewDefault().ReviewStaged(context.Background(), root)
	if err != nil {
		t.Fatalf("ReviewStaged() error = %v", err)
	}
	if result.SchemaVersion != SchemaVersion {
		t.Errorf("SchemaVersion = %d", result.SchemaVersion)
	}
	if result.Summary.FilesChanged != 2 || result.Summary.FilesReviewed != 1 || result.Summary.FilesSkipped != 1 {
		t.Errorf("Summary = %#v", result.Summary)
	}
	if result.Summary.HunksReviewed != 1 || result.Summary.AddedLines != 3 || result.Summary.DeletedLines != 0 {
		t.Errorf("Summary change counts = %#v", result.Summary)
	}
	if len(result.Findings) != 1 || result.Summary.FindingCount != 1 {
		t.Fatalf("Findings = %#v, summary = %#v", result.Findings, result.Summary)
	}
	if result.Findings[0].File != "config.go" || result.Findings[0].StartLine != 3 {
		t.Errorf("Finding = %#v", result.Findings[0])
	}
}

func TestReviewChangesFiltersExcludedBinaryAndPathlessFiles(t *testing.T) {
	t.Parallel()

	changes := gitdiff.ChangeSet{Files: []gitdiff.FileChange{
		{
			OldPath: "main.go",
			NewPath: "main.go",
			Status:  gitdiff.StatusModified,
			Hunks: []gitdiff.Hunk{{Lines: []gitdiff.Line{
				{Kind: gitdiff.LineDeleted, OldLine: 3, Content: "old"},
				{Kind: gitdiff.LineAdded, NewLine: 3, Content: "new"},
			}}},
		},
		{NewPath: ".env", Status: gitdiff.StatusAdded},
		{NewPath: "logo.png", Status: gitdiff.StatusAdded, Binary: true},
		{Status: gitdiff.StatusModified},
	}}
	service := New(pathfilter.New(pathfilter.DefaultPatterns()))
	result, err := service.ReviewChanges(context.Background(), changes)
	if err != nil {
		t.Fatalf("ReviewChanges() error = %v", err)
	}
	if result.Summary.FilesChanged != 4 || result.Summary.FilesReviewed != 1 || result.Summary.FilesSkipped != 3 {
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
	_, err := service.ReviewChanges(context.Background(), gitdiff.ChangeSet{})
	if err == nil || !strings.Contains(err.Error(), "finding 1 is invalid") {
		t.Fatalf("ReviewChanges() error = %v", err)
	}
}

func TestReviewChangesHonorsCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := New(pathfilter.New(nil)).ReviewChanges(ctx, gitdiff.ChangeSet{})
	if err != context.Canceled {
		t.Fatalf("ReviewChanges() error = %v, want context.Canceled", err)
	}
}

type staticAnalyzer struct {
	findings []findings.Finding
	err      error
}

var _ analyzer.Analyzer = staticAnalyzer{}

func (staticAnalyzer) Name() string {
	return "static"
}

func (a staticAnalyzer) Analyze(context.Context, gitdiff.ChangeSet) ([]findings.Finding, error) {
	return a.findings, a.err
}

func runGit(t *testing.T, root string, arguments ...string) {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", root}, arguments...)...)
	command.Env = append(os.Environ(), "LC_ALL=C")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(arguments, " "), err, output)
	}
}

func writeFile(t *testing.T, filePath, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(filePath), 0o755); err != nil {
		t.Fatalf("create fixture directory: %v", err)
	}
	if err := os.WriteFile(filePath, []byte(content), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
}
