package git

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"code-review/internal/change"
)

func TestOpenAndStagedChanges(t *testing.T) {
	requireGit(t)

	root := t.TempDir()
	runTestGit(t, root, "init", "--quiet")
	runTestGit(t, root, "config", "user.email", "reviewer@example.invalid")
	runTestGit(t, root, "config", "user.name", "Reviewer Test")

	filePath := filepath.Join(root, "main.go")
	writeTestFile(t, filePath, "package main\n")
	runTestGit(t, root, "add", "--", "main.go")
	runTestGit(t, root, "commit", "--quiet", "-m", "initial")

	writeTestFile(t, filePath, "package main\n\nconst staged = true\n")
	runTestGit(t, root, "add", "--", "main.go")
	writeTestFile(t, filePath, "package main\n\nconst staged = true\nconst unstaged = true\n")

	nested := filepath.Join(root, "internal", "example")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("create nested directory: %v", err)
	}
	repository, err := Open(context.Background(), nested)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	wantRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatalf("resolve repository fixture: %v", err)
	}
	if repository.Root() != wantRoot {
		t.Fatalf("Root() = %q, want %q", repository.Root(), wantRoot)
	}

	changes, err := repository.StagedChanges(context.Background())
	if err != nil {
		t.Fatalf("StagedChanges() error = %v", err)
	}
	if !changeSetContains(changes, "const staged = true") {
		t.Errorf("staged changes do not contain staged line: %#v", changes)
	}
	if changeSetContains(changes, "unstaged") {
		t.Errorf("staged changes contain unstaged line: %#v", changes)
	}
}

func TestStagedChangesSupportsRepositoryWithoutCommits(t *testing.T) {
	requireGit(t)

	root := t.TempDir()
	runTestGit(t, root, "init", "--quiet")
	writeTestFile(t, filepath.Join(root, "new.go"), "package sample\n")
	runTestGit(t, root, "add", "--", "new.go")

	repository, err := Open(context.Background(), root)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	changes, err := repository.StagedChanges(context.Background())
	if err != nil {
		t.Fatalf("StagedChanges() error = %v", err)
	}
	if len(changes.Files) != 1 || changes.Files[0].Path() != "new.go" {
		t.Fatalf("unexpected staged changes: %#v", changes)
	}
}

func TestStagedSnapshotIDTracksOnlyIndexChanges(t *testing.T) {
	requireGit(t)

	root := t.TempDir()
	runTestGit(t, root, "init", "--quiet")
	filePath := filepath.Join(root, "main.go")
	writeTestFile(t, filePath, "package main\n")
	runTestGit(t, root, "add", "--", "main.go")
	repository, err := Open(context.Background(), root)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	first, err := repository.StagedSnapshot(context.Background())
	if err != nil {
		t.Fatalf("StagedSnapshot() error = %v", err)
	}
	if !strings.HasPrefix(first.ID, "sha256:") {
		t.Fatalf("snapshot ID = %q", first.ID)
	}

	writeTestFile(t, filePath, "package main\n\nconst unstaged = true\n")
	unstaged, err := repository.StagedSnapshot(context.Background())
	if err != nil {
		t.Fatalf("StagedSnapshot() after working-tree change error = %v", err)
	}
	if unstaged.ID != first.ID {
		t.Fatalf("unstaged edit changed snapshot ID: %q != %q", unstaged.ID, first.ID)
	}

	runTestGit(t, root, "add", "--", "main.go")
	second, err := repository.StagedSnapshot(context.Background())
	if err != nil {
		t.Fatalf("StagedSnapshot() after staging error = %v", err)
	}
	if second.ID == first.ID {
		t.Fatal("staged edit did not change snapshot ID")
	}
}

func TestOpenOutsideRepository(t *testing.T) {
	requireGit(t)

	_, err := Open(context.Background(), t.TempDir())
	if !errors.Is(err, ErrNotRepository) {
		t.Fatalf("Open() error = %v, want ErrNotRepository", err)
	}
}

func TestOpenHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := Open(ctx, ".")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Open() error = %v, want context.Canceled", err)
	}
}

func TestOpenIgnoresRepositorySelectingEnvironment(t *testing.T) {
	requireGit(t)

	target := t.TempDir()
	other := t.TempDir()
	runTestGit(t, target, "init", "--quiet")
	runTestGit(t, other, "init", "--quiet")
	writeTestFile(t, filepath.Join(other, "other.go"), "package other\n")
	runTestGit(t, other, "add", "--", "other.go")

	t.Setenv("GIT_DIR", filepath.Join(other, ".git"))
	t.Setenv("GIT_WORK_TREE", other)
	t.Setenv("GIT_INDEX_FILE", filepath.Join(other, ".git", "index"))

	repository, err := Open(context.Background(), target)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	wantRoot, err := filepath.EvalSymlinks(target)
	if err != nil {
		t.Fatalf("resolve target repository: %v", err)
	}
	if repository.Root() != wantRoot {
		t.Fatalf("Root() = %q, want %q", repository.Root(), wantRoot)
	}
	changes, err := repository.StagedChanges(context.Background())
	if err != nil {
		t.Fatalf("StagedChanges() error = %v", err)
	}
	if len(changes.Files) != 0 {
		t.Fatalf("staged changes were redirected by ambient Git variables: %#v", changes)
	}
}

func TestGitEnvironmentRemovesReviewerAPIKey(t *testing.T) {
	t.Setenv("REVIEWER_OPENAI_API_KEY", "must-not-reach-git")
	t.Setenv("REVIEWER_DEEPSEEK_API_KEY", "must-not-reach-git")

	for _, value := range gitEnvironment() {
		key, _, _ := strings.Cut(value, "=")
		if strings.EqualFold(key, "REVIEWER_OPENAI_API_KEY") || strings.EqualFold(key, "REVIEWER_DEEPSEEK_API_KEY") {
			t.Fatalf("git environment contains reviewer API key %q", key)
		}
	}
}

func TestRunBoundsGitOutput(t *testing.T) {
	requireGit(t)

	root := t.TempDir()
	runTestGit(t, root, "init", "--quiet")
	_, _, err := run(context.Background(), root, 4, "rev-parse", "--show-toplevel")
	if !errors.Is(err, errOutputLimit) {
		t.Fatalf("run() error = %v, want errOutputLimit", err)
	}
}

func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
}

func runTestGit(t *testing.T, root string, arguments ...string) {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", root}, arguments...)...)
	command.Env = append(os.Environ(), "LC_ALL=C")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(arguments, " "), err, output)
	}
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write test file: %v", err)
	}
}

func changeSetContains(changes change.ChangeSet, content string) bool {
	for _, file := range changes.Files {
		for _, hunk := range file.Hunks {
			for _, line := range hunk.Lines {
				if strings.Contains(line.Content, content) {
					return true
				}
			}
		}
	}
	return false
}
