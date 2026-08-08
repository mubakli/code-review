package context

import (
	"strings"
	"testing"

	"code-review/internal/change"
)

func TestDiffSymbolsExtractsDistinctIdentifiers(t *testing.T) {
	t.Parallel()

	changes := change.ChangeSet{Files: []change.FileChange{{
		NewPath: "service.go",
		Status:  change.StatusModified,
		Hunks: []change.Hunk{{Lines: []change.Line{
			{Kind: change.LineAdded, NewLine: 1, Content: "func (s *UserService) FindUser(id int64) (User, error) {"},
			{Kind: change.LineAdded, NewLine: 2, Content: "owner, err := s.ownershipPolicy.Check(ctx, id)"},
		}}},
	}}}
	symbols := DiffSymbols(changes)
	for _, symbol := range []string{"FindUser", "UserService", "ownershipPolicy"} {
		if !containsString(symbols, symbol) {
			t.Errorf("DiffSymbols() = %#v, want %q", symbols, symbol)
		}
	}
	if containsString(symbols, "request") || containsString(symbols, "func") || containsString(symbols, "id") {
		t.Errorf("DiffSymbols() retained generic tokens: %#v", symbols)
	}
}

func TestDiffSymbolsExtractsImportPackageNames(t *testing.T) {
	t.Parallel()

	changes := change.ChangeSet{Files: []change.FileChange{{
		NewPath: "api.go",
		Status:  change.StatusAdded,
		Hunks: []change.Hunk{{Lines: []change.Line{
			{Kind: change.LineAdded, NewLine: 1, Content: `import "github.com/acme/identity/vault"`},
		}}},
	}}}
	symbols := DiffSymbols(changes)
	if !containsString(symbols, "vault") {
		t.Fatalf("DiffSymbols() = %#v, want the import package name", symbols)
	}
	if containsString(symbols, "github") || containsString(symbols, "acme") {
		t.Fatalf("DiffSymbols() leaked import domain segments: %#v", symbols)
	}
}

func TestDiffSymbolsRanksByFrequencyAndCaps(t *testing.T) {
	t.Parallel()

	lines := make([]string, 0, 20)
	for index := 0; index < 20; index++ {
		lines = append(lines, "workflow.Invoke(alpha, beta)")
	}
	changes := change.ChangeSet{Files: []change.FileChange{{
		NewPath: "flow.go",
		Status:  change.StatusAdded,
		Hunks: []change.Hunk{{Lines: []change.Line{
			{Kind: change.LineAdded, NewLine: 1, Content: "workflow.Invoke(alpha, beta) " + strings.Repeat("gamma() ", 40)},
		}}},
	}}}
	symbols := DiffSymbols(changes)
	if len(symbols) > maxDiffSymbols {
		t.Fatalf("DiffSymbols() = %d symbols, want at most %d", len(symbols), maxDiffSymbols)
	}
	if len(symbols) != 5 {
		t.Fatalf("DiffSymbols() = %#v, want the 5 distinct tokens deduplicated", symbols)
	}
	for _, symbol := range []string{"workflow", "Invoke", "alpha", "beta", "gamma"} {
		if !containsString(symbols, symbol) {
			t.Errorf("DiffSymbols() = %#v, want %q", symbols, symbol)
		}
	}
}

func TestDiffSymbolsIgnoresDeletedAndContextLines(t *testing.T) {
	t.Parallel()

	changes := change.ChangeSet{Files: []change.FileChange{{
		NewPath: "service.go",
		Status:  change.StatusModified,
		Hunks: []change.Hunk{{Lines: []change.Line{
			{Kind: change.LineDeleted, OldLine: 1, Content: "vulnerableLookup()"},
			{Kind: change.LineContext, OldLine: 2, NewLine: 2, Content: "existingLookup()"},
			{Kind: change.LineAdded, NewLine: 3, Content: "replacementLookup()"},
		}}},
	}}}
	symbols := DiffSymbols(changes)
	if len(symbols) != 1 || symbols[0] != "replacementLookup" {
		t.Fatalf("DiffSymbols() = %#v, want only added-line symbols", symbols)
	}
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
