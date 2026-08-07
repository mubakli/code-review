package pathfilter

import "testing"

func TestDefaultMatcher(t *testing.T) {
	t.Parallel()

	matcher := New(DefaultPatterns())
	tests := []struct {
		path     string
		excluded bool
	}{
		{path: ".env", excluded: false},
		{path: "config/.env.local", excluded: false},
		{path: ".env.production", excluded: false},
		{path: "web/node_modules/pkg/index.js", excluded: true},
		{path: "service/generated/client.go", excluded: true},
		{path: `web\dist\bundle.js`, excluded: true},
		{path: "service/vendor_name.go", excluded: false},
		{path: "internal/buildinfo/info.go", excluded: false},
		{path: "cmd/reviewer/main.go", excluded: false},
	}

	for _, test := range tests {
		test := test
		t.Run(test.path, func(t *testing.T) {
			t.Parallel()
			if got := matcher.Excludes(test.path); got != test.excluded {
				t.Fatalf("Excludes(%q) = %t, want %t", test.path, got, test.excluded)
			}
		})
	}
}

func TestDefaultAIEgressMatcher(t *testing.T) {
	t.Parallel()

	matcher := New(DefaultAIEgressPatterns())
	for _, file := range []string{".env", ".env.local", "config/.env.production", ".env.example"} {
		if !matcher.Excludes(file) {
			t.Errorf("AI egress matcher did not exclude %q", file)
		}
	}
	if matcher.Excludes("config/environment.go") {
		t.Fatal("AI egress matcher excluded a normal source file")
	}
}

func TestMatcherSupportsRepositoryRelativeDirectoryAndGlob(t *testing.T) {
	t.Parallel()

	matcher := New([]string{"testdata/fixtures/", "docs/*.md"})
	tests := []struct {
		path string
		want bool
	}{
		{path: "testdata/fixtures/generated.go", want: true},
		{path: "other/testdata/fixtures/generated.go", want: false},
		{path: "docs/design.md", want: true},
		{path: "docs/nested/design.md", want: false},
	}
	for _, test := range tests {
		if got := matcher.Excludes(test.path); got != test.want {
			t.Errorf("Excludes(%q) = %v, want %v", test.path, got, test.want)
		}
	}
}

func TestMatcherCopiesPatterns(t *testing.T) {
	t.Parallel()

	patterns := []string{"private/"}
	matcher := New(patterns)
	patterns[0] = "changed/"
	if !matcher.Excludes("private/value.txt") {
		t.Fatal("New() retained mutable caller storage")
	}
}

func TestValidatePattern(t *testing.T) {
	t.Parallel()

	for _, pattern := range []string{"private/", "docs/*.md", ".env.*"} {
		if err := ValidatePattern(pattern); err != nil {
			t.Errorf("ValidatePattern(%q) error = %v", pattern, err)
		}
	}
	for _, pattern := range []string{"", "   ", "private/["} {
		if err := ValidatePattern(pattern); err == nil {
			t.Errorf("ValidatePattern(%q) error = nil", pattern)
		}
	}
}
