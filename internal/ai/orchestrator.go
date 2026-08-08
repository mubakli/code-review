package ai

import (
	stdcontext "context"
	"fmt"
	"strings"

	"code-review/internal/ai/context"
	"code-review/internal/ai/provider"
	"code-review/internal/ai/request"
	"code-review/internal/change"
	"code-review/internal/findings"
)

// Orchestrator wires the preparation/execution layers with the declarative
// agent model: policies route, the context layer prepares, the request layer
// builds, providers execute, and the orchestrator validates and merges.
type Orchestrator struct {
	preparer context.Preparer
	builder  request.RequestBuilder
	provider provider.Provider
	agents   []AgentSpec
	resolver context.ContextResolver
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

func NewOrchestrator(preparer context.Preparer, builder request.RequestBuilder, provider provider.Provider) (*Orchestrator, error) {
	return NewOrchestratorWithAgents(preparer, builder, provider, DefaultAgents())
}

func NewOrchestratorWithAgents(preparer context.Preparer, builder request.RequestBuilder, provider provider.Provider, agents []AgentSpec) (*Orchestrator, error) {
	return NewOrchestratorWithAgentsAndResolver(preparer, builder, provider, agents, nil)
}

func NewOrchestratorWithAgentsAndResolver(preparer context.Preparer, builder request.RequestBuilder, provider provider.Provider, agents []AgentSpec, resolver context.ContextResolver) (*Orchestrator, error) {
	if provider == nil {
		return nil, fmt.Errorf("AI provider is required")
	}
	if len(agents) == 0 {
		return nil, fmt.Errorf("at least one AI review agent is required")
	}
	for _, agent := range agents {
		if agent.Policy == nil {
			return nil, fmt.Errorf("agent %q is missing a routing policy", agent.ID)
		}
		if agent.Role != RoleAnalyzer {
			return nil, fmt.Errorf("agent %q has role %q; only analyzer agents can be selected for review", agent.ID, agent.Role)
		}
	}
	return &Orchestrator{preparer: preparer, builder: builder, provider: provider, agents: append([]AgentSpec(nil), agents...), resolver: resolver}, nil
}

// Review runs each selected agent only when its routing policy decides to, and
// executes the agent according to its role. Routing is declarative: agents do
// not switch on sibling identities, and routers are consulted only by policies.
func (o *Orchestrator) Review(ctx stdcontext.Context, changes change.ChangeSet, localFindings []findings.Finding) (ReviewResult, error) {
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
		decision, err := agent.Policy.ShouldRun(ctx, changes, localFindings, &result, &batchIndex, o)
		if err != nil {
			return result, err
		}
		if !decision.Run {
			continue
		}
		v, idx, err := o.runAnalyzer(ctx, changes, localFindings, eligibleLines, agent, decision.Context, &result, batchIndex)
		if err != nil {
			return result, err
		}
		aiFindings = append(aiFindings, v...)
		batchIndex = idx
	}

	result.Findings = findings.Merge(localFindings, aiFindings)
	return result, nil
}

// RunRouter executes a router agent and records its activity. Router errors
// escalate (fail-closed): policies may treat the return value as their gate.
func (o *Orchestrator) RunRouter(ctx stdcontext.Context, spec AgentSpec, changes change.ChangeSet, staticFindings []findings.Finding, result *ReviewResult, batchIndex *int) (bool, error) {
	if spec.Role != RoleRouter {
		return false, fmt.Errorf("agent %q has role %q and cannot route", spec.ID, spec.Role)
	}
	prepared, err := o.preparer.Prepare(ctx, changes, staticFindings, nil, spec.Prompt)
	if err != nil {
		result.Failures = append(result.Failures, newBatchFailure(spec.ID, *batchIndex, nil, err))
		return true, nil // fail closed
	}
	if len(prepared.Batches) == 0 {
		return false, nil
	}
	result.Agents = append(result.Agents, string(spec.ID))
	result.BatchCount += len(prepared.Batches)
	escalate := false
	for _, batch := range prepared.Batches {
		if err := ctx.Err(); err != nil {
			return escalate, nil
		}
		result.ReviewedFiles = appendUnique(result.ReviewedFiles, batch.Files...)
		analysisRequest, err := o.builder.Build(batch, spec.Prompt)
		if err != nil {
			result.Failures = append(result.Failures, newBatchFailure(spec.ID, *batchIndex, batch.Files, err))
			*batchIndex++
			escalate = true
			continue
		}
		response, err := o.provider.Triage(ctx, analysisRequest)
		if err != nil {
			result.Failures = append(result.Failures, newBatchFailure(spec.ID, *batchIndex, batch.Files, err))
			*batchIndex++
			escalate = true
			continue
		}
		if err := validateTriageResponse(response); err != nil {
			result.Failures = append(result.Failures, newBatchFailure(spec.ID, *batchIndex, batch.Files, err))
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
	return escalate, nil
}

// ResolveStagedContext supplies related staged file content for deep
// specialist review. It is advisory: failures yield no context, never errors.
func (o *Orchestrator) ResolveStagedContext(ctx stdcontext.Context, changes change.ChangeSet) ([]context.ContextFile, error) {
	if o.resolver == nil {
		return nil, nil
	}
	return o.resolver.ResolveStagedContext(ctx, contextPaths(changes))
}

func (o *Orchestrator) runAnalyzer(ctx stdcontext.Context, changes change.ChangeSet, localFindings []findings.Finding, eligibleLines map[string]map[int]struct{}, agent AgentSpec, relatedContext []context.ContextFile, result *ReviewResult, batchIndex int) ([]findings.Finding, int, error) {
	prepared, err := o.preparer.Prepare(ctx, changes, localFindings, relatedContext, agent.Prompt)
	if err != nil {
		return nil, batchIndex, fmt.Errorf("prepare %s agent context: %w", agent.ID, err)
	}
	result.BatchCount += len(prepared.Batches)
	aiFindings := make([]findings.Finding, 0)
	for _, batch := range prepared.Batches {
		if err := ctx.Err(); err != nil {
			return aiFindings, batchIndex, err
		}
		result.ReviewedFiles = appendUnique(result.ReviewedFiles, batch.Files...)
		analysisRequest, err := o.builder.Build(batch, agent.Prompt)
		if err != nil {
			return nil, batchIndex, fmt.Errorf("build %s agent request: %w", agent.ID, err)
		}
		response, err := o.provider.Analyze(ctx, analysisRequest)
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
	result.Agents = append(result.Agents, string(agent.ID))
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

func validateTriageResponse(response *provider.TriageResponse) error {
	if response == nil {
		return fmt.Errorf("provider returned an empty triage response")
	}
	if response.Status != provider.ResponseStatusComplete {
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

var _ PolicyScope = (*Orchestrator)(nil)
