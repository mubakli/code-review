package context

import (
	"reflect"
	stdcontext "context"
	"strings"
	"testing"

	"code-review/internal/change"
	"code-review/internal/findings"
	"code-review/internal/redact"
)

const testPrompt = "Review the staged diff for defects only."

func TestPreparerCreatesLanguageIndependentRedactedBatches(t *testing.T) {
	t.Parallel()

	budget := Budget{MaxInputTokens: 700, MaxDiffTokens: 180, MaxStaticFindingTokens: 200}
	preparer, err := NewPreparer(budget)
	if err != nil {
		t.Fatalf("NewPreparer() error = %v", err)
	}
	secret := "actual-provider-secret-value"
	changes := change.ChangeSet{Files: []change.FileChange{
		changedFile("cmd/main.go", []string{"func main() {", `password := "go-secret-value"`, "}"}),
		changedFile("web/app.ts", []string{`const apiKey = "` + secret + `";`, "export const ready = true;"}),
		changedFile("worker/task.py", []string{`token = "python-secret-value"`, "run_task()"}),
		{NewPath: "assets/logo.png", Status: change.StatusAdded, Binary: true},
	}}
	localFinding := validFinding("web/app.ts", "Potential credential", "A local rule found a credential.")
	diffLimit, err := budget.DiffLimit(EstimateTokens(testPrompt))
	if err != nil {
		t.Fatalf("DiffLimit() error = %v", err)
	}

	prepared, err := preparer.Prepare(stdcontext.Background(), changes, []findings.Finding{localFinding}, nil, testPrompt)
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	if len(prepared.Batches) == 0 {
		t.Fatal("Prepare() returned no batches")
	}

	seenFiles := make(map[string]bool)
	seenFinding := false
	seenRedaction := false
	for _, batch := range prepared.Batches {
		if batch.EstimatedTokens > budget.MaxInputTokens {
			t.Errorf("EstimatedTokens = %d, limit %d", batch.EstimatedTokens, budget.MaxInputTokens)
		}
		if batch.DiffTokens > diffLimit {
			t.Errorf("DiffTokens = %d, limit %d", batch.DiffTokens, diffLimit)
		}
		if strings.Contains(batch.Diff, secret) || strings.Contains(batch.Diff, "go-secret-value") || strings.Contains(batch.Diff, "python-secret-value") {
			t.Fatalf("batch contains a raw secret:\n%s", batch.Diff)
		}
		if strings.Contains(batch.Diff, redact.Placeholder) {
			seenRedaction = true
		}
		if strings.Contains(batch.Diff, "logo.png") {
			t.Fatalf("batch contains a binary file:\n%s", batch.Diff)
		}
		for _, file := range batch.Files {
			seenFiles[file] = true
		}
		if len(batch.StaticFindings) > 0 {
			seenFinding = true
			if batch.StaticFindings[0].File != "web/app.ts" {
				t.Fatalf("batch contains unrelated finding: %#v", batch.StaticFindings)
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

func TestPreparerSplitsLargeDiffByFileAndHunk(t *testing.T) {
	t.Parallel()

	budget := Budget{MaxInputTokens: 400, MaxDiffTokens: 80, MaxStaticFindingTokens: 0}
	preparer, err := NewPreparer(budget)
	if err != nil {
		t.Fatalf("NewPreparer() error = %v", err)
	}
	lines := make([]string, 0, 30)
	for index := 0; index < 30; index++ {
		lines = append(lines, "changed line with useful context")
	}
	changes := change.ChangeSet{Files: []change.FileChange{changedFile("src/service.ts", lines)}}
	diffLimit, err := budget.DiffLimit(EstimateTokens(testPrompt))
	if err != nil {
		t.Fatalf("DiffLimit() error = %v", err)
	}

	prepared, err := preparer.Prepare(stdcontext.Background(), changes, nil, nil, testPrompt)
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	if len(prepared.Batches) < 2 {
		t.Fatalf("len(batches) = %d, want multiple batches", len(prepared.Batches))
	}
	for _, batch := range prepared.Batches {
		if batch.DiffTokens > diffLimit || batch.EstimatedTokens > budget.MaxInputTokens {
			t.Errorf("batch exceeds budget: %#v", batch)
		}
		if len(batch.Files) != 1 || batch.Files[0] != "src/service.ts" {
			t.Errorf("batch files = %#v", batch.Files)
		}
	}
}

func TestPreparerMarksLongLineTruncation(t *testing.T) {
	t.Parallel()

	budget := Budget{MaxInputTokens: 400, MaxDiffTokens: 80, MaxStaticFindingTokens: 0}
	preparer, err := NewPreparer(budget)
	if err != nil {
		t.Fatalf("NewPreparer() error = %v", err)
	}
	longLine := strings.Repeat("x", 2000)
	changes := change.ChangeSet{Files: []change.FileChange{changedFile("dist-like-but-reviewed.js", []string{longLine})}}
	diffLimit, err := budget.DiffLimit(EstimateTokens(testPrompt))
	if err != nil {
		t.Fatalf("DiffLimit() error = %v", err)
	}

	prepared, err := preparer.Prepare(stdcontext.Background(), changes, nil, nil, testPrompt)
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	if len(prepared.Batches) != 1 || !prepared.Batches[0].Truncated {
		t.Fatalf("batches = %#v, want one truncated batch", prepared.Batches)
	}
	if !strings.Contains(prepared.Batches[0].Diff, "long diff line truncated") {
		t.Fatalf("truncation marker is missing:\n%s", prepared.Batches[0].Diff)
	}
	if prepared.Batches[0].DiffTokens > diffLimit {
		t.Fatalf("DiffTokens = %d, limit %d", prepared.Batches[0].DiffTokens, diffLimit)
	}
}

func TestPreparerBudgetsStaticFindings(t *testing.T) {
	t.Parallel()

	first := validFinding("main.go", "First", "Short message")
	second := validFinding("main.go", "Second", strings.Repeat("long message ", 30))
	staticLimit := estimateFindings([]findings.Finding{first})
	budget := Budget{MaxInputTokens: 700, MaxDiffTokens: 120, MaxStaticFindingTokens: staticLimit}
	preparer, err := NewPreparer(budget)
	if err != nil {
		t.Fatalf("NewPreparer() error = %v", err)
	}
	changes := change.ChangeSet{Files: []change.FileChange{changedFile("main.go", []string{"changed"})}}

	prepared, err := preparer.Prepare(stdcontext.Background(), changes, []findings.Finding{first, second}, nil, testPrompt)
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	if len(prepared.Batches) != 1 {
		t.Fatalf("len(batches) = %d, want 1", len(prepared.Batches))
	}
	if got := len(prepared.Batches[0].StaticFindings); got != 1 {
		t.Fatalf("len(StaticFindings) = %d, want 1", got)
	}
	if prepared.Batches[0].OmittedFindings != 1 {
		t.Fatalf("OmittedFindings = %d, want 1", prepared.Batches[0].OmittedFindings)
	}
}

func TestSelectContextUsesRelatedTo(t *testing.T) {
	t.Parallel()

	fileSet := map[string]struct{}{"controllers/user.go": {}}
	contextFiles := []ContextFile{
		{Path: "middleware/auth.go", Content: "auth code", RelatedTo: []string{"controllers/user.go"}},
		{Path: "services/payment.go", Content: "payment code", RelatedTo: []string{"controllers/payment.go"}},
		{Path: "shared/types.go", Content: "shared code"},
	}
	selected := selectContext(fileSet, contextFiles)
	paths := make([]string, 0, len(selected))
	for _, file := range selected {
		paths = append(paths, file.Path)
	}
	want := []string{"middleware/auth.go", "shared/types.go"}
	if !reflect.DeepEqual(paths, want) {
		t.Fatalf("selectContext() = %#v, want %#v", paths, want)
	}
}

func TestSelectContextBudgetsTotalContentBytes(t *testing.T) {
	t.Parallel()

	files := map[string]struct{}{"app.go": {}}
	contextFiles := []ContextFile{
		{Path: "first.go", Content: strings.Repeat("a", MaxContextTotalBytes/2), RelatedTo: []string{"app.go"}},
		{Path: "second.go", Content: strings.Repeat("b", MaxContextTotalBytes), RelatedTo: []string{"app.go"}},
	}
	selected := selectContext(files, contextFiles)
	if len(selected) != 2 {
		t.Fatalf("len(selected) = %d, want both files included up to the budget", len(selected))
	}
	total := 0
	for _, file := range selected {
		total += len(file.Content)
	}
	if total > MaxContextTotalBytes+len(contextTruncatedMarker) {
		t.Fatalf("selected context totals %d bytes, budget %d", total, MaxContextTotalBytes)
	}
}

func TestPreparerAttachesRelatedContextToMatchingBatches(t *testing.T) {
	t.Parallel()

	// Two changed files are large enough to land in separate batches. The
	// related file points only at a.go, so it must be attached to the a.go
	// batch and must not be duplicated into the b.go batch.
	budget := Budget{MaxInputTokens: 400, MaxDiffTokens: 80, MaxStaticFindingTokens: 0}
	preparer, err := NewPreparer(budget)
	if err != nil {
		t.Fatalf("NewPreparer() error = %v", err)
	}
	lines := make([]string, 0, 15)
	for index := 0; index < 15; index++ {
		lines = append(lines, "changed line with useful context")
	}
	changes := change.ChangeSet{Files: []change.FileChange{
		changedFile("a.go", lines),
		changedFile("b.go", lines),
	}}
	related := []ContextFile{
		{Path: "shared/auth.go", Content: "func Authenticate() {}", RelatedTo: []string{"a.go"}},
	}

	prepared, err := preparer.Prepare(stdcontext.Background(), changes, nil, related, testPrompt)
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	seenA, seenB := false, false
	for _, batch := range prepared.Batches {
		if len(batch.Files) != 1 {
			t.Fatalf("batch files = %#v, want one file per batch", batch.Files)
		}
		if batch.Files[0] == "a.go" {
			seenA = true
			if len(batch.RelatedContext) != 1 || batch.RelatedContext[0].Path != "shared/auth.go" {
				t.Fatalf("a.go batch related context = %#v, want shared/auth.go", batch.RelatedContext)
			}
		}
		if batch.Files[0] == "b.go" {
			seenB = true
			if len(batch.RelatedContext) != 0 {
				t.Fatalf("b.go batch related context = %#v, want none", batch.RelatedContext)
			}
		}
	}
	if !seenA || !seenB {
		t.Fatalf("batches missing a.go or b.go: %#v", prepared.Batches)
	}
}

func TestPreparerHonorsCancellation(t *testing.T) {
	t.Parallel()

	preparer, err := NewPreparer(DefaultBudget())
	if err != nil {
		t.Fatalf("NewPreparer() error = %v", err)
	}
	ctx, cancel := stdcontext.WithCancel(stdcontext.Background())
	cancel()
	_, err = preparer.Prepare(ctx, change.ChangeSet{}, nil, nil, testPrompt)
	if err != stdcontext.Canceled {
		t.Fatalf("Prepare() error = %v, want context.Canceled", err)
	}
}

func changedFile(path string, additions []string) change.FileChange {
	lines := make([]change.Line, 0, len(additions))
	for index, content := range additions {
		lines = append(lines, change.Line{Kind: change.LineAdded, NewLine: index + 1, Content: content})
	}
	return change.FileChange{
		NewPath: path,
		Status:  change.StatusAdded,
		Hunks: []change.Hunk{{
			OldStart: 0,
			OldLines: 0,
			NewStart: 1,
			NewLines: len(lines),
			Lines:    lines,
		}},
	}
}

func validFinding(file, title, message string) findings.Finding {
	finding := findings.Finding{
		RuleID:     "test/static-rule",
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
	finding.FinalizeID()
	return finding
}
