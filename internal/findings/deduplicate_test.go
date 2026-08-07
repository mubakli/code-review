package findings

import "testing"

func TestMergeConsolidatesLocalAndAIDuplicates(t *testing.T) {
	t.Parallel()

	local := Finding{
		File:       "repository.go",
		StartLine:  42,
		EndLine:    42,
		Severity:   SeverityMedium,
		Category:   CategorySecurity,
		Title:      "Potential SQL Injection",
		Message:    "User-controlled data is interpolated into a SQL query.",
		Confidence: 0.82,
		Source:     SourceLocalRule,
	}
	ai := Finding{
		File:       "repository.go",
		StartLine:  42,
		EndLine:    42,
		Severity:   SeverityHigh,
		Category:   CategorySecurity,
		Title:      "Unsanitized input may reach SQL query",
		Message:    "The changed SQL construction may allow injection.",
		Suggestion: "Use a parameterized query.",
		Confidence: 0.94,
		Source:     SourceAI,
	}

	merged := Merge([]Finding{local}, []Finding{ai})
	if len(merged) != 1 {
		t.Fatalf("len(Merge()) = %d, want 1: %#v", len(merged), merged)
	}
	if merged[0].Source != SourceLocalRule || merged[0].Title != local.Title {
		t.Fatalf("primary finding was not preserved: %#v", merged[0])
	}
	if merged[0].Severity != SeverityHigh || merged[0].Confidence != 0.94 {
		t.Fatalf("severity or confidence was not consolidated: %#v", merged[0])
	}
	if merged[0].Suggestion != ai.Suggestion {
		t.Fatalf("missing suggestion was not filled: %#v", merged[0])
	}
}

func TestMergeKeepsDistinctIssuesOnSameLine(t *testing.T) {
	t.Parallel()

	base := Finding{File: "handler.go", StartLine: 10, EndLine: 10, Severity: SeverityHigh, Category: CategorySecurity, Confidence: 0.9, Source: SourceLocalRule}
	sql := base
	sql.Title = "SQL injection"
	sql.Message = "A query contains interpolated input."
	xss := base
	xss.Title = "Cross-site scripting"
	xss.Message = "HTML output is not escaped."
	xss.Source = SourceAI

	merged := Merge([]Finding{sql}, []Finding{xss})
	if len(merged) != 2 {
		t.Fatalf("len(Merge()) = %d, want 2: %#v", len(merged), merged)
	}
}

func TestMergeDoesNotMutateInputs(t *testing.T) {
	t.Parallel()

	primary := []Finding{{File: "a.go", StartLine: 1, EndLine: 1, Severity: SeverityLow, Category: CategoryQuality, Title: "Duplicate code", Message: "Logic is repeated.", Confidence: 0.5, Source: SourceLocalRule}}
	secondary := []Finding{{File: "a.go", StartLine: 1, EndLine: 1, Severity: SeverityHigh, Category: CategoryQuality, Title: "Repeated duplicate code", Message: "Logic is repeated.", Confidence: 0.9, Source: SourceAI}}
	_ = Merge(primary, secondary)
	if primary[0].Severity != SeverityLow || primary[0].Confidence != 0.5 {
		t.Fatalf("primary input was mutated: %#v", primary)
	}
}
