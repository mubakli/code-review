package secrets

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"code-review/internal/change"
	"code-review/internal/findings"
)

func TestSecretAnalyzerChecksOnlyAddedLinesWithoutLeakingValues(t *testing.T) {
	t.Parallel()

	providerCredential := "AKIA" + strings.Repeat("A", 16)
	changes := change.ChangeSet{Files: []change.FileChange{{
		NewPath: "config.go",
		Status:  change.StatusModified,
		Hunks: []change.Hunk{{Lines: []change.Line{
			{Kind: change.LineAdded, NewLine: 4, Content: `const apiKey = "` + providerCredential + `"`},
			{Kind: change.LineAdded, NewLine: 5, Content: `password: "correct-horse-battery"`},
			{Kind: change.LineAdded, NewLine: 6, Content: `"client_secret": "json-secret-value-123"`},
			{Kind: change.LineAdded, NewLine: 7, Content: `const token = os.Getenv("TOKEN")`},
			{Kind: change.LineAdded, NewLine: 8, Content: `secret: "example-placeholder"`},
			{Kind: change.LineDeleted, OldLine: 8, Content: `password: "deleted-secret"`},
			{Kind: change.LineContext, OldLine: 9, NewLine: 9, Content: `password: "existing-secret"`},
		}}},
	}}}

	values, err := (Analyzer{}).Analyze(context.Background(), changes)
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	if len(values) != 3 {
		t.Fatalf("len(findings) = %d, want 3: %#v", len(values), values)
	}
	if values[0].Severity != findings.SeverityHigh || values[0].StartLine != 4 {
		t.Errorf("provider finding = %#v", values[0])
	}
	wantRules := []string{"secrets/provider-credential", "secrets/hardcoded-secret", "secrets/hardcoded-secret"}
	for index, finding := range values {
		if finding.RuleID != wantRules[index] || finding.FindingID != "" || finding.ProposedFix != nil {
			t.Errorf("finding %d identity/fix = %#v", index, finding)
		}
	}
	if values[1].Severity != findings.SeverityMedium || values[1].StartLine != 5 {
		t.Errorf("literal finding = %#v", values[1])
	}
	if values[2].Severity != findings.SeverityMedium || values[2].StartLine != 6 {
		t.Errorf("JSON finding = %#v", values[2])
	}

	encoded, err := json.Marshal(values)
	if err != nil {
		t.Fatalf("marshal findings: %v", err)
	}
	if strings.Contains(string(encoded), providerCredential) || strings.Contains(string(encoded), "correct-horse-battery") || strings.Contains(string(encoded), "json-secret-value-123") {
		t.Fatal("finding output leaked a detected credential")
	}
}

func TestDetectSecretClassifiesPrivateKeys(t *testing.T) {
	t.Parallel()

	for _, header := range []string{"-----BEGIN PRIVATE KEY-----", "-----BEGIN ENCRYPTED PRIVATE KEY-----"} {
		match, detected := detectSecret(header)
		if !detected || match.severity != findings.SeverityCritical || match.ruleID != "secrets/private-key" {
			t.Errorf("detectSecret(%q) = %#v, %v", header, match, detected)
		}
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

func TestDetectSecretEnvironmentAssignments(t *testing.T) {
	t.Parallel()

	detected := []string{
		"DATABASE_PASSWORD=correct-horse-battery",
		"AUTH_TOKEN=admin123",
		"OPENAI_API_KEY=sk-actual-provider-secret-value",
	}
	for _, line := range detected {
		if _, ok := detectSecret(line); !ok {
			t.Errorf("detectSecret(%q) = false", line)
		}
	}
	ignored := []string{
		"DATABASE_PASSWORD=${DATABASE_PASSWORD}",
		"DATABASE_PASSWORD=<database-password>",
		"OPENAI_API_KEY=your-api-key",
		"AUTH_TOKEN=placeholder",
		"AUTH_TOKEN=replace_with_token",
		"PASSWORD=test",
	}
	for _, line := range ignored {
		if _, ok := detectSecret(line); ok {
			t.Errorf("detectSecret(%q) = true", line)
		}
	}
}

func TestSecretAnalyzerHonorsCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := (Analyzer{}).Analyze(ctx, change.ChangeSet{Files: []change.FileChange{{
		NewPath: "config.go",
		Hunks:   []change.Hunk{{Lines: []change.Line{{Kind: change.LineAdded, NewLine: 1, Content: `password = "real-secret"`}}}},
	}}})
	if err != context.Canceled {
		t.Fatalf("Analyze() error = %v, want context.Canceled", err)
	}
}
