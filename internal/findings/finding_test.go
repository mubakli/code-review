package findings

import (
	"math"
	"strings"
	"testing"
)

func TestFindingValidate(t *testing.T) {
	t.Parallel()

	valid := Finding{
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
	if err := valid.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*Finding)
		want   string
	}{
		{name: "file", mutate: func(f *Finding) { f.File = "" }, want: "file"},
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
