package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

var ErrNotRepository = errors.New("not a Git repository")

// Repository is a local Git worktree discovered from a starting directory.
type Repository struct {
	root string
}

// Open locates the worktree containing startDirectory.
func Open(ctx context.Context, startDirectory string) (*Repository, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if startDirectory == "" {
		startDirectory = "."
	}

	rootOutput, stderr, err := run(ctx, startDirectory, "rev-parse", "--show-toplevel")
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		lowerStderr := strings.ToLower(stderr)
		if strings.Contains(lowerStderr, "not a git repository") || strings.Contains(lowerStderr, "must be run in a work tree") {
			return nil, fmt.Errorf("%w: %s", ErrNotRepository, startDirectory)
		}
		return nil, commandError("detect Git repository", stderr, err)
	}

	root := strings.TrimSuffix(string(rootOutput), "\n")
	root = strings.TrimSuffix(root, "\r")
	if root == "" {
		return nil, errors.New("detect Git repository: Git returned an empty worktree path")
	}
	root = filepath.Clean(root)
	if resolved, resolveErr := filepath.EvalSymlinks(root); resolveErr == nil {
		root = resolved
	}
	return &Repository{root: root}, nil
}

// Root returns the absolute repository root reported by Git.
func (r *Repository) Root() string {
	return r.root
}

// StagedDiff returns a deterministic, text-only unified diff of the index.
func (r *Repository) StagedDiff(ctx context.Context) ([]byte, error) {
	patch, stderr, err := run(
		ctx,
		r.root,
		"-c", "core.quotePath=true",
		"-c", "diff.external=",
		"-c", "diff.mnemonicPrefix=false",
		"-c", "diff.noprefix=false",
		"-c", "diff.outputIndicatorContext= ",
		"-c", "diff.outputIndicatorNew=+",
		"-c", "diff.outputIndicatorOld=-",
		"-c", "diff.suppressBlankEmpty=false",
		"diff",
		"--cached",
		"--no-ext-diff",
		"--no-textconv",
		"--no-color",
		"--find-renames=50%",
		"--diff-algorithm=myers",
		"--unified=3",
		"--src-prefix=a/",
		"--dst-prefix=b/",
		"--",
	)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, commandError("read staged diff", stderr, err)
	}
	return patch, nil
}

func run(ctx context.Context, directory string, arguments ...string) ([]byte, string, error) {
	commandArguments := append([]string{"-C", directory}, arguments...)
	command := exec.CommandContext(ctx, "git", commandArguments...)
	command.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0", "LC_ALL=C")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		return nil, strings.TrimSpace(stderr.String()), err
	}
	return stdout.Bytes(), strings.TrimSpace(stderr.String()), nil
}

func commandError(action, stderr string, err error) error {
	if stderr == "" {
		return fmt.Errorf("%s: %w", action, err)
	}
	return fmt.Errorf("%s: %w: %s", action, err, stderr)
}
