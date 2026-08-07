package analyzer

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"code-review/internal/findings"
	"code-review/internal/gitdiff"
)

func TestSecretAnalyzerChecksOnlyAddedLinesWithoutLeakingValues(t *testing.T) {
	t.Parallel()

	providerCredential := "AKIA" + strings.Repeat("A", 16)
	changes := gitdiff.ChangeSet{Files: []gitdiff.FileChange{{
		NewPath: "config.go",
		Status:  gitdiff.StatusModified,
		Hunks: []gitdiff.Hunk{{Lines: []gitdiff.Line{
			{Kind: gitdiff.LineAdded, NewLine: 4, Content: `const apiKey = "` + providerCredential + `"`},
			{Kind: gitdiff.LineAdded, NewLine: 5, Content: `password: "correct-horse-battery"`},
			{Kind: gitdiff.LineAdded, NewLine: 6, Content: `const token = os.Getenv("TOKEN")`},
			{Kind: gitdiff.LineAdded, NewLine: 7, Content: `secret: "example-placeholder"`},
			{Kind: gitdiff.LineDeleted, OldLine: 8, Content: `password: "deleted-secret"`},
			{Kind: gitdiff.LineContext, OldLine: 9, NewLine: 8, Content: `password: "existing-secret"`},
		}}},
	}}}

	values, err := (SecretAnalyzer{}).Analyze(context.Background(), changes)
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	if len(values) != 2 {
		t.Fatalf("len(findings) = %d, want 2: %#v", len(values), values)
	}
	if values[0].Severity != findings.SeverityHigh || values[0].StartLine != 4 {
		t.Errorf("provider finding = %#v", values[0])
	}
	if values[1].Severity != findings.SeverityMedium || values[1].StartLine != 5 {
		t.Errorf("literal finding = %#v", values[1])
	}

	encoded, err := json.Marshal(values)
	if err != nil {
		t.Fatalf("marshal findings: %v", err)
	}
	if strings.Contains(string(encoded), providerCredential) || strings.Contains(string(encoded), "correct-horse-battery") {
		t.Fatal("finding output leaked a detected credential")
	}
}

func TestDetectSecretClassifiesPrivateKey(t *testing.T) {
	t.Parallel()

	match, detected := detectSecret("-----BEGIN PRIVATE KEY-----")
	if !detected || match.severity != findings.SeverityCritical {
		t.Fatalf("detectSecret() = %#v, %v", match, detected)
	}
}

func TestDetectSecretIgnoresPlaceholders(t *testing.T) {
	t.Parallel()

	for _, line := range []string{
		`const apiKey = "your-api-key"`,
		`password: "[REDACTED]"`,
		`const secret = "not-a-secret"`,
	} {
		if _, detected := detectSecret(line); detected {
			t.Errorf("detectSecret(%q) = true", line)
		}
	}
}

func TestSecretAnalyzerHonorsCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := (SecretAnalyzer{}).Analyze(ctx, gitdiff.ChangeSet{Files: []gitdiff.FileChange{{
		NewPath: "config.go",
		Hunks:   []gitdiff.Hunk{{Lines: []gitdiff.Line{{Kind: gitdiff.LineAdded, NewLine: 1, Content: `password = "real-secret"`}}}},
	}}})
	if err != context.Canceled {
		t.Fatalf("Analyze() error = %v, want context.Canceled", err)
	}
}
