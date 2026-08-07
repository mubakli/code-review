package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"code-review/internal/ai"
	"code-review/internal/ai/providers/deepseek"
	"code-review/internal/ai/providers/openai"
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
	Provider      ai.Provider
	ExpectedID    string
}

// reviewStaged is the composition root for the staged-review use case. Concrete
// adapters belong here rather than inside the application service.
func reviewStaged(ctx context.Context, repositoryPath string, options reviewOptions) (review.Result, error) {
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

	provider := options.Provider
	if provider == nil {
		provider, err = configuredProvider(options.AI)
		if err != nil {
			return review.Result{}, err
		}
	}
	builder, err := ai.New(ai.DefaultBudget())
	if err != nil {
		return review.Result{}, fmt.Errorf("configure AI prompt builder: %w", err)
	}
	agents, err := ai.SelectAgents(options.AI.Agents)
	if err != nil {
		return review.Result{}, fmt.Errorf("configure AI review agents: %w", err)
	}
	orchestrator, err := ai.NewOrchestratorWithAgents(builder, provider, agents)
	if err != nil {
		return review.Result{}, fmt.Errorf("configure AI orchestrator: %w", err)
	}
	aiResult, err := orchestrator.Review(ctx, aiEgressChanges(scope.Changes()), result.Findings)
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

func aiEgressChanges(changes change.ChangeSet) change.ChangeSet {
	matcher := pathfilter.New(pathfilter.DefaultAIEgressPatterns())
	filtered := change.ChangeSet{Files: make([]change.FileChange, 0, len(changes.Files))}
	for _, file := range changes.Files {
		if !matcher.Excludes(file.Path()) {
			filtered.Files = append(filtered.Files, file)
		}
	}
	return filtered
}

func configuredProvider(providerConfig config.AI) (ai.Provider, error) {
	switch providerConfig.Provider {
	case config.AIProviderOpenAI:
		apiKey := strings.TrimSpace(os.Getenv(openAIAPIKeyEnvironment))
		if apiKey == "" {
			return nil, fmt.Errorf("%s is required for OpenAI review", openAIAPIKeyEnvironment)
		}
		provider, err := openai.New(openai.Options{
			APIKey:          apiKey,
			Model:           providerConfig.Model,
			MaxOutputTokens: providerConfig.MaxOutputTokens,
		})
		if err != nil {
			return nil, fmt.Errorf("configure OpenAI provider: %w", err)
		}
		return provider, nil
	case config.AIProviderDeepSeek:
		apiKey := strings.TrimSpace(os.Getenv(deepSeekAPIKeyEnvironment))
		if apiKey == "" {
			return nil, fmt.Errorf("%s is required for DeepSeek review", deepSeekAPIKeyEnvironment)
		}
		provider, err := deepseek.New(deepseek.Options{
			APIKey:          apiKey,
			Model:           providerConfig.Model,
			MaxOutputTokens: providerConfig.MaxOutputTokens,
		})
		if err != nil {
			return nil, fmt.Errorf("configure DeepSeek provider: %w", err)
		}
		return provider, nil
	default:
		return nil, fmt.Errorf("unsupported enabled AI provider %q", providerConfig.Provider)
	}
}
