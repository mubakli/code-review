package ai_test

import (
	"testing"

	"code-review/internal/ai"
	"code-review/internal/change"
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
