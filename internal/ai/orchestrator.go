package ai

import (
	"context"
	"fmt"

	"code-review/internal/change"
	"code-review/internal/findings"
)

type Orchestrator struct {
	builder  Builder
	provider Provider
	agents   []ReviewAgent
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
	if provider == nil {
		return nil, fmt.Errorf("AI provider is required")
	}
	return &Orchestrator{builder: builder, provider: provider, agents: DefaultAgents()}, nil
}

// Review executes provider requests sequentially and degrades to local
// findings when individual batches fail or return invalid data.
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

	for _, agent := range RouteAgents(changes, o.agents) {
		agentBuilder, err := o.builder.ForAgent(agent)
		if err != nil {
			return result, fmt.Errorf("configure %s agent: %w", agent.ID, err)
		}
		batches, err := agentBuilder.Build(ctx, changes, localFindings)
		if err != nil {
			return result, fmt.Errorf("prepare %s agent batches: %w", agent.ID, err)
		}
		result.Agents = append(result.Agents, string(agent.ID))
		result.BatchCount += len(batches)
		for _, batch := range batches {
			if err := ctx.Err(); err != nil {
				result.Findings = findings.Merge(localFindings, aiFindings)
				return result, err
			}
			result.ReviewedFiles = appendUnique(result.ReviewedFiles, batch.Files...)
			response, err := o.provider.Analyze(ctx, batch.Request)
			if ctxErr := ctx.Err(); ctxErr != nil {
				result.Findings = findings.Merge(localFindings, aiFindings)
				return result, ctxErr
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
	}

	result.Findings = findings.Merge(localFindings, aiFindings)
	return result, nil
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

func validateResponse(response *AnalysisResponse, batchFiles []string, eligibleLines map[string]map[int]struct{}, agent ReviewAgent) ([]findings.Finding, error) {
	if response == nil {
		return nil, fmt.Errorf("provider returned an empty response")
	}
	if response.Status != ResponseStatusComplete {
		return nil, fmt.Errorf("unsupported response status %q", response.Status)
	}

	files := make(map[string]struct{}, len(batchFiles))
	for _, file := range batchFiles {
		files[file] = struct{}{}
	}
	validated := make([]findings.Finding, 0, len(response.Findings))
	for index, candidate := range response.Findings {
		if _, allowed := agent.Categories[candidate.Category]; !allowed {
			continue
		}
		finding := findings.Finding{
			File:       candidate.File,
			StartLine:  candidate.StartLine,
			EndLine:    candidate.EndLine,
			Severity:   candidate.Severity,
			Category:   candidate.Category,
			Title:      candidate.Title,
			Message:    candidate.Message,
			Suggestion: candidate.Suggestion,
			Confidence: candidate.Confidence,
			Source:     findings.SourceAI,
			AgentID:    string(agent.ID),
		}
		if err := finding.Validate(); err != nil {
			return nil, fmt.Errorf("finding %d is invalid: %w", index+1, err)
		}
		if _, exists := files[finding.File]; !exists {
			return nil, fmt.Errorf("finding %d references file outside its batch", index+1)
		}
		if _, exists := eligibleLines[finding.File][finding.StartLine]; !exists {
			return nil, fmt.Errorf("finding %d does not start on an added line", index+1)
		}
		validated = append(validated, finding)
	}
	return validated, nil
}

func addedLines(changes change.ChangeSet) map[string]map[int]struct{} {
	result := make(map[string]map[int]struct{}, len(changes.Files))
	for _, file := range changes.Files {
		path := file.Path()
		if path == "" || file.Binary {
			continue
		}
		lines := make(map[int]struct{})
		for _, hunk := range file.Hunks {
			for _, line := range hunk.Lines {
				if line.Kind == change.LineAdded && line.NewLine > 0 {
					lines[line.NewLine] = struct{}{}
				}
			}
		}
		result[path] = lines
	}
	return result
}

func newBatchFailure(agentID AgentID, index int, files []string, err error) BatchFailure {
	return BatchFailure{
		AgentID: string(agentID),
		Batch:   index + 1,
		Files:   append([]string(nil), files...),
		Message: err.Error(),
	}
}
