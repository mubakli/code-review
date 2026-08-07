package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"code-review/internal/ai"
	"code-review/internal/ai/providers/mock"
	"code-review/internal/config"
	"code-review/internal/findings"
	"code-review/internal/review"
)

func TestRunValidatesReviewFlags(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		arguments []string
		wantCode  int
		wantError string
	}{
		{name: "no command", wantCode: 2, wantError: "Usage:"},
		{name: "unknown command", arguments: []string{"unknown"}, wantCode: 2, wantError: "unknown command"},
		{name: "staged required", arguments: []string{"review"}, wantCode: 2, wantError: "requires --staged"},
		{name: "format validated", arguments: []string{"review", "--staged", "--format", "xml"}, wantCode: 2, wantError: "unsupported output format"},
		{name: "empty exclude", arguments: []string{"review", "--staged", "--exclude", ""}, wantCode: 2, wantError: "exclusion pattern cannot be empty"},
		{name: "malformed exclude", arguments: []string{"review", "--staged", "--exclude", "private/["}, wantCode: 2, wantError: "invalid exclusion pattern"},
		{name: "unsupported AI provider", arguments: []string{"review", "--staged", "--ai-provider", "unknown", "--ai-model", "model"}, wantCode: 2, wantError: "unsupported AI provider"},
		{name: "missing AI model", arguments: []string{"review", "--staged", "--ai-provider", "openai"}, wantCode: 2, wantError: "AI model is required"},
		{name: "model without provider", arguments: []string{"review", "--staged", "--ai-model", "model"}, wantCode: 2, wantError: "AI model requires"},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			code := run(context.Background(), test.arguments, &stdout, &stderr, ".")
			if code != test.wantCode {
				t.Errorf("run() code = %d, want %d", code, test.wantCode)
			}
			if !strings.Contains(stderr.String(), test.wantError) {
				t.Errorf("stderr does not contain %q:\n%s", test.wantError, stderr.String())
			}
		})
	}
}

func TestRunReviewJSON(t *testing.T) {
	requireGit(t)

	repository := t.TempDir()
	runTestGit(t, repository, "init", "--quiet")
	writeSource(t, repository, "config.go", "package sample\n\nconst apiKey = \"actual-secret-value-123\"\n")
	runTestGit(t, repository, "add", "--", "config.go")

	var stdout, stderr bytes.Buffer
	exitCode := run(
		context.Background(),
		[]string{"review", "--staged", "--format", "json"},
		&stdout,
		&stderr,
		repository,
	)
	if exitCode != 0 {
		t.Fatalf("run() exit code = %d, want 0; stderr = %s", exitCode, stderr.String())
	}

	var result review.Result
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("decode JSON result: %v\n%s", err, stdout.String())
	}
	if result.Summary.FilesReviewed != 1 || result.Summary.FindingCount != 1 {
		t.Fatalf("result summary = %#v", result.Summary)
	}
	if !strings.HasPrefix(result.ReviewID, "sha256:") || len(result.Files) != 1 || result.Files[0].Path != "config.go" {
		t.Fatalf("review identity/files = %q, %#v", result.ReviewID, result.Files)
	}
	if result.AI != nil {
		t.Fatalf("AI metadata is present when AI is disabled: %#v", result.AI)
	}
	if result.Findings[0].File != "config.go" || result.Findings[0].StartLine != 3 {
		t.Errorf("finding = %#v", result.Findings[0])
	}
}

func TestRunSnapshotAndExpectedReviewID(t *testing.T) {
	requireGit(t)

	repository := t.TempDir()
	runTestGit(t, repository, "init", "--quiet")
	writeSource(t, repository, "main.go", "package main\n")
	runTestGit(t, repository, "add", "--", "main.go")

	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), []string{"snapshot", "--staged"}, &stdout, &stderr, repository); code != 0 {
		t.Fatalf("snapshot exit code = %d; stderr = %s", code, stderr.String())
	}
	var snapshot struct {
		SchemaVersion int    `json:"schemaVersion"`
		ReviewID      string `json:"reviewId"`
		FilesChanged  int    `json:"filesChanged"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &snapshot); err != nil {
		t.Fatalf("decode snapshot: %v", err)
	}
	if snapshot.SchemaVersion != review.SchemaVersion || !strings.HasPrefix(snapshot.ReviewID, "sha256:") || snapshot.FilesChanged != 1 {
		t.Fatalf("snapshot = %#v", snapshot)
	}

	writeSource(t, repository, "main.go", "package main\n\nconst changed = true\n")
	runTestGit(t, repository, "add", "--", "main.go")
	stdout.Reset()
	stderr.Reset()
	code := run(
		context.Background(),
		[]string{"review", "--staged", "--format", "json", "--expected-review-id", snapshot.ReviewID},
		&stdout,
		&stderr,
		repository,
	)
	if code != 1 || !strings.Contains(stderr.String(), "staged snapshot changed") {
		t.Fatalf("stale review code = %d, stderr = %s", code, stderr.String())
	}
}

func TestRunReviewSupportsCustomExclusion(t *testing.T) {
	requireGit(t)

	repository := t.TempDir()
	runTestGit(t, repository, "init", "--quiet")
	writeSource(t, repository, "private/config.go", "package private\n\nconst password = \"actual-secret-value-123\"\n")
	runTestGit(t, repository, "add", "--", "private/config.go")

	var stdout, stderr bytes.Buffer
	exitCode := run(
		context.Background(),
		[]string{"review", "--staged", "--format", "json", "--exclude", "private/"},
		&stdout,
		&stderr,
		repository,
	)
	if exitCode != 0 {
		t.Fatalf("run() exit code = %d, want 0; stderr = %s", exitCode, stderr.String())
	}
	var result review.Result
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("decode JSON result: %v", err)
	}
	if result.Summary.FilesSkipped != 1 || result.Summary.FindingCount != 0 {
		t.Fatalf("result summary = %#v", result.Summary)
	}
}

func TestRunReviewAnalyzesEnvironmentFilesLocally(t *testing.T) {
	requireGit(t)

	repository := t.TempDir()
	runTestGit(t, repository, "init", "--quiet")
	writeSource(t, repository, ".env", "OPENAI_API_KEY=sk-actual-provider-secret-value\nDATABASE_PASSWORD=correct-horse-battery\n")
	runTestGit(t, repository, "add", "-f", "--", ".env")

	var stdout, stderr bytes.Buffer
	exitCode := run(
		context.Background(),
		[]string{"review", "--staged", "--format", "json"},
		&stdout,
		&stderr,
		repository,
	)
	if exitCode != 0 {
		t.Fatalf("run() exit code = %d, want 0; stderr = %s", exitCode, stderr.String())
	}

	var result review.Result
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("decode JSON result: %v", err)
	}
	if result.Summary.FilesReviewed != 1 || result.Summary.FindingCount != 2 {
		t.Fatalf("result summary = %#v", result.Summary)
	}
	for _, finding := range result.Findings {
		if finding.File != ".env" || finding.Source != findings.SourceLocalRule {
			t.Errorf("finding = %#v", finding)
		}
	}
	if strings.Contains(stdout.String(), "correct-horse-battery") || strings.Contains(stdout.String(), "sk-actual-provider-secret-value") {
		t.Fatal("JSON output exposed a detected environment secret")
	}
}

func TestRunReviewIgnoresEnvironmentExamplePlaceholders(t *testing.T) {
	requireGit(t)

	repository := t.TempDir()
	runTestGit(t, repository, "init", "--quiet")
	writeSource(t, repository, ".env.example", "OPENAI_API_KEY=your-api-key\nDATABASE_PASSWORD=${DATABASE_PASSWORD}\nAUTH_TOKEN=<auth-token>\n")
	runTestGit(t, repository, "add", "--", ".env.example")

	var stdout, stderr bytes.Buffer
	exitCode := run(context.Background(), []string{"review", "--staged", "--format", "json"}, &stdout, &stderr, repository)
	if exitCode != 0 {
		t.Fatalf("run() exit code = %d, want 0; stderr = %s", exitCode, stderr.String())
	}
	var result review.Result
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("decode JSON result: %v", err)
	}
	if result.Summary.FilesReviewed != 1 || result.Summary.FindingCount != 0 {
		t.Fatalf("result summary = %#v", result.Summary)
	}
}

func TestRunReviewOutsideRepository(t *testing.T) {
	requireGit(t)

	var stdout, stderr bytes.Buffer
	exitCode := run(
		context.Background(),
		[]string{"review", "--staged"},
		&stdout,
		&stderr,
		t.TempDir(),
	)
	if exitCode != 1 {
		t.Fatalf("run() exit code = %d, want 1", exitCode)
	}
	if !strings.Contains(stderr.String(), "not a Git repository") {
		t.Errorf("stderr = %q, want repository error", stderr.String())
	}
}

func TestRunAIReviewRequiresRuntimeKey(t *testing.T) {
	requireGit(t)

	repository := t.TempDir()
	runTestGit(t, repository, "init", "--quiet")
	writeSource(t, repository, "main.go", "package main\n")
	runTestGit(t, repository, "add", "--", "main.go")
	t.Setenv(openAIAPIKeyEnvironment, "")

	var stdout, stderr bytes.Buffer
	exitCode := run(
		context.Background(),
		[]string{"review", "--staged", "--ai-provider", "openai", "--ai-model", "review-model"},
		&stdout,
		&stderr,
		repository,
	)
	if exitCode != 1 {
		t.Fatalf("run() exit code = %d, want 1", exitCode)
	}
	if !strings.Contains(stderr.String(), openAIAPIKeyEnvironment) {
		t.Fatalf("stderr does not identify required key environment: %s", stderr.String())
	}
}

func TestConfiguredProviderBuildsOpenAIAdapter(t *testing.T) {
	t.Setenv(openAIAPIKeyEnvironment, "test-key")
	provider, err := configuredProvider(config.AI{
		Provider:        config.AIProviderOpenAI,
		Model:           "review-model",
		MaxOutputTokens: 1000,
	})
	if err != nil || provider == nil {
		t.Fatalf("configuredProvider() = %#v, %v", provider, err)
	}
}

func TestReviewStagedRunsConfiguredAIProvider(t *testing.T) {
	requireGit(t)

	repository := t.TempDir()
	runTestGit(t, repository, "init", "--quiet")
	writeSource(t, repository, "main.go", "package main\n\nfunc run() {}\n")
	runTestGit(t, repository, "add", "--", "main.go")
	provider := mock.Provider{AnalyzeFunc: func(_ context.Context, _ ai.AnalysisRequest) (*ai.AnalysisResponse, error) {
		return &ai.AnalysisResponse{
			Status: ai.ResponseStatusComplete,
			Findings: []ai.ResponseFinding{{
				File:       "main.go",
				StartLine:  3,
				EndLine:    3,
				Severity:   findings.SeverityLow,
				Category:   findings.CategoryQuality,
				Title:      "Mock AI finding",
				Message:    "The mock provider reviewed this line.",
				Suggestion: "No action required.",
				Confidence: 0.75,
			}},
		}, nil
	}}

	result, err := reviewStaged(context.Background(), repository, reviewOptions{
		AI: config.AI{
			Provider:        config.AIProviderOpenAI,
			Model:           "review-model",
			MaxOutputTokens: 1000,
		},
		Provider: provider,
	})
	if err != nil {
		t.Fatalf("reviewStaged() error = %v", err)
	}
	if result.AI == nil || result.AI.SuccessfulBatches != 1 || result.AI.FailedBatches != 0 {
		t.Fatalf("AI summary = %#v", result.AI)
	}
	if len(result.Findings) != 1 || result.Findings[0].Source != findings.SourceAI {
		t.Fatalf("findings = %#v", result.Findings)
	}
}

func TestReviewStagedNeverSendsEnvironmentFilesToAI(t *testing.T) {
	requireGit(t)

	repository := t.TempDir()
	runTestGit(t, repository, "init", "--quiet")
	writeSource(t, repository, ".env", "DATABASE_PASSWORD=correct-horse-battery\n")
	writeSource(t, repository, "main.go", "package main\n\nfunc run() {}\n")
	runTestGit(t, repository, "add", "-f", "--", ".env", "main.go")

	providerCalls := 0
	provider := mock.Provider{AnalyzeFunc: func(_ context.Context, request ai.AnalysisRequest) (*ai.AnalysisResponse, error) {
		providerCalls++
		if strings.Contains(request.Diff(), ".env") || strings.Contains(request.Diff(), "correct-horse-battery") {
			t.Fatalf("provider received environment file content:\n%s", request.Diff())
		}
		if !strings.Contains(request.Diff(), "main.go") {
			t.Fatalf("provider did not receive reviewable source diff:\n%s", request.Diff())
		}
		if len(request.StaticFindings()) != 0 {
			t.Fatalf("provider received environment-file findings: %#v", request.StaticFindings())
		}
		return &ai.AnalysisResponse{Status: ai.ResponseStatusComplete}, nil
	}}

	result, err := reviewStaged(context.Background(), repository, reviewOptions{
		AI: config.AI{
			Provider:        config.AIProviderOpenAI,
			Model:           "review-model",
			MaxOutputTokens: 1000,
		},
		Provider: provider,
	})
	if err != nil {
		t.Fatalf("reviewStaged() error = %v", err)
	}
	if providerCalls != 1 || result.AI == nil || result.AI.SuccessfulBatches != 1 {
		t.Fatalf("provider calls = %d, AI summary = %#v", providerCalls, result.AI)
	}
	if len(result.Findings) != 1 || result.Findings[0].File != ".env" || result.Findings[0].Source != findings.SourceLocalRule {
		t.Fatalf("local environment finding was not preserved: %#v", result.Findings)
	}
}

func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
}

func runTestGit(t *testing.T, directory string, args ...string) {
	t.Helper()
	commandArgs := append([]string{"-C", directory}, args...)
	command := exec.Command("git", commandArgs...)
	command.Env = append(os.Environ(), "LC_ALL=C")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
}

func writeSource(t *testing.T, root, name, content string) {
	t.Helper()
	file := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatalf("create fixture directory: %v", err)
	}
	if err := os.WriteFile(file, []byte(content), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
}
