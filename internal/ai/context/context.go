package context

import (
	stdcontext "context"

	"code-review/internal/change"
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
// it. RelatedTo names the changed files whose diff symbols this context file
// references; when non-empty the file is attached only to batches covering one
// of those changed files, so a multi-batch diff never duplicates the same
// surrounding code into every batch. An empty RelatedTo attaches to every batch.
type ContextFile struct {
	Path      string   `json:"path"`
	Content   string   `json:"content"`
	RelatedTo []string `json:"relatedTo,omitempty"`
}

// ContextIntent names the kind of surrounding code a deep specialist may need
// in addition to the diff itself. Intents are resolved from the routing
// assessment (context-on-demand): the resolver expands only the areas the
// escalated surfaces point at.
type ContextIntent string

const (
	// ContextIntentRoute resolves route registration (paths, methods,
	// handlers).
	ContextIntentRoute ContextIntent = "route"
	// ContextIntentMiddleware resolves middleware, filters, and interceptors.
	ContextIntentMiddleware ContextIntent = "middleware"
	// ContextIntentController resolves controller/handler implementations.
	ContextIntentController ContextIntent = "controller"
	// ContextIntentService resolves service and application-layer logic.
	ContextIntentService ContextIntent = "service"
	// ContextIntentRepository resolves persistence and data access.
	ContextIntentRepository ContextIntent = "repository"
	// ContextIntentAuthorization resolves authorization policies, permission
	// and owner checks for accessing an object.
	ContextIntentAuthorization ContextIntent = "authorization"
	// ContextIntentOwnership resolves the entity that owns an object, tracking
	// who may access it.
	ContextIntentOwnership ContextIntent = "ownership"
)

// ContextRequest describes what related context is needed for a deep specialist
// review. Paths are repository-relative files the diff already points to;
// Symbols are identifiers gathered from routing context; Intent is the
// surrounding layer to expand.
type ContextRequest struct {
	Symbols []string
	Paths   []string
	Intent  ContextIntent
}

// RepositoryContext is the resolved related code handed to a deep specialist
// review. A nil Files is valid: context is advisory and the diff review
// proceeds without it.
type RepositoryContext struct {
	Files []ContextFile
}

// ContextResolver supplies related staged code for deep specialist review. It
// is invoked only after deterministic signals or a routing decision escalate,
// and it resolves on demand: the request names only the areas the escalated
// surfaces actually need.
type ContextResolver interface {
	Resolve(ctx stdcontext.Context, changes change.ChangeSet, request ContextRequest) (RepositoryContext, error)
}
