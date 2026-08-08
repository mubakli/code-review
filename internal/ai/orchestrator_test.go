package ai_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"code-review/internal/ai"
	aicontext "code-review/internal/ai/context"
	"code-review/internal/ai/provider"
	"code-review/internal/ai/request"
	"code-review/internal/change"
	"code-review/internal/findings"
	"code-review/internal/redact"
)

func TestOrchestratorValidatesMergesAndDeduplicates(t *testing.T) {
	t.Parallel()

	builder := newPreparer(t, aicontext.DefaultBudget())
	secret := "actual-secret-value"
	changes := addedFile("service.go", []string{
		"package service",
		`const password = "` + secret + `"`,
		"result, _ := run()",
	})
	local := localFinding("service.go", 2, "Potential provider credential", "An added line matches a credential format.")
	providerCalls := 0
	provider := provider.Mock{AnalyzeFunc: func(_ context.Context, request request.AnalysisRequest) (*provider.AnalysisResponse, error) {
		providerCalls++
		if strings.Contains(request.Diff(), secret) || !strings.Contains(request.Diff(), redact.Placeholder) {
			t.Fatalf("provider received unredacted diff:\n%s", request.Diff())
		}
		if len(request.StaticFindings()) != 1 {
			t.Fatalf("provider received %d static findings, want 1", len(request.StaticFindings()))
		}
		return &provider.AnalysisResponse{
			Status: provider.ResponseStatusComplete,
			Findings: []provider.ResponseFinding{
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
					ProposedFix: &findings.ProposedFix{
						Description: "Handle the returned error.",
						StartLine:   3,
						EndLine:     3,
						Replacement: "result, err := run()",
					},
					Confidence: 0.91,
				},
			},
		}, nil
	}}
	agents, _ := ai.SelectAgents([]string{"correctness", "security"})
	orchestrator, err := ai.NewOrchestratorWithAgents(builder, request.RequestBuilder{}, provider, agents)
	if err != nil {
		t.Fatalf("NewOrchestratorWithAgents() error = %v", err)
	}

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
	if ignoredError.RuleID != "ai/correctness" || !strings.HasPrefix(ignoredError.FindingID, "sha256:") || ignoredError.ProposedFix == nil {
		t.Fatalf("AI finding identity/fix was not finalized: %#v", ignoredError)
	}
}

func TestOrchestratorRejectsInvalidResponsesWithoutLosingLocalFindings(t *testing.T) {
	t.Parallel()

	changes := addedFile("service.go", []string{"unchanged-looking context", "changed()"})
	local := localFinding("service.go", 2, "Local issue", "A deterministic local issue.")
	validCandidate := provider.ResponseFinding{
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
		response *provider.AnalysisResponse
	}{
		{name: "nil response", response: nil},
		{name: "unknown status", response: &provider.AnalysisResponse{Status: "need_context"}},
		{name: "outside file", response: &provider.AnalysisResponse{Status: provider.ResponseStatusComplete, Findings: []provider.ResponseFinding{withFile(validCandidate, "other.go")}}},
		{name: "unchanged line", response: &provider.AnalysisResponse{Status: provider.ResponseStatusComplete, Findings: []provider.ResponseFinding{withLine(validCandidate, 99)}}},
		{name: "range includes unchanged line", response: &provider.AnalysisResponse{Status: provider.ResponseStatusComplete, Findings: []provider.ResponseFinding{withRange(validCandidate, 2, 3)}}},
		{name: "invalid severity", response: &provider.AnalysisResponse{Status: provider.ResponseStatusComplete, Findings: []provider.ResponseFinding{withSeverity(validCandidate, "unknown")}}},
		{name: "fix range differs", response: &provider.AnalysisResponse{Status: provider.ResponseStatusComplete, Findings: []provider.ResponseFinding{withFix(validCandidate, &findings.ProposedFix{Description: "Replace line.", StartLine: 1, EndLine: 1, Replacement: "changed()"})}}},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			provider := provider.Mock{AnalyzeFunc: func(context.Context, request.AnalysisRequest) (*provider.AnalysisResponse, error) {
				return test.response, nil
			}}
			agents, _ := ai.SelectAgents([]string{"correctness"})
			orchestrator, _ := ai.NewOrchestratorWithAgents(newPreparer(t, aicontext.DefaultBudget()), request.RequestBuilder{}, provider, agents)
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

	budget := aicontext.Budget{MaxInputTokens: 400, MaxDiffTokens: 80, MaxStaticFindingTokens: 0}
	builder := newPreparer(t, budget)
	lines := make([]string, 30)
	for index := range lines {
		lines[index] = "changed line with enough content to split batches"
	}
	changes := addedFile("large.ts", lines)
	calls := 0
	provider := provider.Mock{AnalyzeFunc: func(context.Context, request.AnalysisRequest) (*provider.AnalysisResponse, error) {
		calls++
		if calls == 1 {
			return nil, errors.New("provider unavailable")
		}
		return &provider.AnalysisResponse{Status: provider.ResponseStatusComplete, Findings: []provider.ResponseFinding{}}, nil
	}}
	agents, _ := ai.SelectAgents([]string{"correctness"})
	orchestrator, _ := ai.NewOrchestratorWithAgents(builder, request.RequestBuilder{}, provider, agents)

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
	provider := provider.Mock{AnalyzeFunc: func(context.Context, request.AnalysisRequest) (*provider.AnalysisResponse, error) {
		providerCalled = true
		return &provider.AnalysisResponse{Status: provider.ResponseStatusComplete}, nil
	}}
	agents, _ := ai.SelectAgents([]string{"correctness"})
	orchestrator, _ := ai.NewOrchestratorWithAgents(newPreparer(t, aicontext.DefaultBudget()), request.RequestBuilder{}, provider, agents)
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

	if _, err := ai.NewOrchestrator(newPreparer(t, aicontext.DefaultBudget()), request.RequestBuilder{}, nil); err == nil {
		t.Fatal("NewOrchestrator() error = nil")
	}
}

func TestNewOrchestratorRejectsRouterAgentsInTheReviewLoop(t *testing.T) {
	t.Parallel()

	_, err := ai.NewOrchestratorWithAgents(newPreparer(t, aicontext.DefaultBudget()), request.RequestBuilder{}, provider.Mock{}, []ai.AgentSpec{ai.SecurityTriageRouter})
	if err == nil {
		t.Fatal("NewOrchestratorWithAgents() error = nil for a router agent")
	}
}

func TestNewOrchestratorRequiresRoutingPolicy(t *testing.T) {
	t.Parallel()

	agents := []ai.AgentSpec{{
		ID:   ai.AgentCorrectness,
		Role: ai.RoleAnalyzer,
	}}
	if _, err := ai.NewOrchestratorWithAgents(newPreparer(t, aicontext.DefaultBudget()), request.RequestBuilder{}, provider.Mock{}, agents); err == nil {
		t.Fatal("NewOrchestratorWithAgents() error = nil for an agent without a policy")
	}
}

func TestSecurityPipelineSkipsRouterOnDeterministicSignal(t *testing.T) {
	t.Parallel()

	changes := addedFile("service.go", []string{`password := request.FormValue("password")`})
	triageCalled := false
	provider := provider.Mock{
		AnalyzeFunc: func(context.Context, request.AnalysisRequest) (*provider.AnalysisResponse, error) {
			return &provider.AnalysisResponse{Status: provider.ResponseStatusComplete}, nil
		},
		TriageFunc: func(context.Context, request.AnalysisRequest) (*provider.TriageResponse, error) {
			triageCalled = true
			t.Fatal("triage router ran despite a deterministic security signal")
			return nil, nil
		},
	}
	agents, _ := ai.SelectAgents([]string{"security"})
	orchestrator, _ := ai.NewOrchestratorWithAgents(newPreparer(t, aicontext.DefaultBudget()), request.RequestBuilder{}, provider, agents)

	result, err := orchestrator.Review(context.Background(), changes, nil)
	if err != nil {
		t.Fatalf("Review() error = %v", err)
	}
	if triageCalled {
		t.Fatal("triage router was called despite the deterministic signal")
	}
	if !containsAgent(result.Agents, string(ai.AgentSecurity)) {
		t.Fatalf("deep security did not run on the deterministic signal: %#v", result.Agents)
	}
	if containsAgent(result.Agents, string(ai.AgentSecurityTriage)) {
		t.Fatalf("triage router was recorded despite the deterministic shortcut: %#v", result.Agents)
	}
}

func TestSecurityPipelineSkipsDeepReviewWhenTriageClears(t *testing.T) {
	t.Parallel()

	changes := addedFile("service.go", []string{"return result"})
	provider := provider.Mock{
		AnalyzeFunc: func(context.Context, request.AnalysisRequest) (*provider.AnalysisResponse, error) {
			t.Fatal("deep security Analyze was called unexpectedly")
			return nil, nil
		},
	}
	agents, _ := ai.SelectAgents([]string{"security"})
	orchestrator, _ := ai.NewOrchestratorWithAgents(newPreparer(t, aicontext.DefaultBudget()), request.RequestBuilder{}, provider, agents)

	result, err := orchestrator.Review(context.Background(), changes, nil)
	if err != nil {
		t.Fatalf("Review() error = %v", err)
	}
	if !containsAgent(result.Agents, string(ai.AgentSecurityTriage)) {
		t.Fatalf("security-triage agent is missing: %#v", result.Agents)
	}
	if containsAgent(result.Agents, string(ai.AgentSecurity)) {
		t.Fatalf("deep security ran despite triage clearing: %#v", result.Agents)
	}
}

func TestSecurityPipelineEscalatesOnTriageDecision(t *testing.T) {
	t.Parallel()

	changes := addedFile("service.go", []string{"return result"})
	securityCalled := false
	provider := provider.Mock{
		AnalyzeFunc: func(context.Context, request.AnalysisRequest) (*provider.AnalysisResponse, error) {
			securityCalled = true
			return &provider.AnalysisResponse{Status: provider.ResponseStatusComplete}, nil
		},
		TriageFunc: func(context.Context, request.AnalysisRequest) (*provider.TriageResponse, error) {
			return &provider.TriageResponse{
				Status:    provider.ResponseStatusComplete,
				Escalate:  true,
				Surfaces:  []string{"user-controlled input reaches a database query built by string concatenation"},
				Rationale: "The surface awaits confirmation; enforcement such as authorization middleware may live outside this diff.",
			}, nil
		},
	}
	agents, _ := ai.SelectAgents([]string{"security"})
	orchestrator, _ := ai.NewOrchestratorWithAgents(newPreparer(t, aicontext.DefaultBudget()), request.RequestBuilder{}, provider, agents)

	result, err := orchestrator.Review(context.Background(), changes, nil)
	if err != nil {
		t.Fatalf("Review() error = %v", err)
	}
	if !containsAgent(result.Agents, string(ai.AgentSecurityTriage)) {
		t.Fatalf("security-triage agent is missing: %#v", result.Agents)
	}
	if !containsAgent(result.Agents, string(ai.AgentSecurity)) {
		t.Fatalf("deep security was not escalated: %#v", result.Agents)
	}
	if !securityCalled {
		t.Fatal("deep security Analyze was not called")
	}
}

func TestSecurityPipelineFailClosedOnTriageError(t *testing.T) {
	t.Parallel()

	changes := addedFile("service.go", []string{"return result"})
	securityCalled := false
	provider := provider.Mock{
		AnalyzeFunc: func(context.Context, request.AnalysisRequest) (*provider.AnalysisResponse, error) {
			securityCalled = true
			return &provider.AnalysisResponse{Status: provider.ResponseStatusComplete}, nil
		},
		TriageFunc: func(context.Context, request.AnalysisRequest) (*provider.TriageResponse, error) {
			return nil, errors.New("triage provider unavailable")
		},
	}
	agents, _ := ai.SelectAgents([]string{"security"})
	orchestrator, _ := ai.NewOrchestratorWithAgents(newPreparer(t, aicontext.DefaultBudget()), request.RequestBuilder{}, provider, agents)

	result, err := orchestrator.Review(context.Background(), changes, nil)
	if err != nil {
		t.Fatalf("Review() error = %v", err)
	}
	if !containsAgent(result.Agents, string(ai.AgentSecurity)) {
		t.Fatalf("deep security was not escalated on triage failure: %#v", result.Agents)
	}
	if !securityCalled {
		t.Fatal("deep security Analyze was not called after triage failure")
	}
	if len(result.Failures) != 1 || result.Failures[0].AgentID != string(ai.AgentSecurityTriage) {
		t.Fatalf("triage failure was not recorded: %#v", result.Failures)
	}
}

func newPreparer(t *testing.T, budget aicontext.Budget) aicontext.Preparer {
	t.Helper()
	preparer, err := aicontext.NewPreparer(budget)
	if err != nil {
		t.Fatalf("aicontext.NewPreparer() error = %v", err)
	}
	return preparer
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
	finding := findings.Finding{
		RuleID:     "test/local-rule",
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
	finding.FinalizeID()
	return finding
}

func findByTitle(values []findings.Finding, title string) *findings.Finding {
	for index := range values {
		if values[index].Title == title {
			return &values[index]
		}
	}
	return nil
}

func containsAgent(agents []string, id string) bool {
	for _, agent := range agents {
		if agent == id {
			return true
		}
	}
	return false
}

func withFile(value provider.ResponseFinding, file string) provider.ResponseFinding {
	value.File = file
	return value
}

func withLine(value provider.ResponseFinding, line int) provider.ResponseFinding {
	value.StartLine = line
	value.EndLine = line
	return value
}

func withSeverity(value provider.ResponseFinding, severity findings.Severity) provider.ResponseFinding {
	value.Severity = severity
	return value
}

func withRange(value provider.ResponseFinding, start, end int) provider.ResponseFinding {
	value.StartLine = start
	value.EndLine = end
	return value
}

func withFix(value provider.ResponseFinding, fix *findings.ProposedFix) provider.ResponseFinding {
	value.ProposedFix = fix
	return value
}
