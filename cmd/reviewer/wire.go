package main

import (
	stdcontext "context"
	"fmt"
	"os"
	"sort"
	"strings"

	"code-review/internal/ai"
	aicontext "code-review/internal/ai/context"
	"code-review/internal/ai/egress"
	"code-review/internal/ai/provider"
	"code-review/internal/ai/request"
	"code-review/internal/analyzers/secrets"
	"code-review/internal/change"
	"code-review/internal/config"
	"code-review/internal/git"
	"code-review/internal/pathfilter"
	"code-review/internal/review"
)

const (
	openAIAPIKeyEnvironment   = "REVIEWER_OPENAI_API_KEY"
	deepSeekAPIKeyEnvironment = "REVIEWER_DEEPSEEK_API_KEY"
)

type reviewOptions struct {
	ExtraExcludes []string
	AI            config.AI
	Provider      provider.Provider
	ExpectedID    string
}

// reviewStaged is the composition root for the staged-review use case. Concrete
// adapters belong here rather than inside the application service.
func reviewStaged(ctx stdcontext.Context, repositoryPath string, options reviewOptions) (review.Result, error) {
	if err := options.AI.Validate(); err != nil {
		return review.Result{}, err
	}
	repository, err := git.Open(ctx, repositoryPath)
	if err != nil {
		return review.Result{}, err
	}
	snapshot, err := repository.StagedSnapshot(ctx)
	if err != nil {
		return review.Result{}, err
	}
	if options.ExpectedID != "" && options.ExpectedID != snapshot.ID {
		return review.Result{}, fmt.Errorf("staged snapshot changed: expected %s, found %s", options.ExpectedID, snapshot.ID)
	}
	changes := snapshot.Changes

	patterns := pathfilter.DefaultPatterns()
	patterns = append(patterns, options.ExtraExcludes...)
	service := review.New(
		pathfilter.New(patterns),
		secrets.Analyzer{},
	)
	scope, err := service.ScopeChanges(ctx, changes)
	if err != nil {
		return review.Result{}, err
	}
	result, err := service.ReviewScope(ctx, scope)
	result.ReviewID = snapshot.ID
	if err != nil || !options.AI.Enabled() {
		return result, err
	}

	providerInstance := options.Provider
	if providerInstance == nil {
		providerInstance, err = configuredProvider(options.AI)
		if err != nil {
			return review.Result{}, err
		}
	}
	preparer, err := aicontext.NewPreparer(aicontext.DefaultBudget())
	if err != nil {
		return review.Result{}, fmt.Errorf("configure AI context preparer: %w", err)
	}
	agents, err := ai.SelectAgents(options.AI.Agents)
	if err != nil {
		return review.Result{}, fmt.Errorf("configure AI review agents: %w", err)
	}
	egressPolicy, err := egress.New(egress.DefaultRules())
	if err != nil {
		return review.Result{}, fmt.Errorf("configure AI egress policy: %w", err)
	}
	orchestrator, err := ai.NewOrchestratorWithAgentsAndResolver(preparer, request.RequestBuilder{}, providerInstance, agents, stagedContextResolver{repository: repository, egress: egressPolicy})
	if err != nil {
		return review.Result{}, fmt.Errorf("configure AI orchestrator: %w", err)
	}
	// The egress policy is the first gate in the provider pipeline: content
	// denied by the policy never reaches the preparation or provider layers.
	aiResult, err := orchestrator.Review(ctx, egressPolicy.FilterChanges(scope.Changes()), result.Findings)
	if err != nil {
		return review.Result{}, fmt.Errorf("run AI review: %w", err)
	}
	result.Findings = aiResult.Findings
	result.Summary.FindingCount = len(result.Findings)
	failures := make([]review.AIFailure, 0, len(aiResult.Failures))
	for _, failure := range aiResult.Failures {
		failures = append(failures, review.AIFailure{
			AgentID: failure.AgentID,
			Batch:   failure.Batch,
			Files:   append([]string(nil), failure.Files...),
			Message: failure.Message,
		})
	}
	result.AI = &review.AISummary{
		Provider:          string(options.AI.Provider),
		Model:             options.AI.Model,
		Agents:            append([]string(nil), aiResult.Agents...),
		ReviewedFiles:     append([]string(nil), aiResult.ReviewedFiles...),
		BatchCount:        aiResult.BatchCount,
		SuccessfulBatches: aiResult.SuccessfulBatches,
		FailedBatches:     len(aiResult.Failures),
		Failures:          failures,
	}
	return result, nil
}

func configuredProvider(providerConfig config.AI) (provider.Provider, error) {
	switch providerConfig.Provider {
	case config.AIProviderOpenAI:
		apiKey := strings.TrimSpace(os.Getenv(openAIAPIKeyEnvironment))
		if apiKey == "" {
			return nil, fmt.Errorf("%s is required for OpenAI review", openAIAPIKeyEnvironment)
		}
		providerInstance, err := provider.NewOpenAI(provider.OpenAIOptions{
			APIKey:          apiKey,
			Model:           providerConfig.Model,
			MaxOutputTokens: providerConfig.MaxOutputTokens,
		})
		if err != nil {
			return nil, fmt.Errorf("configure OpenAI provider: %w", err)
		}
		return providerInstance, nil
	case config.AIProviderDeepSeek:
		apiKey := strings.TrimSpace(os.Getenv(deepSeekAPIKeyEnvironment))
		if apiKey == "" {
			return nil, fmt.Errorf("%s is required for DeepSeek review", deepSeekAPIKeyEnvironment)
		}
		providerInstance, err := provider.NewDeepSeek(provider.DeepSeekOptions{
			APIKey:          apiKey,
			Model:           providerConfig.Model,
			MaxOutputTokens: providerConfig.MaxOutputTokens,
		})
		if err != nil {
			return nil, fmt.Errorf("configure DeepSeek provider: %w", err)
		}
		return providerInstance, nil
	default:
		return nil, fmt.Errorf("unsupported enabled AI provider %q", providerConfig.Provider)
	}
}

type stagedContextResolver struct {
	repository *git.Repository
	egress     egress.Policy
}

// maxContextSymbols bounds how many diff symbols one context request searches
// the index for, and maxRelatedContextFiles bounds how many related files a
// deep specialist may receive: the diff shows the changed files, so context is
// only the 1-3 surrounding files the symbols actually point at.
const (
	maxContextSymbols      = 8
	maxRelatedContextFiles = 3
)

// Resolve supplies related staged code on demand. The diff already shows the
// changed files, so they are never re-sent as context: instead the resolver
// searches the index for the diff's symbols (identifiers, imports, callees),
// excludes the changed files themselves and anything the egress policy denies,
// prefers the surrounding layer the intent names, and returns at most three
// related files. The model never receives filesystem access: it can only ask
// for symbols, and the resolver answers with redacted, budgeted content.
func (r stagedContextResolver) Resolve(ctx stdcontext.Context, changes change.ChangeSet, request aicontext.ContextRequest) (aicontext.RepositoryContext, error) {
	resolved := make([]aicontext.ContextFile, 0, maxRelatedContextFiles)
	seen := make(map[string]struct{}, len(changes.Files)+len(request.Paths))
	for _, file := range changes.Files {
		seen[file.Path()] = struct{}{}
	}
	for _, path := range request.Paths {
		if err := ctx.Err(); err != nil {
			return aicontext.RepositoryContext{}, err
		}
		if _, duplicate := seen[path]; duplicate {
			continue
		}
		seen[path] = struct{}{}
		if file, ok := r.stagedFile(ctx, path); ok {
			resolved = append(resolved, file)
		}
	}
	if len(resolved) >= maxRelatedContextFiles {
		return aicontext.RepositoryContext{Files: resolved}, nil
	}
	symbols := request.Symbols
	if len(symbols) > maxContextSymbols {
		symbols = symbols[:maxContextSymbols]
	}
	if len(symbols) == 0 {
		return aicontext.RepositoryContext{Files: resolved}, nil
	}
	related, err := r.symbolFiles(ctx, changes, request.Intent, symbols, seen)
	if err != nil {
		return aicontext.RepositoryContext{Files: resolved}, nil // context is advisory
	}
	return aicontext.RepositoryContext{Files: append(resolved, related...)}, nil
}

func (r stagedContextResolver) stagedFile(ctx stdcontext.Context, path string) (aicontext.ContextFile, bool) {
	if !r.egress.Allow(path) {
		return aicontext.ContextFile{}, false
	}
	content, err := r.repository.StagedFileContent(ctx, path)
	if err != nil {
		return aicontext.ContextFile{}, false
	}
	return aicontext.ContextFile{Path: path, Content: content}, true
}

// symbolFiles finds the files whose index content references the diff's
// symbols, ranked by how many references they hold. Files matching the intent
// layer come first, then the most referenced files; changed files and
// egress-denied files are excluded. A deterministic path tiebreak keeps the
// selection stable.
func (r stagedContextResolver) symbolFiles(ctx stdcontext.Context, changes change.ChangeSet, intent aicontext.ContextIntent, symbols []string, seen map[string]struct{}) ([]aicontext.ContextFile, error) {
	matches, err := r.repository.GrepIndex(ctx, symbols)
	if err != nil {
		return nil, err
	}
	markers := intentPathMarkers[intent]
	type candidate struct {
		path  string
		count int
		marks bool
	}
	candidates := make([]candidate, 0, len(matches))
	for path, count := range matches {
		if _, duplicate := seen[path]; duplicate {
			continue
		}
		if !r.egress.Allow(path) {
			continue
		}
		candidates = append(candidates, candidate{path: path, count: count, marks: matchesIntent(path, markers, symbols)})
	}
	sort.Slice(candidates, func(i, j int) bool {
		left, right := candidates[i], candidates[j]
		if left.marks != right.marks {
			return left.marks
		}
		if left.count != right.count {
			return left.count > right.count
		}
		return left.path < right.path
	})
	remaining := maxRelatedContextFiles - len(seen)
	if remaining > 0 && len(candidates) > remaining {
		candidates = candidates[:remaining]
	}
	files := make([]aicontext.ContextFile, 0, len(candidates))
	for _, candidate := range candidates {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if file, ok := r.stagedFile(ctx, candidate.path); ok {
			files = append(files, file)
		}
	}
	return files, nil
}

func matchesIntent(path string, markers []string, symbols []string) bool {
	lower := strings.ToLower(path)
	for _, marker := range markers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	for _, symbol := range symbols {
		if symbol != "" && strings.Contains(lower, strings.ToLower(symbol)) {
			return true
		}
	}
	return false
}

// intentPathMarkers maps each surrounding layer to the path tokens that likely
// implement it, so context is resolved on demand instead of bundled blindly.
var intentPathMarkers = map[aicontext.ContextIntent][]string{
	aicontext.ContextIntentRoute:         {"route", "router", "endpoint", "urls."},
	aicontext.ContextIntentMiddleware:    {"middleware", "filter", "interceptor"},
	aicontext.ContextIntentController:    {"controller", "handler", "view."},
	aicontext.ContextIntentService:       {"service", "usecase", "application"},
	aicontext.ContextIntentRepository:    {"repository", "repositories", "repo", "store", "dao", "mapper"},
	aicontext.ContextIntentAuthorization: {"authoriz", "permission", "policy", "access", "principal", "role", "acl", "guard", "rbac"},
	aicontext.ContextIntentOwnership:     {"owner", "ownership", "tenant", "account"},
}
