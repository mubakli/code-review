package git

import (
	"strings"
	"testing"

	"code-review/internal/change"
)

func TestParseFilesAndHunks(t *testing.T) {
	t.Parallel()

	patch := strings.Join([]string{
		"diff --git a/service.go b/service.go",
		"index 1111111..2222222 100644",
		"--- a/service.go",
		"+++ b/service.go",
		"@@ -10,3 +10,4 @@ func run() {",
		" keepBefore()",
		"-oldCall()",
		"+newCall()",
		"+audit()",
		" keepAfter()",
		`diff --git "a/config file.go" "b/config file.go"`,
		"new file mode 100644",
		"--- /dev/null",
		`+++ "b/config file.go"`,
		"@@ -0,0 +1,2 @@",
		"+package config",
		"+const Enabled = true",
		"diff --git a/old.go b/old.go",
		"deleted file mode 100644",
		"--- a/old.go",
		"+++ /dev/null",
		"@@ -1 +0,0 @@",
		"-package old",
		"diff --git a/logo.png b/logo.png",
		"new file mode 100644",
		"Binary files /dev/null and b/logo.png differ",
	}, "\n")

	changes, err := Parse([]byte(patch))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(changes.Files) != 4 {
		t.Fatalf("len(Files) = %d, want 4", len(changes.Files))
	}

	modified := changes.Files[0]
	if modified.Path() != "service.go" || modified.Status != change.StatusModified {
		t.Fatalf("modified file = %#v", modified)
	}
	if len(modified.Hunks) != 1 || len(modified.Hunks[0].Lines) != 5 {
		t.Fatalf("modified hunks = %#v", modified.Hunks)
	}
	added := modified.Hunks[0].Lines[3]
	if added.Kind != change.LineAdded || added.NewLine != 12 || added.Content != "audit()" {
		t.Fatalf("added line = %#v", added)
	}

	if file := changes.Files[1]; file.Path() != "config file.go" || file.OldPath != "" || file.Status != change.StatusAdded {
		t.Errorf("added file = %#v", file)
	}
	if file := changes.Files[2]; file.Path() != "old.go" || file.NewPath != "" || file.Status != change.StatusDeleted {
		t.Errorf("deleted file = %#v", file)
	}
	if file := changes.Files[3]; file.Path() != "logo.png" || file.OldPath != "" || file.Status != change.StatusAdded || !file.Binary {
		t.Errorf("binary file = %#v", file)
	}
}

func TestParseRenameCopyAndQuotedPaths(t *testing.T) {
	t.Parallel()

	patch := strings.Join([]string{
		`diff --git "a/caf\303\251.go" "b/caf\303\251.go"`,
		`--- "a/caf\303\251.go"`,
		`+++ "b/caf\303\251.go"`,
		"@@ -1 +1 @@",
		"-old",
		"+new",
		"diff --git a/old name.go b/new name.go",
		"similarity index 100%",
		"rename from old name.go",
		"rename to new name.go",
		"diff --git a/source.go b/copy.go",
		"similarity index 100%",
		"copy from source.go",
		"copy to copy.go",
	}, "\n")

	changes, err := Parse([]byte(patch))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if got := changes.Files[0].Path(); got != "café.go" {
		t.Errorf("quoted Path() = %q, want café.go", got)
	}
	if file := changes.Files[1]; file.Status != change.StatusRenamed || file.OldPath != "old name.go" || file.NewPath != "new name.go" {
		t.Errorf("renamed file = %#v", file)
	}
	if file := changes.Files[2]; file.Status != change.StatusCopied || file.OldPath != "source.go" || file.NewPath != "copy.go" {
		t.Errorf("copied file = %#v", file)
	}
}

func TestParsePreservesNoNewlineMarkerAndCR(t *testing.T) {
	t.Parallel()

	patch := "diff --git a/a.txt b/a.txt\n--- a/a.txt\n+++ b/a.txt\n@@ -1 +1 @@\n-old\r\n\\ No newline at end of file\n+new\r\n\\ No newline at end of file\n"
	changes, err := Parse([]byte(patch))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	lines := changes.Files[0].Hunks[0].Lines
	if lines[0].Content != "old\r" || lines[1].Content != "new\r" {
		t.Fatalf("line endings were not preserved: %#v", lines)
	}
}

func TestParseRejectsMalformedDiffs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		patch string
		want  string
	}{
		{
			name:  "incomplete hunk",
			patch: "diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n@@ -1,2 +1 @@\n-old\n+new\n",
			want:  "hunk line count mismatch",
		},
		{
			name:  "excess hunk line",
			patch: "diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n@@ -1 +1 @@\n-old\n+new\n+extra\n",
			want:  "more lines than declared",
		},
		{
			name:  "unterminated path",
			patch: `diff --git "a/unterminated b/path.go` + "\n",
			want:  "unterminated quoted path",
		},
		{
			name:  "content before header",
			patch: "unexpected\n",
			want:  "before file header",
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := Parse([]byte(test.patch))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Parse() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestParseEmpty(t *testing.T) {
	t.Parallel()

	changes, err := Parse(nil)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if changes.Files == nil || len(changes.Files) != 0 {
		t.Fatalf("Files = %#v, want non-nil empty slice", changes.Files)
	}
}
