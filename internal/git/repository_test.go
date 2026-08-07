package git

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenAndStagedDiff(t *testing.T) {
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

	patch, err := repository.StagedDiff(context.Background())
	if err != nil {
		t.Fatalf("StagedDiff() error = %v", err)
	}
	diff := string(patch)
	if !strings.Contains(diff, "+const staged = true") {
		t.Errorf("staged diff does not contain staged line:\n%s", diff)
	}
	if strings.Contains(diff, "unstaged") {
		t.Errorf("staged diff contains unstaged line:\n%s", diff)
	}
}

func TestStagedDiffSupportsRepositoryWithoutCommits(t *testing.T) {
	requireGit(t)

	root := t.TempDir()
	runTestGit(t, root, "init", "--quiet")
	writeTestFile(t, filepath.Join(root, "new.go"), "package sample\n")
	runTestGit(t, root, "add", "--", "new.go")

	repository, err := Open(context.Background(), root)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	patch, err := repository.StagedDiff(context.Background())
	if err != nil {
		t.Fatalf("StagedDiff() error = %v", err)
	}
	if !strings.Contains(string(patch), "diff --git a/new.go b/new.go") {
		t.Fatalf("unexpected staged patch:\n%s", patch)
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
	patch, err := repository.StagedDiff(context.Background())
	if err != nil {
		t.Fatalf("StagedDiff() error = %v", err)
	}
	if strings.Contains(string(patch), "other.go") {
		t.Fatalf("staged diff was redirected by ambient Git variables:\n%s", patch)
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
