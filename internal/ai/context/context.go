package context

import (
	stdcontext "context"
)

const (
	// MaxContextFileBytes bounds one related staged file attached to a deep
	// specialist review request.
	MaxContextFileBytes = 16 << 10
	// MaxContextTotalBytes bounds all related staged files in one request.
	MaxContextTotalBytes   = 32 << 10
	contextTruncatedMarker = "\n[reviewer: context truncated to respect the context budget]\n"
)

// ContextFile is a related staged source file resolved locally for deep
// specialist review. Content is always redacted before a provider can observe
// it.
type ContextFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// ContextResolver supplies related staged code for deep specialist review. It
// is invoked only after deterministic signals or a routing decision escalate.
type ContextResolver interface {
	ResolveStagedContext(ctx stdcontext.Context, paths []string) ([]ContextFile, error)
}
