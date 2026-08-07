package ai_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"code-review/internal/ai"
	"code-review/internal/ai/providers/mock"
	"code-review/internal/change"
	"code-review/internal/findings"
	"code-review/internal/redact"
)

func TestOrchestratorValidatesMergesAndDeduplicates(t *testing.T) {
	t.Parallel()

	builder := newBuilder(t, ai.DefaultBudget())
	secret := "actual-secret-value"
	changes := addedFile("service.go", []string{
		"package service",
		`const password = "` + secret + `"`,
		"result, _ := run()",
	})
	local := localFinding("service.go", 2, "Potential provider credential", "An added line matches a credential format.")
	providerCalls := 0
	provider := mock.Provider{AnalyzeFunc: func(_ context.Context, request ai.AnalysisRequest) (*ai.AnalysisResponse, error) {
		providerCalls++
		if strings.Contains(request.Diff(), secret) || !strings.Contains(request.Diff(), redact.Placeholder) {
			t.Fatalf("provider received unredacted diff:\n%s", request.Diff())
		}
		if len(request.StaticFindings()) != 1 {
			t.Fatalf("provider received %d static findings, want 1", len(request.StaticFindings()))
		}
		return &ai.AnalysisResponse{
			Status: ai.ResponseStatusComplete,
			Findings: []ai.ResponseFinding{
				{
					File:       "service.go",
					StartLine:  2,
					EndLine:    2,
					Severity:   findings.SeverityHigh,
					Category:   findings.CategorySecurity,
					Title:      "Hardcoded credential may be exposed",
					Message:    "The changed line contains a hardcoded credential.",
					Confidence: 0.96,
				},
				{
					File:       "service.go",
					StartLine:  3,
					EndLine:    3,
					Severity:   findings.SeverityMedium,
					Category:   findings.CategoryQuality,
					Title:      "Ignored error",
					Message:    "The returned error is discarded.",
					Suggestion: "Handle or explicitly justify the error.",
					Confidence: 0.91,
				},
			},
		}, nil
	}}
	orchestrator := newOrchestrator(t, builder, provider)

	result, err := orchestrator.Review(context.Background(), changes, []findings.Finding{local})
	if err != nil {
		t.Fatalf("Review() error = %v", err)
	}
	if providerCalls != 2 || result.BatchCount != 2 || result.SuccessfulBatches != 2 || len(result.Failures) != 0 {
		t.Fatalf("unexpected orchestration metadata: calls=%d result=%#v", providerCalls, result)
	}
	if len(result.Agents) != 2 || result.Agents[0] != string(ai.AgentCorrectness) || result.Agents[1] != string(ai.AgentSecurity) {
		t.Fatalf("Agents = %#v", result.Agents)
	}
	if len(result.ReviewedFiles) != 1 || result.ReviewedFiles[0] != "service.go" {
		t.Fatalf("ReviewedFiles = %#v", result.ReviewedFiles)
	}
	if len(result.Findings) != 2 {
		t.Fatalf("len(Findings) = %d, want 2: %#v", len(result.Findings), result.Findings)
	}

	credential := findByTitle(result.Findings, local.Title)
	if credential == nil {
		t.Fatalf("local credential finding was not preserved: %#v", result.Findings)
	}
	if credential.Source != findings.SourceLocalRule || credential.Severity != findings.SeverityHigh || credential.Confidence != 0.96 {
		t.Fatalf("duplicate was not consolidated into local finding: %#v", credential)
	}
	ignoredError := findByTitle(result.Findings, "Ignored error")
	if ignoredError == nil || ignoredError.Source != findings.SourceAI || ignoredError.AgentID != string(ai.AgentCorrectness) {
		t.Fatalf("AI finding was not retained with AI source: %#v", result.Findings)
	}
}

func TestOrchestratorRejectsInvalidResponsesWithoutLosingLocalFindings(t *testing.T) {
	t.Parallel()

	changes := addedFile("service.go", []string{"unchanged-looking context", "changed()"})
	local := localFinding("service.go", 2, "Local issue", "A deterministic local issue.")
	validCandidate := ai.ResponseFinding{
		File:       "service.go",
		StartLine:  2,
		EndLine:    2,
		Severity:   findings.SeverityMedium,
		Category:   findings.CategoryQuality,
		Title:      "AI issue",
		Message:    "A contextual issue.",
		Confidence: 0.8,
	}
	tests := []struct {
		name     string
		response *ai.AnalysisResponse
	}{
		{name: "nil response", response: nil},
		{name: "unknown status", response: &ai.AnalysisResponse{Status: "need_context"}},
		{name: "outside file", response: &ai.AnalysisResponse{Status: ai.ResponseStatusComplete, Findings: []ai.ResponseFinding{withFile(validCandidate, "other.go")}}},
		{name: "unchanged line", response: &ai.AnalysisResponse{Status: ai.ResponseStatusComplete, Findings: []ai.ResponseFinding{withLine(validCandidate, 99)}}},
		{name: "invalid severity", response: &ai.AnalysisResponse{Status: ai.ResponseStatusComplete, Findings: []ai.ResponseFinding{withSeverity(validCandidate, "unknown")}}},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			provider := mock.Provider{AnalyzeFunc: func(context.Context, ai.AnalysisRequest) (*ai.AnalysisResponse, error) {
				return test.response, nil
			}}
			orchestrator := newOrchestrator(t, newBuilder(t, ai.DefaultBudget()), provider)
			result, err := orchestrator.Review(context.Background(), changes, []findings.Finding{local})
			if err != nil {
				t.Fatalf("Review() error = %v", err)
			}
			if len(result.Findings) != 1 || result.Findings[0].Title != local.Title {
				t.Fatalf("local findings were not preserved: %#v", result.Findings)
			}
			if len(result.Failures) != 1 || result.SuccessfulBatches != 0 {
				t.Fatalf("invalid response was not recorded as a batch failure: %#v", result)
			}
		})
	}
}

func TestOrchestratorContinuesAfterProviderFailure(t *testing.T) {
	t.Parallel()

	budget := ai.Budget{MaxInputTokens: 400, MaxDiffTokens: 80, MaxStaticFindingTokens: 0}
	builder := newBuilder(t, budget)
	lines := make([]string, 30)
	for index := range lines {
		lines[index] = "changed line with enough content to split batches"
	}
	changes := addedFile("large.ts", lines)
	calls := 0
	provider := mock.Provider{AnalyzeFunc: func(context.Context, ai.AnalysisRequest) (*ai.AnalysisResponse, error) {
		calls++
		if calls == 1 {
			return nil, errors.New("provider unavailable")
		}
		return &ai.AnalysisResponse{Status: ai.ResponseStatusComplete, Findings: []ai.ResponseFinding{}}, nil
	}}
	orchestrator := newOrchestrator(t, builder, provider)

	result, err := orchestrator.Review(context.Background(), changes, nil)
	if err != nil {
		t.Fatalf("Review() error = %v", err)
	}
	if result.BatchCount < 2 || calls != result.BatchCount {
		t.Fatalf("provider calls = %d, batches = %d", calls, result.BatchCount)
	}
	if len(result.Failures) != 1 || result.SuccessfulBatches != result.BatchCount-1 {
		t.Fatalf("orchestrator did not continue after failure: %#v", result)
	}
}

func TestOrchestratorHonorsCancellation(t *testing.T) {
	t.Parallel()

	providerCalled := false
	provider := mock.Provider{AnalyzeFunc: func(context.Context, ai.AnalysisRequest) (*ai.AnalysisResponse, error) {
		providerCalled = true
		return &ai.AnalysisResponse{Status: ai.ResponseStatusComplete}, nil
	}}
	orchestrator := newOrchestrator(t, newBuilder(t, ai.DefaultBudget()), provider)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := orchestrator.Review(ctx, addedFile("main.go", []string{"changed"}), nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Review() error = %v, want context.Canceled", err)
	}
	if providerCalled {
		t.Fatal("provider was called after cancellation")
	}
}

func TestNewOrchestratorRequiresProvider(t *testing.T) {
	t.Parallel()

	if _, err := ai.NewOrchestrator(newBuilder(t, ai.DefaultBudget()), nil); err == nil {
		t.Fatal("NewOrchestrator() error = nil")
	}
}

func newBuilder(t *testing.T, budget ai.Budget) ai.Builder {
	t.Helper()
	builder, err := ai.New(budget)
	if err != nil {
		t.Fatalf("ai.New() error = %v", err)
	}
	return builder
}

func newOrchestrator(t *testing.T, builder ai.Builder, provider ai.Provider) *ai.Orchestrator {
	t.Helper()
	orchestrator, err := ai.NewOrchestrator(builder, provider)
	if err != nil {
		t.Fatalf("NewOrchestrator() error = %v", err)
	}
	return orchestrator
}

func addedFile(path string, additions []string) change.ChangeSet {
	lines := make([]change.Line, 0, len(additions))
	for index, content := range additions {
		lines = append(lines, change.Line{Kind: change.LineAdded, NewLine: index + 1, Content: content})
	}
	return change.ChangeSet{Files: []change.FileChange{{
		NewPath: path,
		Status:  change.StatusAdded,
		Hunks: []change.Hunk{{
			OldStart: 0,
			OldLines: 0,
			NewStart: 1,
			NewLines: len(lines),
			Lines:    lines,
		}},
	}}}
}

func localFinding(file string, line int, title, message string) findings.Finding {
	return findings.Finding{
		File:       file,
		StartLine:  line,
		EndLine:    line,
		Severity:   findings.SeverityMedium,
		Category:   findings.CategorySecurity,
		Title:      title,
		Message:    message,
		Confidence: 0.82,
		Source:     findings.SourceLocalRule,
	}
}

func findByTitle(values []findings.Finding, title string) *findings.Finding {
	for index := range values {
		if values[index].Title == title {
			return &values[index]
		}
	}
	return nil
}

func withFile(value ai.ResponseFinding, file string) ai.ResponseFinding {
	value.File = file
	return value
}

func withLine(value ai.ResponseFinding, line int) ai.ResponseFinding {
	value.StartLine = line
	value.EndLine = line
	return value
}

func withSeverity(value ai.ResponseFinding, severity findings.Severity) ai.ResponseFinding {
	value.Severity = severity
	return value
}
