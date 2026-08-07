package pathfilter

import "testing"

func TestDefaultMatcher(t *testing.T) {
	t.Parallel()

	matcher := New(DefaultPatterns())
	tests := []struct {
		path     string
		excluded bool
	}{
		{path: ".env", excluded: true},
		{path: "config/.env.local", excluded: true},
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
