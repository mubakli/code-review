package ai

import (
	stdcontext "context"
	"fmt"

	"code-review/internal/ai/context"
	"code-review/internal/ai/provider"
	"code-review/internal/ai/request"
	"code-review/internal/ai/routing"
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
		v, idx, err := o.runAnalyzer(ctx, changes, localFindings, eligibleLines, agent, decision.Context, decision.Assessment, &result, batchIndex)
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
// escalate (fail-closed): a failed router returns an escalated assessment so
// policies send the change to the deep security agent regardless.
func (o *Orchestrator) RunRouter(ctx stdcontext.Context, spec AgentSpec, changes change.ChangeSet, staticFindings []findings.Finding, result *ReviewResult, batchIndex *int) (*routing.SecurityAssessment, error) {
	if spec.Role != RoleRouter {
		return nil, fmt.Errorf("agent %q has role %q and cannot route", spec.ID, spec.Role)
	}
	prepared, err := o.preparer.Prepare(ctx, changes, staticFindings, nil, spec.Prompt)
	if err != nil {
		result.Failures = append(result.Failures, newBatchFailure(spec.ID, *batchIndex, nil, err))
		return &routing.SecurityAssessment{Escalate: true, Confidence: routing.ConfidenceLow}, nil // fail closed
	}
	if len(prepared.Batches) == 0 {
		return nil, nil
	}
	result.Agents = append(result.Agents, string(spec.ID))
	result.BatchCount += len(prepared.Batches)
	var escalated *routing.SecurityAssessment
	for _, batch := range prepared.Batches {
		if err := ctx.Err(); err != nil {
			return escalated, nil
		}
		result.ReviewedFiles = appendUnique(result.ReviewedFiles, batch.Files...)
		analysisRequest, err := o.builder.Build(batch, spec.Prompt)
		if err != nil {
			result.Failures = append(result.Failures, newBatchFailure(spec.ID, *batchIndex, batch.Files, err))
			*batchIndex++
			escalated = &routing.SecurityAssessment{Escalate: true, Confidence: routing.ConfidenceLow}
			continue
		}
		response, err := o.provider.Triage(ctx, analysisRequest)
		if err != nil {
			result.Failures = append(result.Failures, newBatchFailure(spec.ID, *batchIndex, batch.Files, err))
			*batchIndex++
			escalated = &routing.SecurityAssessment{Escalate: true, Confidence: routing.ConfidenceLow}
			continue
		}
		if err := validateAssessment(response); err != nil {
			result.Failures = append(result.Failures, newBatchFailure(spec.ID, *batchIndex, batch.Files, err))
			*batchIndex++
			escalated = &routing.SecurityAssessment{Escalate: true, Confidence: routing.ConfidenceLow}
			continue
		}
		result.SuccessfulBatches++
		*batchIndex++
		if response.Escalate {
			escalated = routing.MergeAssessments(escalated, response)
		}
	}
	return escalated, nil
}

// ResolveContext supplies related staged code for deep specialist review, on
// demand: the request names only the areas the escalated surfaces point at.
// It is advisory: failures yield no context, never errors.
func (o *Orchestrator) ResolveContext(ctx stdcontext.Context, changes change.ChangeSet, request context.ContextRequest) (context.RepositoryContext, error) {
	if o.resolver == nil {
		return context.RepositoryContext{}, nil
	}
	return o.resolver.Resolve(ctx, changes, request)
}

func (o *Orchestrator) runAnalyzer(ctx stdcontext.Context, changes change.ChangeSet, localFindings []findings.Finding, eligibleLines map[string]map[int]struct{}, agent AgentSpec, relatedContext []context.ContextFile, assessment *routing.SecurityAssessment, result *ReviewResult, batchIndex int) ([]findings.Finding, int, error) {
	// The deep security agent consumes the triage routing context as input:
	// which surfaces to examine first and why the review was escalated.
	prompt := agent.Prompt
	if escalationContext := SecurityEscalationContext(assessment); escalationContext != "" {
		prompt += escalationContext
	}
	prepared, err := o.preparer.Prepare(ctx, changes, localFindings, relatedContext, prompt)
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
		analysisRequest, err := o.builder.Build(batch, prompt)
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

func validateAssessment(assessment *routing.SecurityAssessment) error {
	if err := assessment.Validate(); err != nil {
		return err
	}
	if assessment.Escalate {
		return nil
	}
	if len(assessment.Surfaces) > 0 || len(assessment.Reasons) > 0 {
		return fmt.Errorf("triage assessment must not carry surfaces or reasons when escalate is false")
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
