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

	"code-review/internal/change"
)

const (
	maxRepositoryProbeBytes = 64 << 10
	maxStagedDiffBytes      = 32 << 20
	maxGitStderrBytes       = 1 << 20
)

var (
	ErrNotRepository = errors.New("not a Git repository")
	ErrDiffTooLarge  = errors.New("staged diff exceeds the 32 MiB safety limit")
	errOutputLimit   = errors.New("Git output exceeds safety limit")
)

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

	rootOutput, stderr, err := run(ctx, startDirectory, maxRepositoryProbeBytes, "rev-parse", "--show-toplevel")
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

// StagedChanges returns parsed changes from the Git index.
func (r *Repository) StagedChanges(ctx context.Context) (change.ChangeSet, error) {
	patch, err := r.stagedDiff(ctx)
	if err != nil {
		return change.ChangeSet{}, err
	}
	changes, err := Parse(patch)
	if err != nil {
		return change.ChangeSet{}, fmt.Errorf("parse staged diff: %w", err)
	}
	return changes, nil
}

func (r *Repository) stagedDiff(ctx context.Context) ([]byte, error) {
	patch, stderr, err := run(
		ctx,
		r.root,
		maxStagedDiffBytes,
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
		if errors.Is(err, errOutputLimit) {
			return nil, ErrDiffTooLarge
		}
		return nil, commandError("read staged diff", stderr, err)
	}
	return patch, nil
}

func run(ctx context.Context, directory string, outputLimit int, arguments ...string) ([]byte, string, error) {
	commandArguments := append([]string{"-C", directory}, arguments...)
	command := exec.CommandContext(ctx, "git", commandArguments...)
	command.Env = gitEnvironment()

	stdout := limitedBuffer{limit: outputLimit, failOnLimit: true}
	stderr := limitedBuffer{limit: maxGitStderrBytes}
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	if stdout.exceeded {
		return nil, strings.TrimSpace(stderr.String()), errOutputLimit
	}
	if err != nil {
		return nil, strings.TrimSpace(stderr.String()), err
	}
	return stdout.Bytes(), strings.TrimSpace(stderr.String()), nil
}

func gitEnvironment() []string {
	blocked := map[string]struct{}{
		"GIT_ALTERNATE_OBJECT_DIRECTORIES": {},
		"GIT_CEILING_DIRECTORIES":          {},
		"GIT_COMMON_DIR":                   {},
		"GIT_CONFIG_COUNT":                 {},
		"GIT_CONFIG_PARAMETERS":            {},
		"GIT_DIR":                          {},
		"GIT_DISCOVERY_ACROSS_FILESYSTEM":  {},
		"GIT_INDEX_FILE":                   {},
		"GIT_NAMESPACE":                    {},
		"GIT_OBJECT_DIRECTORY":             {},
		"GIT_OPTIONAL_LOCKS":               {},
		"GIT_PREFIX":                       {},
		"GIT_WORK_TREE":                    {},
		"LC_ALL":                           {},
	}

	environment := make([]string, 0, len(os.Environ())+2)
	for _, value := range os.Environ() {
		key, _, _ := strings.Cut(value, "=")
		key = strings.ToUpper(key)
		if _, remove := blocked[key]; remove || strings.HasPrefix(key, "GIT_CONFIG_KEY_") || strings.HasPrefix(key, "GIT_CONFIG_VALUE_") {
			continue
		}
		environment = append(environment, value)
	}
	return append(environment, "GIT_OPTIONAL_LOCKS=0", "LC_ALL=C")
}

type limitedBuffer struct {
	buffer      bytes.Buffer
	limit       int
	failOnLimit bool
	exceeded    bool
}

func (b *limitedBuffer) Write(value []byte) (int, error) {
	if b.exceeded {
		if b.failOnLimit {
			return 0, errOutputLimit
		}
		return len(value), nil
	}

	remaining := b.limit - b.buffer.Len()
	if len(value) <= remaining {
		return b.buffer.Write(value)
	}
	originalLength := len(value)
	written := 0
	if remaining > 0 {
		written, _ = b.buffer.Write(value[:remaining])
	}
	b.exceeded = true
	if b.failOnLimit {
		return written, errOutputLimit
	}
	return originalLength, nil
}

func (b *limitedBuffer) Bytes() []byte {
	return b.buffer.Bytes()
}

func (b *limitedBuffer) String() string {
	if b.exceeded {
		return b.buffer.String() + " [output truncated]"
	}
	return b.buffer.String()
}

func commandError(action, stderr string, err error) error {
	if stderr == "" {
		return fmt.Errorf("%s: %w", action, err)
	}
	return fmt.Errorf("%s: %w: %s", action, err, stderr)
}
