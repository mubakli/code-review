package ai

import (
	stdcontext "context"
	"fmt"
	"sync"

	"code-review/internal/ai/context"
	"code-review/internal/ai/provider"
	"code-review/internal/ai/request"
	"code-review/internal/ai/routing"
	"code-review/internal/change"
	"code-review/internal/findings"
)

// defaultConcurrency bounds how many provider calls run simultaneously. Two
// parallel requests stay well within provider rate limits while roughly
// halving wall time when both agents run. Bounded batch parallelism keeps the
// limit stable even for diffs that split into many batches.
const defaultConcurrency = 2

// Orchestrator wires the preparation/execution layers with the declarative
// agent model: policies route, the context layer prepares, the request layer
// builds, providers execute, and the orchestrator validates and merges.
type Orchestrator struct {
	preparer    context.Preparer
	builder     request.RequestBuilder
	provider    provider.Provider
	agents      []AgentSpec
	resolver    context.ContextResolver
	concurrency int
	semaphore   chan struct{}
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
	return &Orchestrator{
		preparer:    preparer,
		builder:     builder,
		provider:    provider,
		agents:      append([]AgentSpec(nil), agents...),
		resolver:    resolver,
		concurrency: defaultConcurrency,
		semaphore:   make(chan struct{}, defaultConcurrency),
	}, nil
}

// WithConcurrency bounds how many provider calls run simultaneously across all
// agents and batches. The limit is clamped to [1, 8].
func (o *Orchestrator) WithConcurrency(limit int) *Orchestrator {
	if limit < 1 {
		limit = 1
	}
	if limit > 8 {
		limit = 8
	}
	o.concurrency = limit
	o.semaphore = make(chan struct{}, limit)
	return o
}

// acquire blocks until a provider slot is free or the context is canceled. It
// reports whether the slot was acquired; callers skip work when it returns
// false, and the caller's context error is propagated after the group joins.
func (o *Orchestrator) acquire(ctx stdcontext.Context) bool {
	select {
	case o.semaphore <- struct{}{}:
		return true
	case <-ctx.Done():
		return false
	}
}

func (o *Orchestrator) release() {
	<-o.semaphore
}

// Review runs each selected agent only when its routing policy decides to, and
// executes the agent according to its role. Routing is declarative: agents do
// not switch on sibling identities, and routers are consulted only by policies.
// Agents run concurrently (bounded by the concurrency limit) and their
// contributions are merged in agent order, so results stay deterministic.
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
	runs := make([]agentRun, len(o.agents))
	var wg sync.WaitGroup
	for index, agent := range o.agents {
		wg.Add(1)
		go func(index int, agent AgentSpec) {
			defer wg.Done()
			runs[index] = o.runAgent(ctx, changes, localFindings, eligibleLines, agent)
		}(index, agent)
	}
	wg.Wait()

	if ctxErr := ctx.Err(); ctxErr != nil {
		result.Findings = findings.Merge(localFindings, combinedFindings(runs))
		return result, ctxErr
	}
	var firstErr error
	for index := range runs {
		if runs[index].err != nil && firstErr == nil {
			firstErr = runs[index].err
			// Mirror the sequential behavior: a failing agent contributes no
			// findings, while every successfully completed agent keeps its own.
			runs[index].findings = nil
		}
	}
	mergeAgentRuns(&result, runs)
	result.Findings = findings.Merge(localFindings, combinedFindings(runs))
	if firstErr != nil {
		return result, firstErr
	}
	return result, nil
}

// agentRun is one agent's concurrent contribution. Its result is assembled in
// its own goroutine and merged into the outer result in agent order, keeping
// Agents, ReviewedFiles, and Failures deterministic.
type agentRun struct {
	result   ReviewResult
	findings []findings.Finding
	err      error
}

// runAgent executes one agent's routing policy and, when it decides to run,
// its analyzer batches. The shared context is checked throughout; hard errors
// (routing or preparation failures) abort the agent, while per-batch provider
// failures are recorded and do not lose earlier batches.
func (o *Orchestrator) runAgent(ctx stdcontext.Context, changes change.ChangeSet, localFindings []findings.Finding, eligibleLines map[string]map[int]struct{}, agent AgentSpec) agentRun {
	result := ReviewResult{
		Findings:      findings.Merge(localFindings, nil),
		ReviewedFiles: make([]string, 0),
		Agents:        make([]string, 0),
		Failures:      make([]BatchFailure, 0),
	}
	batchIndex := 0
	decision, err := agent.Policy.ShouldRun(ctx, changes, localFindings, &result, &batchIndex, o)
	if err != nil {
		return agentRun{result: result, err: err}
	}
	if !decision.Run {
		return agentRun{result: result}
	}
	findingsForAgent, _, err := o.runAnalyzer(ctx, changes, localFindings, eligibleLines, agent, decision.Context, decision.Assessment, &result, 0)
	if err != nil {
		return agentRun{result: result, findings: nil, err: err}
	}
	return agentRun{result: result, findings: findingsForAgent}
}

// mergeAgentRuns folds each agent's concurrent result into the outer result in
// agent order. Findings are merged separately so the failing agent's partials
// can be dropped before the final deterministic merge.
func mergeAgentRuns(result *ReviewResult, runs []agentRun) {
	for _, run := range runs {
		result.Agents = append(result.Agents, run.result.Agents...)
		result.ReviewedFiles = appendUnique(result.ReviewedFiles, run.result.ReviewedFiles...)
		result.Failures = append(result.Failures, run.result.Failures...)
		result.BatchCount += run.result.BatchCount
		result.SuccessfulBatches += run.result.SuccessfulBatches
	}
}

func combinedFindings(runs []agentRun) []findings.Finding {
	all := make([]findings.Finding, 0)
	for _, run := range runs {
		all = append(all, run.findings...)
	}
	return all
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
	base := *batchIndex
	outcomes := make([]triageOutcome, len(prepared.Batches))
	var wg sync.WaitGroup
	for index, batch := range prepared.Batches {
		wg.Add(1)
		go func(index int, batch context.PreparedBatch) {
			defer wg.Done()
			if !o.acquire(ctx) {
				outcomes[index].skipped = true
				return
			}
			defer o.release()
			analysisRequest, err := o.builder.Build(batch, spec.Prompt)
			if err != nil {
				outcomes[index].failure = newBatchFailurePtr(spec.ID, base+index, batch.Files, err)
				return
			}
			response, err := o.provider.Triage(ctx, analysisRequest)
			if err != nil {
				outcomes[index].failure = newBatchFailurePtr(spec.ID, base+index, batch.Files, err)
				return
			}
			if err := validateAssessment(response); err != nil {
				outcomes[index].failure = newBatchFailurePtr(spec.ID, base+index, batch.Files, err)
				return
			}
			outcomes[index].assessment = response
		}(index, batch)
	}
	wg.Wait()
	*batchIndex += len(prepared.Batches)

	var escalated *routing.SecurityAssessment
	for index, outcome := range outcomes {
		if outcome.skipped {
			continue
		}
		result.ReviewedFiles = appendUnique(result.ReviewedFiles, prepared.Batches[index].Files...)
		if outcome.failure != nil {
			result.Failures = append(result.Failures, *outcome.failure)
			// Router failures escalate (fail-closed): the change still gets a
			// deep security pass, so a triage outage never clears a review.
			escalated = &routing.SecurityAssessment{Escalate: true, Confidence: routing.ConfidenceLow}
			continue
		}
		result.SuccessfulBatches++
		if outcome.assessment.Escalate {
			escalated = routing.MergeAssessments(escalated, outcome.assessment)
		}
	}
	return escalated, nil
}

// triageOutcome is one concurrent router batch's contribution. Skipped batches
// are dropped when the context is canceled; the caller propagates the context
// error after the group joins.
type triageOutcome struct {
	assessment *routing.SecurityAssessment
	failure    *BatchFailure
	skipped    bool
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

// batchOutcome is one concurrent analyzer batch's contribution. Skipped batches
// are dropped when the context is canceled; the caller propagates the context
// error after the group joins.
type batchOutcome struct {
	findings []findings.Finding
	failure  *BatchFailure
	skipped  bool
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
	if len(prepared.Batches) == 0 {
		return aiFindings, batchIndex, nil
	}
	outcomes := make([]batchOutcome, len(prepared.Batches))
	var wg sync.WaitGroup
	for index, batch := range prepared.Batches {
		wg.Add(1)
		go func(index int, batch context.PreparedBatch) {
			defer wg.Done()
			if !o.acquire(ctx) {
				outcomes[index].skipped = true
				return
			}
			defer o.release()
			analysisRequest, err := o.builder.Build(batch, prompt)
			if err != nil {
				outcomes[index].failure = newBatchFailurePtr(agent.ID, batchIndex+index, batch.Files, err)
				return
			}
			response, err := o.provider.Analyze(ctx, analysisRequest)
			if err != nil {
				if ctxErr := ctx.Err(); ctxErr != nil {
					outcomes[index].skipped = true
					return
				}
				outcomes[index].failure = newBatchFailurePtr(agent.ID, batchIndex+index, batch.Files, err)
				return
			}
			validated, err := validateResponse(response, batch.Files, eligibleLines, agent)
			if err != nil {
				outcomes[index].failure = newBatchFailurePtr(agent.ID, batchIndex+index, batch.Files, err)
				return
			}
			outcomes[index].findings = validated
		}(index, batch)
	}
	wg.Wait()
	if ctxErr := ctx.Err(); ctxErr != nil {
		return aiFindings, batchIndex + len(prepared.Batches), ctxErr
	}
	for index, outcome := range outcomes {
		if outcome.skipped {
			continue
		}
		result.ReviewedFiles = appendUnique(result.ReviewedFiles, prepared.Batches[index].Files...)
		if outcome.failure != nil {
			result.Failures = append(result.Failures, *outcome.failure)
			continue
		}
		aiFindings = append(aiFindings, outcome.findings...)
		result.SuccessfulBatches++
	}
	result.Agents = append(result.Agents, string(agent.ID))
	return aiFindings, batchIndex + len(prepared.Batches), nil
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

func newBatchFailurePtr(agentID AgentID, index int, files []string, err error) *BatchFailure {
	failure := newBatchFailure(agentID, index, files, err)
	return &failure
}

var _ PolicyScope = (*Orchestrator)(nil)
