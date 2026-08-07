package analyzer

import (
	"context"

	"code-review/internal/findings"
	"code-review/internal/gitdiff"
)

// Analyzer is a deterministic local analysis pass over staged changes.
type Analyzer interface {
	Name() string
	Analyze(ctx context.Context, changes gitdiff.ChangeSet) ([]findings.Finding, error)
}
