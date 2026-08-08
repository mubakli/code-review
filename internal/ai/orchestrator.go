package ai

import (
	"context"
	"fmt"
	"strings"

	"code-review/internal/change"
	"code-review/internal/findings"
)

type Orchestrator struct {
	builder  Builder
	provider Provider
	agents   []ReviewAgent
	resolver ContextResolver
}

type ReviewResult struct {
	Findings          []findings.Finding
	ReviewedFiles     []string
	Agents            []string
	BatchCount        int
	SuccessfulBatches int
	Failures          []BatchFailure
}

type BatchFailure struct {
	AgentID string
	Batch   int
	Files   []string
	Message string
}

func NewOrchestrator(builder Builder, provider Provider) (*Orchestrator, error) {
	return NewOrchestratorWithAgents(builder, provider, DefaultAgents())
}

func NewOrchestratorWithAgents(builder Builder, provider Provider, agents []ReviewAgent) (*Orchestrator, error) {
	return NewOrchestratorWithAgentsAndResolver(builder, provider, agents, nil)
}

func NewOrchestratorWithAgentsAndResolver(builder Builder, provider Provider, agents []ReviewAgent, resolver ContextResolver) (*Orchestrator, error) {
	if provider == nil {
		return nil, fmt.Errorf("AI provider is required")
	}
	if len(agents) == 0 {
		return nil, fmt.Errorf("at least one AI review agent is required")
	}
	return &Orchestrator{builder: builder, provider: provider, agents: append([]ReviewAgent(nil), agents...), resolver: resolver}, nil
}

func (o *Orchestrator) Review(ctx context.Context, changes change.ChangeSet, localFindings []findings.Finding) (ReviewResult, error) {
	result := ReviewResult{
		Findings:      findings.Merge(localFindings, nil),
		ReviewedFiles: make([]string, 0),
		Agents:        make([]string, 0),
		Failures:      make([]BatchFailure, 0),
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}

	eligibleLines := addedLines(changes)
	aiFindings := make([]findings.Finding, 0)
	batchIndex := 0

	for _, agent := range o.agents {
		if err := ctx.Err(); err != nil {
			result.Findings = findings.Merge(localFindings, aiFindings)
			return result, err
		}
		switch agent.ID {
		case AgentSecurity:
			deepFindings, deepIndex, err := o.reviewSecurity(ctx, changes, localFindings, eligibleLines, agent, &result, batchIndex)
			if err != nil {
				return result, err
			}
			aiFindings = append(aiFindings, deepFindings...)
			batchIndex = deepIndex
		default:
			v, idx, err := o.runFindingAgent(ctx, changes, localFindings, eligibleLines, agent, nil, &result, batchIndex)
			if err != nil {
				return result, err
			}
			aiFindings = append(aiFindings, v...)
			batchIndex = idx
			result.Agents = append(result.Agents, string(agent.ID))
		}
	}

	result.Findings = findings.Merge(localFindings, aiFindings)
	return result, nil
}

func (o *Orchestrator) reviewSecurity(ctx context.Context, changes change.ChangeSet, localFindings []findings.Finding, eligibleLines map[string]map[int]struct{}, agent ReviewAgent, result *ReviewResult, batchIndex int) ([]findings.Finding, int, error) {
	require := RequiresSecurityReview(changes)
	escalate := require
	if !require {
		escalate = o.runTriage(ctx, changes, localFindings, result, &batchIndex)
	}
	if !escalate {
		return nil, batchIndex, nil
	}
	var contextFiles []ContextFile
	if o.resolver != nil {
		paths := contextPaths(changes)
		resolved, err := o.resolver.ResolveStagedContext(ctx, paths)
		if err == nil && len(resolved) > 0 {
			contextFiles = resolved
		}
	}
	findings, idx, err := o.runFindingAgent(ctx, changes, localFindings, eligibleLines, agent, contextFiles, result, batchIndex)
	if err != nil {
		return nil, idx, err
	}
	result.Agents = append(result.Agents, string(AgentSecurity))
	return findings, idx, nil
}

func (o *Orchestrator) runTriage(ctx context.Context, changes change.ChangeSet, localFindings []findings.Finding, result *ReviewResult, batchIndex *int) bool {
	triageAgent := securityTriageAgent()
	builder, err := o.builder.ForAgent(triageAgent)
	if err != nil {
		result.Failures = append(result.Failures, newBatchFailure(AgentSecurityTriage, *batchIndex, nil, err))
		return true // fail closed
	}
	batches, err := builder.Build(ctx, changes, localFindings, nil)
	if err != nil {
		result.Failures = append(result.Failures, newBatchFailure(AgentSecurityTriage, *batchIndex, nil, err))
		return true
	}
	if len(batches) == 0 {
		return false
	}
	result.Agents = append(result.Agents, string(AgentSecurityTriage))
	result.BatchCount += len(batches)
	escalate := false
	for _, batch := range batches {
		if err := ctx.Err(); err != nil {
			return escalate
		}
		result.ReviewedFiles = appendUnique(result.ReviewedFiles, batch.Files...)
		response, err := o.provider.Triage(ctx, batch.Request)
		if err != nil {
			result.Failures = append(result.Failures, newBatchFailure(AgentSecurityTriage, *batchIndex, batch.Files, err))
			*batchIndex++
			escalate = true
			continue
		}
		if err := validateTriageResponse(response); err != nil {
			result.Failures = append(result.Failures, newBatchFailure(AgentSecurityTriage, *batchIndex, batch.Files, err))
			*batchIndex++
			escalate = true
			continue
		}
		result.SuccessfulBatches++
		*batchIndex++
		if response.Escalate {
			escalate = true
		}
	}
	return escalate
}

func (o *Orchestrator) runFindingAgent(ctx context.Context, changes change.ChangeSet, localFindings []findings.Finding, eligibleLines map[string]map[int]struct{}, agent ReviewAgent, relatedContext []ContextFile, result *ReviewResult, batchIndex int) ([]findings.Finding, int, error) {
	agentBuilder, err := o.builder.ForAgent(agent)
	if err != nil {
		return nil, batchIndex, fmt.Errorf("configure %s agent: %w", agent.ID, err)
	}
	batches, err := agentBuilder.Build(ctx, changes, localFindings, relatedContext)
	if err != nil {
		return nil, batchIndex, fmt.Errorf("prepare %s agent batches: %w", agent.ID, err)
	}
	result.BatchCount += len(batches)
	aiFindings := make([]findings.Finding, 0)
	for _, batch := range batches {
		if err := ctx.Err(); err != nil {
			return aiFindings, batchIndex, err
		}
		result.ReviewedFiles = appendUnique(result.ReviewedFiles, batch.Files...)
		response, err := o.provider.Analyze(ctx, batch.Request)
		if ctxErr := ctx.Err(); ctxErr != nil {
			return aiFindings, batchIndex, ctxErr
		}
		if err != nil {
			result.Failures = append(result.Failures, newBatchFailure(agent.ID, batchIndex, batch.Files, err))
			batchIndex++
			continue
		}
		validated, err := validateResponse(response, batch.Files, eligibleLines, agent)
		if err != nil {
			result.Failures = append(result.Failures, newBatchFailure(agent.ID, batchIndex, batch.Files, err))
			batchIndex++
			continue
		}
		aiFindings = append(aiFindings, validated...)
		result.SuccessfulBatches++
		batchIndex++
	}
	return aiFindings, batchIndex, nil
}

func contextPaths(changes change.ChangeSet) []string {
	paths := make([]string, 0, len(changes.Files))
	for _, file := range changes.Files {
		if file.Binary || file.Status == change.StatusDeleted {
			continue
		}
		if path := file.Path(); path != "" {
			paths = append(paths, path)
		}
	}
	return paths
}

func validateTriageResponse(response *TriageResponse) error {
	if response == nil {
		return fmt.Errorf("provider returned an empty triage response")
	}
	if response.Status != ResponseStatusComplete {
		return fmt.Errorf("unsupported triage response status %q", response.Status)
	}
	if len(response.Surfaces) > 20 {
		return fmt.Errorf("triage surfaces exceed 20-entry limit")
	}
	for i, surface := range response.Surfaces {
		if len(surface) > 500 || strings.IndexByte(surface, 0) >= 0 {
			return fmt.Errorf("triage surface %d is invalid", i+1)
		}
	}
	if len(response.Rationale) > 2000 || strings.IndexByte(response.Rationale, 0) >= 0 {
		return fmt.Errorf("triage rationale is invalid")
	}
	return nil
}

func appendUnique(values []string, candidates ...string) []string {
	existing := make(map[string]struct{}, len(values)+len(candidates))
	for _, value := range values {
		existing[value] = struct{}{}
	}
	for _, candidate := range candidates {
		if _, found := existing[candidate]; found {
			continue
		}
		existing[candidate] = struct{}{}
		values = append(values, candidate)
	}
	return values
}

func newBatchFailure(agentID AgentID, index int, files []string, err error) BatchFailure {
	return BatchFailure{
		AgentID: string(agentID),
		Batch:   index + 1,
		Files:   append([]string(nil), files...),
		Message: err.Error(),
	}
}
