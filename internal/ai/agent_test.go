package ai_test

import (
	"context"
	"testing"

	"code-review/internal/ai"
	"code-review/internal/change"
	"code-review/internal/findings"
)

func TestRequiresSecurityReviewDetectsKeywordsInAddedLines(t *testing.T) {
	t.Parallel()

	ordinary := addedFile("service.go", []string{"return result"})
	if ai.RequiresSecurityReview(ordinary) {
		t.Fatal("RequiresSecurityReview should return false for ordinary changes")
	}

	security := addedFile("service.go", []string{`password := request.FormValue("password")`})
	if !ai.RequiresSecurityReview(security) {
		t.Fatal("RequiresSecurityReview should return true for security keyword changes")
	}
}

func TestRequiresSecurityReviewIgnoresDeletedLines(t *testing.T) {
	t.Parallel()

	changes := change.ChangeSet{Files: []change.FileChange{{
		NewPath: "service.go",
		Status:  change.StatusModified,
		Hunks: []change.Hunk{{Lines: []change.Line{
			{Kind: change.LineDeleted, OldLine: 1, Content: "password = unsafe"},
			{Kind: change.LineAdded, NewLine: 1, Content: "value = safe"},
		}}},
	}}}
	if ai.RequiresSecurityReview(changes) {
		t.Fatal("RequiresSecurityReview should ignore deleted security terms")
	}
}

func TestRequiresSecurityReviewDetectsSensitivePaths(t *testing.T) {
	t.Parallel()

	changes := change.ChangeSet{Files: []change.FileChange{{
		NewPath: ".env.production",
		Status:  change.StatusAdded,
		Hunks: []change.Hunk{{Lines: []change.Line{
			{Kind: change.LineAdded, NewLine: 1, Content: "NODE_ENV=production"},
		}}},
	}}}
	if !ai.RequiresSecurityReview(changes) {
		t.Fatal("RequiresSecurityReview should return true for sensitive paths")
	}
}

func TestRequiresSecurityReviewDetectsInjectionSurfaces(t *testing.T) {
	t.Parallel()

	changes := change.ChangeSet{Files: []change.FileChange{{
		NewPath: "views/input.gohtml",
		Status:  change.StatusModified,
		Hunks: []change.Hunk{{Lines: []change.Line{
			{Kind: change.LineAdded, NewLine: 1, Content: `innerHTML = qs["name"]`},
		}}},
	}}}
	if !ai.RequiresSecurityReview(changes) {
		t.Fatal("RequiresSecurityReview should return true for injection surfaces")
	}
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

type fakePolicyScope struct {
	t               *testing.T
	routerEscalate  bool
	routerRuns      int
	contextResolves int
}

func (s *fakePolicyScope) RunRouter(context.Context, ai.AgentSpec, change.ChangeSet, []findings.Finding, *ai.ReviewResult, *int) (bool, error) {
	s.routerRuns++
	return s.routerEscalate, nil
}

func (s *fakePolicyScope) ResolveStagedContext(_ context.Context, changes change.ChangeSet) ([]ai.ContextFile, error) {
	s.contextResolves++
	return []ai.ContextFile{{Path: changes.Files[0].Path(), Content: "staged content"}}, nil
}
