package ai

import "context"

const (
	// MaxContextFileBytes bounds one related staged file attached to a deep
	// security review request.
	MaxContextFileBytes = 16 << 10
	// MaxContextTotalBytes bounds all related staged files in one request.
	MaxContextTotalBytes   = 32 << 10
	contextTruncatedMarker = "\n[reviewer: context truncated to respect the context budget]\n"
)

// ContextFile is related staged file content resolved locally for deep review.
// Content is always redacted by the Builder before a provider can observe it.
type ContextFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// ContextResolver supplies related staged code for deep specialist review. It
// is invoked only after deterministic signals or security triage escalate.
type ContextResolver interface {
	ResolveStagedContext(ctx context.Context, paths []string) ([]ContextFile, error)
}
