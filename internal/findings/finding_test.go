package findings

import (
	"math"
	"strings"
	"testing"
)

func TestFindingValidate(t *testing.T) {
	t.Parallel()

	valid := Finding{
		RuleID:     "secrets/provider-credential",
		File:       "main.go",
		StartLine:  3,
		EndLine:    3,
		Severity:   SeverityHigh,
		Category:   CategorySecurity,
		Title:      "Potential credential",
		Message:    "A credential may be hardcoded.",
		Confidence: 0.9,
		Source:     SourceLocalRule,
	}
	valid.FinalizeID()
	if err := valid.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*Finding)
		want   string
	}{
		{name: "file", mutate: func(f *Finding) { f.File = "" }, want: "file"},
		{name: "rule ID missing", mutate: func(f *Finding) { f.RuleID = "" }, want: "rule ID"},
		{name: "rule ID namespace", mutate: func(f *Finding) { f.RuleID = "unsafe" }, want: "rule ID"},
		{name: "rule ID characters", mutate: func(f *Finding) { f.RuleID = "Secrets/Rule" }, want: "rule ID"},
		{name: "finding ID", mutate: func(f *Finding) { f.FindingID = "sha256:ABC" }, want: "finding ID"},
		{name: "start line", mutate: func(f *Finding) { f.StartLine = 0 }, want: "start line"},
		{name: "end line", mutate: func(f *Finding) { f.EndLine = 2 }, want: "end line"},
		{name: "severity", mutate: func(f *Finding) { f.Severity = "unknown" }, want: "severity"},
		{name: "category", mutate: func(f *Finding) { f.Category = "unknown" }, want: "category"},
		{name: "title", mutate: func(f *Finding) { f.Title = "" }, want: "title"},
		{name: "message", mutate: func(f *Finding) { f.Message = "" }, want: "message"},
		{name: "confidence", mutate: func(f *Finding) { f.Confidence = math.NaN() }, want: "confidence"},
		{name: "source", mutate: func(f *Finding) { f.Source = "unknown" }, want: "source"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			finding := valid
			test.mutate(&finding)
			err := finding.Validate()
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Validate() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestFindingIDIsDeterministicAndExcludesMutableContent(t *testing.T) {
	t.Parallel()

	base := Finding{
		RuleID:     "ai/correctness",
		File:       "service.go",
		StartLine:  10,
		EndLine:    11,
		Severity:   SeverityHigh,
		Category:   CategoryCorrectness,
		Title:      "  Lost   Update ",
		Message:    "Original message.",
		Suggestion: "Original suggestion.",
		Confidence: 0.9,
		Source:     SourceAI,
		AgentID:    "correctness",
		ProposedFix: &ProposedFix{
			Description: "Replace the transaction.",
			StartLine:   10,
			EndLine:     11,
			Replacement: "replacement",
		},
	}
	base.FinalizeID()
	if !findingIDPattern.MatchString(base.FindingID) {
		t.Fatalf("FindingID = %q", base.FindingID)
	}

	mutable := base.Clone()
	mutable.Title = "lost update"
	mutable.Message = "Changed message."
	mutable.Suggestion = "Changed suggestion."
	mutable.Severity = SeverityLow
	mutable.Confidence = 0.1
	mutable.ProposedFix.Description = "Changed fix."
	mutable.ProposedFix.Replacement = "different"
	mutable.FinalizeID()
	if mutable.FindingID != base.FindingID {
		t.Fatalf("mutable content changed ID: %q != %q", mutable.FindingID, base.FindingID)
	}

	identityChanges := []struct {
		name   string
		mutate func(*Finding)
	}{
		{name: "source", mutate: func(f *Finding) { f.Source = SourceStaticAnalysis }},
		{name: "rule", mutate: func(f *Finding) { f.RuleID = "ai/security" }},
		{name: "agent", mutate: func(f *Finding) { f.AgentID = "security" }},
		{name: "category", mutate: func(f *Finding) { f.Category = CategoryQuality }},
		{name: "file", mutate: func(f *Finding) { f.File = "other.go" }},
		{name: "start", mutate: func(f *Finding) { f.StartLine = 9 }},
		{name: "end", mutate: func(f *Finding) { f.EndLine = 12 }},
		{name: "title", mutate: func(f *Finding) { f.Title = "Different issue" }},
	}
	for _, change := range identityChanges {
		identity := base.Clone()
		change.mutate(&identity)
		identity.FinalizeID()
		if identity.FindingID == base.FindingID {
			t.Errorf("%s did not change finding ID", change.name)
		}
	}
}

func TestFindingValidateProposedFix(t *testing.T) {
	t.Parallel()

	valid := Finding{
		RuleID:     "ai/correctness",
		File:       "main.go",
		StartLine:  3,
		EndLine:    4,
		Severity:   SeverityHigh,
		Category:   CategoryCorrectness,
		Title:      "Incorrect branch",
		Message:    "The branch returns the wrong value.",
		Confidence: 0.9,
		Source:     SourceAI,
		AgentID:    "correctness",
		ProposedFix: &ProposedFix{
			Description: "Replace both changed lines.",
			StartLine:   3,
			EndLine:     4,
			Replacement: "return true\nreturn nil",
		},
	}
	valid.FinalizeID()
	if err := valid.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*ProposedFix)
		want   string
	}{
		{name: "blank description", mutate: func(f *ProposedFix) { f.Description = " \t" }, want: "description"},
		{name: "long description", mutate: func(f *ProposedFix) { f.Description = strings.Repeat("x", MaxFixDescriptionBytes+1) }, want: "description"},
		{name: "start mismatch", mutate: func(f *ProposedFix) { f.StartLine++ }, want: "exactly match"},
		{name: "end mismatch", mutate: func(f *ProposedFix) { f.EndLine-- }, want: "exactly match"},
		{name: "large replacement", mutate: func(f *ProposedFix) { f.Replacement = strings.Repeat("x", MaxFixReplacementBytes+1) }, want: "byte limit"},
		{name: "NUL description", mutate: func(f *ProposedFix) { f.Description = "replace\x00line" }, want: "NUL"},
		{name: "NUL replacement", mutate: func(f *ProposedFix) { f.Replacement = "safe\x00unsafe" }, want: "NUL"},
		{name: "description placeholder", mutate: func(f *ProposedFix) { f.Description = "Use [REDACTED_SECRET]." }, want: "placeholder"},
		{name: "redaction placeholder", mutate: func(f *ProposedFix) { f.Replacement = "token = [REDACTED_SECRET]" }, want: "placeholder"},
		{name: "truncation placeholder", mutate: func(f *ProposedFix) {
			f.Replacement = "[reviewer: long diff line truncated to respect the token budget]"
		}, want: "placeholder"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			finding := valid.Clone()
			test.mutate(finding.ProposedFix)
			if err := finding.Validate(); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Validate() error = %v, want %q", err, test.want)
			}
		})
	}

	deletion := valid.Clone()
	deletion.ProposedFix.Replacement = ""
	if err := deletion.Validate(); err != nil {
		t.Fatalf("empty deletion replacement rejected: %v", err)
	}
}

func TestSortOrdersBySeverityThenLocation(t *testing.T) {
	t.Parallel()

	items := []Finding{
		{Severity: SeverityLow, File: "a.go", StartLine: 1, Title: "low"},
		{Severity: SeverityHigh, File: "b.go", StartLine: 2, Title: "later"},
		{Severity: SeverityHigh, File: "a.go", StartLine: 3, Title: "first file"},
		{Severity: SeverityCritical, File: "z.go", StartLine: 9, Title: "critical"},
	}
	Sort(items)

	wantTitles := []string{"critical", "first file", "later", "low"}
	for index, want := range wantTitles {
		if items[index].Title != want {
			t.Errorf("items[%d].Title = %q, want %q", index, items[index].Title, want)
		}
	}
}
