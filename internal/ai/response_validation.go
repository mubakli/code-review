package ai

import (
	"fmt"

	"code-review/internal/change"
	"code-review/internal/findings"
)

// validateResponse enforces the provider boundary contract: findings must be
// structurally valid, scoped to the batch's files, and anchored to lines the
// agent was allowed to change. Unsupported categories are dropped rather than
// trusted.
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
			RuleID:      "ai/" + string(agent.ID),
			File:        candidate.File,
			StartLine:   candidate.StartLine,
			EndLine:     candidate.EndLine,
			Severity:    candidate.Severity,
			Category:    candidate.Category,
			Title:       candidate.Title,
			Message:     candidate.Message,
			Suggestion:  candidate.Suggestion,
			ProposedFix: candidate.ProposedFix,
			Confidence:  candidate.Confidence,
			Source:      findings.SourceAI,
			AgentID:     string(agent.ID),
		}
		finding = finding.Clone()
		finding.FinalizeID()
		if err := finding.Validate(); err != nil {
			return nil, fmt.Errorf("finding %d is invalid: %w", index+1, err)
		}
		if _, exists := files[finding.File]; !exists {
			return nil, fmt.Errorf("finding %d references file outside its batch", index+1)
		}
		if !rangeIsAdded(eligibleLines[finding.File], finding.StartLine, finding.EndLine) {
			return nil, fmt.Errorf("finding %d range is not entirely on added lines", index+1)
		}
		if finding.ProposedFix != nil && !rangeIsAdded(eligibleLines[finding.File], finding.ProposedFix.StartLine, finding.ProposedFix.EndLine) {
			return nil, fmt.Errorf("finding %d proposed fix range is not entirely on added lines", index+1)
		}
		validated = append(validated, finding)
	}
	return validated, nil
}

func rangeIsAdded(lines map[int]struct{}, start, end int) bool {
	for line := start; ; line++ {
		if _, exists := lines[line]; !exists {
			return false
		}
		if line == end {
			return true
		}
	}
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
