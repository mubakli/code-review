package ai_test

import (
	"testing"

	"code-review/internal/ai"
	"code-review/internal/change"
)

func TestRouteAgentsAlwaysUsesCorrectnessAndSelectsSecurityBySignal(t *testing.T) {
	t.Parallel()

	ordinary := addedFile("service.go", []string{"return result"})
	agents := ai.RouteAgents(ordinary, ai.DefaultAgents())
	if len(agents) != 1 || agents[0].ID != ai.AgentCorrectness {
		t.Fatalf("ordinary agents = %#v", agents)
	}

	security := addedFile("service.go", []string{`password := request.FormValue("password")`})
	agents = ai.RouteAgents(security, ai.DefaultAgents())
	if len(agents) != 2 || agents[0].ID != ai.AgentCorrectness || agents[1].ID != ai.AgentSecurity {
		t.Fatalf("security agents = %#v", agents)
	}
}

func TestRouteAgentsIgnoresDeletedSecurityTerms(t *testing.T) {
	t.Parallel()

	changes := change.ChangeSet{Files: []change.FileChange{{
		NewPath: "service.go",
		Status:  change.StatusModified,
		Hunks: []change.Hunk{{Lines: []change.Line{
			{Kind: change.LineDeleted, OldLine: 1, Content: "password = unsafe"},
			{Kind: change.LineAdded, NewLine: 1, Content: "value = safe"},
		}}},
	}}}
	agents := ai.RouteAgents(changes, ai.DefaultAgents())
	if len(agents) != 1 || agents[0].ID != ai.AgentCorrectness {
		t.Fatalf("agents = %#v", agents)
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
