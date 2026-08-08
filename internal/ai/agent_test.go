package ai_test

import (
	"context"
	"strings"
	"testing"

	"code-review/internal/ai"
	aicontext "code-review/internal/ai/context"
	"code-review/internal/ai/routing"
	"code-review/internal/change"
	"code-review/internal/findings"
)

func TestSecurityEscalationPolicySkipsRouterOnKeywordSignal(t *testing.T) {
	t.Parallel()

	scope := &fakePolicyScope{t: t}
	changes := addedFile("service.go", []string{`password := request.FormValue("password")`})
	var policy ai.RoutingPolicy = ai.SecurityEscalationPolicy{}
	decision, err := policy.ShouldRun(context.Background(), changes, nil, &ai.ReviewResult{}, new(int), scope)
	if err != nil {
		t.Fatalf("ShouldRun() error = %v", err)
	}
	if !decision.Run {
		t.Fatal("deterministic keyword signal did not escalate")
	}
	if scope.routerRuns != 0 {
		t.Fatalf("triage router ran %d times, want 0 with a deterministic signal", scope.routerRuns)
	}
}

func TestSecurityEscalationPolicySkipsRouterOnSensitivePathSignal(t *testing.T) {
	t.Parallel()

	scope := &fakePolicyScope{t: t}
	changes := change.ChangeSet{Files: []change.FileChange{{
		NewPath: ".env.production",
		Status:  change.StatusAdded,
		Hunks: []change.Hunk{{Lines: []change.Line{
			{Kind: change.LineAdded, NewLine: 1, Content: "NODE_ENV=production"},
		}}},
	}}}
	var policy ai.RoutingPolicy = ai.SecurityEscalationPolicy{}
	decision, err := policy.ShouldRun(context.Background(), changes, nil, &ai.ReviewResult{}, new(int), scope)
	if err != nil {
		t.Fatalf("ShouldRun() error = %v", err)
	}
	if !decision.Run {
		t.Fatal("deterministic sensitive-path signal did not escalate")
	}
	if scope.routerRuns != 0 {
		t.Fatalf("triage router ran %d times, want 0 with a sensitive path", scope.routerRuns)
	}
}

func TestSecurityEscalationPolicyFeedsEndpointSignalsToTriage(t *testing.T) {
	t.Parallel()

	// A new endpoint registration is a broad signal (medium confidence), not a
	// verdict: the cheap triage router examines it instead of the change being
	// escalated blindly, so routine endpoint work stays on the triage path.
	scope := &fakePolicyScope{t: t, routerEscalate: true}
	changes := addedFile("api.py", []string{`@app.post("/transfer") def transfer():`})
	var policy ai.RoutingPolicy = ai.SecurityEscalationPolicy{}
	decision, err := policy.ShouldRun(context.Background(), changes, nil, &ai.ReviewResult{}, new(int), scope)
	if err != nil {
		t.Fatalf("ShouldRun() error = %v", err)
	}
	if scope.routerRuns != 1 {
		t.Fatalf("triage router ran %d times, want 1 for an endpoint feature", scope.routerRuns)
	}
	if !strings.Contains(scope.lastRouter.Prompt, "Deterministic features detected in this diff") {
		t.Fatalf("triage router did not receive the endpoint feature:\n%s", scope.lastRouter.Prompt)
	}
	if !decision.Run {
		t.Fatal("router escalation did not reach the decision")
	}
}

func TestSecurityEscalationPolicyIgnoresDeletedSecurityLines(t *testing.T) {
	t.Parallel()

	scope := &fakePolicyScope{t: t}
	changes := change.ChangeSet{Files: []change.FileChange{{
		NewPath: "service.go",
		Status:  change.StatusModified,
		Hunks: []change.Hunk{{Lines: []change.Line{
			{Kind: change.LineDeleted, OldLine: 1, Content: "password = unsafe"},
			{Kind: change.LineAdded, NewLine: 1, Content: "value = safe"},
		}}},
	}}}
	var policy ai.RoutingPolicy = ai.SecurityEscalationPolicy{}
	decision, err := policy.ShouldRun(context.Background(), changes, nil, &ai.ReviewResult{}, new(int), scope)
	if err != nil {
		t.Fatalf("ShouldRun() error = %v", err)
	}
	if decision.Run {
		t.Fatal("deleted security lines must not escalate")
	}
}

func TestSecurityEscalationPolicyAcceptsInjectedDetector(t *testing.T) {
	t.Parallel()

	scope := &fakePolicyScope{t: t}
	changes := addedFile("service.go", []string{"return result"})
	detector := fixedSignalDetector{signals: 1}
	policy := ai.SecurityEscalationPolicy{Detector: detector}
	decision, err := policy.ShouldRun(context.Background(), changes, nil, &ai.ReviewResult{}, new(int), scope)
	if err != nil {
		t.Fatalf("ShouldRun() error = %v", err)
	}
	if !decision.Run || scope.routerRuns != 0 {
		t.Fatalf("injected detector signal did not escalate: decision=%#v routerRuns=%d", decision, scope.routerRuns)
	}
}

type fixedSignalDetector struct {
	signals    int
	confidence routing.Confidence
}

func (d fixedSignalDetector) Detect(change.ChangeSet) []routing.Signal {
	if d.signals == 0 {
		return nil
	}
	confidence := d.confidence
	if confidence == "" {
		confidence = routing.ConfidenceHigh
	}
	return []routing.Signal{{
		Kind:       routing.SignalKeyword,
		Surface:    routing.SurfaceCredentials,
		Confidence: confidence,
		File:       "injected.go",
		Reason:     "injected deterministic signal",
	}}
}

func TestSelectAgentsUsesRequestedOrder(t *testing.T) {
	t.Parallel()

	agents, err := ai.SelectAgents([]string{"security", "correctness"})
	if err != nil {
		t.Fatalf("SelectAgents() error = %v", err)
	}
	if len(agents) != 2 || agents[0].ID != ai.AgentSecurity || agents[1].ID != ai.AgentCorrectness {
		t.Fatalf("SelectAgents() = %#v", agents)
	}
}

func TestSelectAgentsRejectsInvalidSelections(t *testing.T) {
	t.Parallel()

	for _, ids := range [][]string{nil, {"performance"}, {"security", "security"}} {
		if _, err := ai.SelectAgents(ids); err == nil {
			t.Fatalf("SelectAgents(%v) error = nil", ids)
		}
	}
}

func TestSelectAgentsRejectsRouterAgents(t *testing.T) {
	t.Parallel()

	if _, err := ai.SelectAgents([]string{"security-triage"}); err == nil {
		t.Fatal("SelectAgents(security-triage) error = nil; router agents are advisory, not selectable")
	}
}

func TestDefaultAgentsDeclareAnalyzerRolesAndPolicies(t *testing.T) {
	t.Parallel()

	agents := ai.DefaultAgents()
	if len(agents) != 2 {
		t.Fatalf("DefaultAgents() = %d agents, want 2", len(agents))
	}
	if agents[0].ID != ai.AgentCorrectness || agents[0].Role != ai.RoleAnalyzer {
		t.Fatalf("correctness spec = %#v", agents[0])
	}
	if _, ok := agents[0].Policy.(ai.AlwaysRun); !ok {
		t.Fatalf("correctness policy = %#v, want AlwaysRun", agents[0].Policy)
	}
	if agents[1].ID != ai.AgentSecurity || agents[1].Role != ai.RoleAnalyzer {
		t.Fatalf("security spec = %#v", agents[1])
	}
	if _, ok := agents[1].Policy.(ai.SecurityEscalationPolicy); !ok {
		t.Fatalf("security policy = %#v, want SecurityEscalationPolicy", agents[1].Policy)
	}
}

func TestSecurityTriageRouterDeclaresRouterRole(t *testing.T) {
	t.Parallel()

	if ai.SecurityTriageRouter.ID != ai.AgentSecurityTriage || ai.SecurityTriageRouter.Role != ai.RoleRouter {
		t.Fatalf("SecurityTriageRouter = %#v, want router role", ai.SecurityTriageRouter)
	}
	if _, ok := ai.SecurityTriageRouter.Policy.(ai.AlwaysRun); !ok {
		t.Fatalf("SecurityTriageRouter policy = %#v, want AlwaysRun", ai.SecurityTriageRouter.Policy)
	}
}

func TestAlwaysRunRoutesEveryChange(t *testing.T) {
	t.Parallel()

	var policy ai.RoutingPolicy = ai.AlwaysRun{}
	decision, err := policy.ShouldRun(context.Background(), addedFile("main.go", []string{"changed"}), nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("ShouldRun() error = %v", err)
	}
	if !decision.Run || len(decision.Context) != 0 {
		t.Fatalf("AlwaysRun decision = %#v, want Run with no context", decision)
	}
}

func TestSecurityEscalationPolicySkipsRouterOnDeterministicSignal(t *testing.T) {
	t.Parallel()

	scope := &fakePolicyScope{t: t}
	changes := addedFile("service.go", []string{`password := request.FormValue("password")`})
	var policy ai.RoutingPolicy = ai.SecurityEscalationPolicy{}
	decision, err := policy.ShouldRun(context.Background(), changes, nil, &ai.ReviewResult{}, new(int), scope)
	if err != nil {
		t.Fatalf("ShouldRun() error = %v", err)
	}
	if !decision.Run {
		t.Fatal("deterministic security signal did not escalate")
	}
	if scope.routerRuns != 0 {
		t.Fatalf("triage router ran %d times, want 0 with a deterministic signal", scope.routerRuns)
	}
}

func TestSecurityEscalationPolicyConsultsRouterAndResolvesContext(t *testing.T) {
	t.Parallel()

	scope := &fakePolicyScope{
		t:              t,
		routerEscalate: true,
	}
	changes := addedFile("service.go", []string{"return result"})
	var policy ai.RoutingPolicy = ai.SecurityEscalationPolicy{}
	decision, err := policy.ShouldRun(context.Background(), changes, nil, &ai.ReviewResult{}, new(int), scope)
	if err != nil {
		t.Fatalf("ShouldRun() error = %v", err)
	}
	if !decision.Run {
		t.Fatal("router escalation did not reach the decision")
	}
	if scope.routerRuns != 1 {
		t.Fatalf("triage router ran %d times, want 1", scope.routerRuns)
	}
	if len(decision.Context) != 1 || decision.Context[0].Path != "service.go" {
		t.Fatalf("related context not resolved: %#v", decision.Context)
	}
}

func TestSecurityEscalationPolicySkipsDeepReviewWhenRouterClears(t *testing.T) {
	t.Parallel()

	scope := &fakePolicyScope{t: t}
	changes := addedFile("service.go", []string{"return result"})
	var policy ai.RoutingPolicy = ai.SecurityEscalationPolicy{}
	decision, err := policy.ShouldRun(context.Background(), changes, nil, &ai.ReviewResult{}, new(int), scope)
	if err != nil {
		t.Fatalf("ShouldRun() error = %v", err)
	}
	if decision.Run || scope.contextResolves != 0 {
		t.Fatalf("clearing router still ran: decision=%#v contextResolves=%d", decision, scope.contextResolves)
	}
}

func TestSecurityEscalationPolicyCarriesAssessmentIntoDecision(t *testing.T) {
	t.Parallel()

	scope := &fakePolicyScope{t: t, routerEscalate: true}
	changes := addedFile("service.go", []string{"return result"})
	var policy ai.RoutingPolicy = ai.SecurityEscalationPolicy{}
	decision, err := policy.ShouldRun(context.Background(), changes, nil, &ai.ReviewResult{}, new(int), scope)
	if err != nil {
		t.Fatalf("ShouldRun() error = %v", err)
	}
	if decision.Assessment == nil || !decision.Assessment.Escalate {
		t.Fatalf("decision has no escalated assessment: %#v", decision.Assessment)
	}
	if len(decision.Assessment.Surfaces) != 1 || decision.Assessment.Surfaces[0] != routing.SurfaceAuthorization {
		t.Fatalf("assessment surfaces = %#v", decision.Assessment.Surfaces)
	}
	if len(decision.Assessment.Reasons) != 1 {
		t.Fatalf("assessment reasons = %#v", decision.Assessment.Reasons)
	}
}

func TestSecurityEscalationPolicyRequestsIntentAwareContext(t *testing.T) {
	t.Parallel()

	scope := &fakePolicyScope{t: t, routerEscalate: true}
	changes := addedFile("service.go", []string{`rows := store.FindUser(profile.ID)`})
	var policy ai.RoutingPolicy = ai.SecurityEscalationPolicy{}
	if _, err := policy.ShouldRun(context.Background(), changes, nil, &ai.ReviewResult{}, new(int), scope); err != nil {
		t.Fatalf("ShouldRun() error = %v", err)
	}
	if scope.lastRequest.Intent != aicontext.ContextIntentAuthorization {
		t.Fatalf("context request intent = %q, want authorization", scope.lastRequest.Intent)
	}
	if len(scope.lastRequest.Symbols) == 0 {
		t.Fatalf("context request carries no diff symbols: %#v", scope.lastRequest)
	}
	if len(scope.lastRequest.Paths) != 0 {
		t.Fatalf("context request re-sends changed files as context: %#v", scope.lastRequest.Paths)
	}
}

func TestSecurityEscalationPolicyLowConfidenceSignalsFeedTriageFeatures(t *testing.T) {
	t.Parallel()

	scope := &fakePolicyScope{t: t, routerEscalate: true}
	changes := addedFile("service.go", []string{"return result"})
	policy := ai.SecurityEscalationPolicy{Detector: fixedSignalDetector{signals: 1, confidence: routing.ConfidenceLow}}
	decision, err := policy.ShouldRun(context.Background(), changes, nil, &ai.ReviewResult{}, new(int), scope)
	if err != nil {
		t.Fatalf("ShouldRun() error = %v", err)
	}
	if scope.routerRuns != 1 {
		t.Fatalf("triage router ran %d times, want 1 for a low-confidence signal", scope.routerRuns)
	}
	if !strings.Contains(scope.lastRouter.Prompt, "Deterministic features detected in this diff") {
		t.Fatalf("triage router did not receive the low-confidence feature:\n%s", scope.lastRouter.Prompt)
	}
	if !decision.Run {
		t.Fatal("router escalation did not reach the decision")
	}
}

func TestSecurityEscalationPolicySkipsRouterOnHighConfidenceSignal(t *testing.T) {
	t.Parallel()

	scope := &fakePolicyScope{t: t, routerEscalate: true}
	changes := addedFile("service.go", []string{"return result"})
	policy := ai.SecurityEscalationPolicy{Detector: fixedSignalDetector{signals: 1, confidence: routing.ConfidenceHigh}}
	decision, err := policy.ShouldRun(context.Background(), changes, nil, &ai.ReviewResult{}, new(int), scope)
	if err != nil {
		t.Fatalf("ShouldRun() error = %v", err)
	}
	if scope.routerRuns != 0 {
		t.Fatalf("triage router ran %d times, want 0 for a high-confidence signal", scope.routerRuns)
	}
	if !decision.Run || decision.Assessment == nil || !decision.Assessment.Escalate {
		t.Fatalf("high-confidence signal did not escalate directly: %#v", decision)
	}
}

func TestSecurityEscalationContextRendersSurfacesAndGuidance(t *testing.T) {
	t.Parallel()

	rendered := ai.SecurityEscalationContext(&routing.SecurityAssessment{
		Escalate:   true,
		Confidence: routing.ConfidenceHigh,
		Surfaces:   []routing.SecuritySurface{routing.SurfaceAuthorization},
		Reasons:    []string{"the surface awaits confirmation"},
	})
	for _, expected := range []string{
		"Security escalation context:",
		"Potential surfaces:",
		"- " + string(routing.SurfaceAuthorization),
		"Why this review was escalated:",
		"the surface awaits confirmation",
		"Investigate these areas first.",
		"Do not assume a vulnerability solely because an authorization check is absent from the provided diff.",
	} {
		if !strings.Contains(rendered, expected) {
			t.Fatalf("rendered context is missing %q:\n%s", expected, rendered)
		}
	}
	if rendered := ai.SecurityEscalationContext(nil); rendered != "" {
		t.Fatalf("SecurityEscalationContext(nil) = %q, want empty", rendered)
	}
}

type fakePolicyScope struct {
	t               *testing.T
	routerEscalate  bool
	routerRuns      int
	contextResolves int
	lastRouter      ai.AgentSpec
	lastRequest     aicontext.ContextRequest
	assessment      *routing.SecurityAssessment
}

func (s *fakePolicyScope) RunRouter(_ context.Context, spec ai.AgentSpec, changes change.ChangeSet, _ []findings.Finding, _ *ai.ReviewResult, _ *int) (*routing.SecurityAssessment, error) {
	s.routerRuns++
	s.lastRouter = spec
	if !s.routerEscalate {
		return nil, nil
	}
	if s.assessment != nil {
		return s.assessment, nil
	}
	return &routing.SecurityAssessment{
		Escalate:   true,
		Confidence: routing.ConfidenceMedium,
		Surfaces:   []routing.SecuritySurface{routing.SurfaceAuthorization},
		Reasons:    []string{"the surface awaits confirmation"},
	}, nil
}

func (s *fakePolicyScope) ResolveContext(_ context.Context, changes change.ChangeSet, request aicontext.ContextRequest) (aicontext.RepositoryContext, error) {
	s.contextResolves++
	s.lastRequest = request
	return aicontext.RepositoryContext{Files: []aicontext.ContextFile{{Path: changes.Files[0].Path(), Content: "staged content"}}}, nil
}
