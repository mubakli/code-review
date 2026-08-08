package egress

import (
	"testing"

	"code-review/internal/change"
)

func TestDefaultRulesClassifySecretsAndSource(t *testing.T) {
	t.Parallel()

	policy, err := New(DefaultRules())
	if err != nil {
		t.Fatalf("New(DefaultRules()) error = %v", err)
	}
	tests := []struct {
		path   string
		action EgressAction
	}{
		{path: ".env", action: EgressDeny},
		{path: ".env.production", action: EgressDeny},
		{path: "config/.env.local", action: EgressDeny},
		{path: "certs/server.pem", action: EgressDeny},
		{path: "secrets/db.key", action: EgressDeny},
		{path: "secrets/nested/token.key", action: EgressDeny},
		{path: "config/settings.config.json", action: EgressRedact},
		{path: "src/server/main.go", action: EgressAllow},
		{path: "tests/unit_test.go", action: EgressAllow},
		{path: "docs/design.md", action: EgressAllow},
	}
	for _, test := range tests {
		if got := policy.Classify(test.path); got != test.action {
			t.Errorf("Classify(%q) = %q, want %q", test.path, got, test.action)
		}
		if test.action == EgressDeny && policy.Allow(test.path) {
			t.Errorf("Allow(%q) = true for a denied path", test.path)
		}
		if test.action == EgressRedact && !policy.Redact(test.path) {
			t.Errorf("Redact(%q) = false for a redacted path", test.path)
		}
	}
}

func TestEmptyPolicyAllowsEverything(t *testing.T) {
	t.Parallel()

	policy, err := New(nil)
	if err != nil {
		t.Fatalf("New(nil) error = %v", err)
	}
	for _, path := range []string{".env", "secrets/token.key", "src/main.go"} {
		if !policy.Allow(path) || policy.Redact(path) {
			t.Errorf("empty policy did not allow %q", path)
		}
	}
}

func TestPolicyFirstMatchWins(t *testing.T) {
	t.Parallel()

	policy, err := New([]EgressRule{
		{Pattern: "src/**", Action: EgressDeny},
		{Pattern: "src/public/**", Action: EgressAllow},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if policy.Classify("src/public/index.html") != EgressDeny {
		t.Fatal("first matching rule did not win")
	}
}

func TestPolicyRejectsInvalidRules(t *testing.T) {
	t.Parallel()

	for _, rules := range [][]EgressRule{
		{{Pattern: "src/**", Action: "upload"}},
		{{Pattern: "", Action: EgressDeny}},
		{{Pattern: "src/[", Action: EgressDeny}},
	} {
		if _, err := New(rules); err == nil {
			t.Errorf("New(%#v) error = nil", rules)
		}
	}
}

func TestPolicyFilterChangesDropsDeniedFiles(t *testing.T) {
	t.Parallel()

	policy, err := New([]EgressRule{{Pattern: "*.pem", Action: EgressDeny}})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	changes := change.ChangeSet{Files: []change.FileChange{
		{NewPath: "certs/server.pem", Status: change.StatusAdded},
		{NewPath: "src/main.go", Status: change.StatusAdded},
	}}
	filtered := policy.FilterChanges(changes)
	if len(filtered.Files) != 1 || filtered.Files[0].Path() != "src/main.go" {
		t.Fatalf("FilterChanges() = %#v", filtered.Files)
	}
}

func TestGlobPatterns(t *testing.T) {
	t.Parallel()

	policy, err := New([]EgressRule{
		{Pattern: "configs/*.json", Action: EgressDeny},
		{Pattern: "**.pem", Action: EgressDeny},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	tests := []struct {
		path string
		deny bool
	}{
		{path: "configs/app.json", deny: true},
		{path: "configs/nested/app.json", deny: false},
		{path: "deploy/prod/server.pem", deny: true},
		{path: "certs/server.pem", deny: true},
		{path: "keys/app.key", deny: false},
	}
	for _, test := range tests {
		if got := !policy.Allow(test.path); got != test.deny {
			t.Errorf("Allow(%q) = %t, want %t", test.path, !test.deny, test.deny)
		}
	}
}
