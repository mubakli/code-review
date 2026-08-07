package prompt

import (
	"context"
	"strings"
	"testing"

	"code-review/internal/findings"
	"code-review/internal/gitdiff"
	"code-review/internal/pathfilter"
	"code-review/internal/redact"
)

func TestBuilderCreatesLanguageIndependentRedactedBatches(t *testing.T) {
	t.Parallel()

	budget := Budget{MaxInputTokens: 700, MaxDiffTokens: 180, MaxStaticFindingTokens: 200}
	builder, err := New(budget, pathfilter.New(pathfilter.DefaultPatterns()))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	secret := "actual-provider-secret-value"
	changes := gitdiff.ChangeSet{Files: []gitdiff.FileChange{
		changedFile("cmd/main.go", []string{"func main() {", `password := "go-secret-value"`, "}"}),
		changedFile("web/app.ts", []string{`const apiKey = "` + secret + `";`, "export const ready = true;"}),
		changedFile("worker/task.py", []string{`token = "python-secret-value"`, "run_task()"}),
		changedFile("node_modules/dependency.js", []string{`password = "excluded-secret"`}),
		{NewPath: "assets/logo.png", Status: gitdiff.StatusAdded, Binary: true},
	}}
	localFinding := validFinding("web/app.ts", "Potential credential", "A local rule found a credential.")

	batches, err := builder.Build(context.Background(), changes, []findings.Finding{localFinding})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if len(batches) == 0 {
		t.Fatal("Build() returned no batches")
	}

	seenFiles := make(map[string]bool)
	seenFinding := false
	seenRedaction := false
	for _, batch := range batches {
		if batch.EstimatedTokens > budget.MaxInputTokens {
			t.Errorf("EstimatedTokens = %d, limit %d", batch.EstimatedTokens, budget.MaxInputTokens)
		}
		if batch.DiffTokens > builder.diffTokenLimit {
			t.Errorf("DiffTokens = %d, limit %d", batch.DiffTokens, builder.diffTokenLimit)
		}
		if batch.Request.Instructions() != ReviewInstructions {
			t.Error("batch uses unexpected review instructions")
		}
		if strings.Contains(batch.Request.Diff(), secret) || strings.Contains(batch.Request.Diff(), "go-secret-value") || strings.Contains(batch.Request.Diff(), "python-secret-value") {
			t.Fatalf("batch contains a raw secret:\n%s", batch.Request.Diff())
		}
		if strings.Contains(batch.Request.Diff(), redact.Placeholder) {
			seenRedaction = true
		}
		if strings.Contains(batch.Request.Diff(), "node_modules") || strings.Contains(batch.Request.Diff(), "logo.png") {
			t.Fatalf("batch contains an excluded or binary file:\n%s", batch.Request.Diff())
		}
		for _, file := range batch.Files {
			seenFiles[file] = true
		}
		if len(batch.Request.StaticFindings()) > 0 {
			seenFinding = true
			if batch.Request.StaticFindings()[0].File != "web/app.ts" {
				t.Fatalf("batch contains unrelated finding: %#v", batch.Request.StaticFindings())
			}
		}
	}
	for _, file := range []string{"cmd/main.go", "web/app.ts", "worker/task.py"} {
		if !seenFiles[file] {
			t.Errorf("file %q was not batched", file)
		}
	}
	if !seenFinding {
		t.Fatal("relevant static finding was not included")
	}
	if !seenRedaction {
		t.Fatal("no batch reported redacted content")
	}
}

func TestBuilderSplitsLargeDiffByFileAndHunk(t *testing.T) {
	t.Parallel()

	budget := Budget{MaxInputTokens: 400, MaxDiffTokens: 80, MaxStaticFindingTokens: 0}
	builder, err := New(budget, pathMatcherForTest())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	lines := make([]string, 0, 30)
	for index := 0; index < 30; index++ {
		lines = append(lines, "changed line with useful context")
	}
	changes := gitdiff.ChangeSet{Files: []gitdiff.FileChange{changedFile("src/service.ts", lines)}}

	batches, err := builder.Build(context.Background(), changes, nil)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if len(batches) < 2 {
		t.Fatalf("len(batches) = %d, want multiple batches", len(batches))
	}
	for _, batch := range batches {
		if batch.DiffTokens > builder.diffTokenLimit || batch.EstimatedTokens > budget.MaxInputTokens {
			t.Errorf("batch exceeds budget: %#v", batch)
		}
		if len(batch.Files) != 1 || batch.Files[0] != "src/service.ts" {
			t.Errorf("batch files = %#v", batch.Files)
		}
	}
}

func TestBuilderMarksLongLineTruncation(t *testing.T) {
	t.Parallel()

	budget := Budget{MaxInputTokens: 400, MaxDiffTokens: 80, MaxStaticFindingTokens: 0}
	builder, err := New(budget, pathMatcherForTest())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	longLine := strings.Repeat("x", 2000)
	changes := gitdiff.ChangeSet{Files: []gitdiff.FileChange{changedFile("dist-like-but-reviewed.js", []string{longLine})}}

	batches, err := builder.Build(context.Background(), changes, nil)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if len(batches) != 1 || !batches[0].Truncated {
		t.Fatalf("batches = %#v, want one truncated batch", batches)
	}
	if !strings.Contains(batches[0].Request.Diff(), "long diff line truncated") {
		t.Fatalf("truncation marker is missing:\n%s", batches[0].Request.Diff())
	}
	if batches[0].DiffTokens > builder.diffTokenLimit {
		t.Fatalf("DiffTokens = %d, limit %d", batches[0].DiffTokens, builder.diffTokenLimit)
	}
}

func TestBuilderBudgetsStaticFindings(t *testing.T) {
	t.Parallel()

	first := validFinding("main.go", "First", "Short message")
	second := validFinding("main.go", "Second", strings.Repeat("long message ", 30))
	staticLimit := estimateFindings([]findings.Finding{first})
	budget := Budget{MaxInputTokens: 700, MaxDiffTokens: 120, MaxStaticFindingTokens: staticLimit}
	builder, err := New(budget, pathMatcherForTest())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	changes := gitdiff.ChangeSet{Files: []gitdiff.FileChange{changedFile("main.go", []string{"changed"})}}

	batches, err := builder.Build(context.Background(), changes, []findings.Finding{first, second})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if len(batches) != 1 {
		t.Fatalf("len(batches) = %d, want 1", len(batches))
	}
	if got := len(batches[0].Request.StaticFindings()); got != 1 {
		t.Fatalf("len(StaticFindings) = %d, want 1", got)
	}
	if batches[0].OmittedFindings != 1 {
		t.Fatalf("OmittedFindings = %d, want 1", batches[0].OmittedFindings)
	}
}

func TestBuilderHonorsCancellation(t *testing.T) {
	t.Parallel()

	builder, err := New(DefaultBudget(), pathMatcherForTest())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = builder.Build(ctx, gitdiff.ChangeSet{}, nil)
	if err != context.Canceled {
		t.Fatalf("Build() error = %v, want context.Canceled", err)
	}
}

func changedFile(path string, additions []string) gitdiff.FileChange {
	lines := make([]gitdiff.Line, 0, len(additions))
	for index, content := range additions {
		lines = append(lines, gitdiff.Line{Kind: gitdiff.LineAdded, NewLine: index + 1, Content: content})
	}
	return gitdiff.FileChange{
		NewPath: path,
		Status:  gitdiff.StatusAdded,
		Hunks: []gitdiff.Hunk{{
			OldStart: 0,
			OldLines: 0,
			NewStart: 1,
			NewLines: len(lines),
			Lines:    lines,
		}},
	}
}

func validFinding(file, title, message string) findings.Finding {
	return findings.Finding{
		File:       file,
		StartLine:  1,
		EndLine:    1,
		Severity:   findings.SeverityHigh,
		Category:   findings.CategorySecurity,
		Title:      title,
		Message:    message,
		Confidence: 0.9,
		Source:     findings.SourceLocalRule,
	}
}

func pathMatcherForTest() pathfilter.Matcher {
	return pathfilter.New(nil)
}
