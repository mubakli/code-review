package ai

import (
	"strings"

	"code-review/internal/change"
	"code-review/internal/findings"
)

type AgentID string

const (
	AgentCorrectness AgentID = "correctness"
	AgentSecurity    AgentID = "security"
)

type ReviewAgent struct {
	ID           AgentID
	Instructions string
	Categories   map[findings.Category]struct{}
}

const agentBaseInstructions = `Review only issues introduced by these staged changes. Report findings on changed lines with evidence from the supplied diff. Do not invent runtime behavior, missing context, or unrelated legacy issues. Return structured findings only.`

func DefaultAgents() []ReviewAgent {
	return []ReviewAgent{
		{
			ID: AgentCorrectness,
			Instructions: agentBaseInstructions + " Act as the correctness specialist. Focus only on edge cases, nil/null handling, error handling, resource cleanup, transactions, concurrency, and incorrect assumptions. " +
				"Use the correctness category for concrete defects and quality only when the issue is directly correctness-adjacent.",
			Categories: map[findings.Category]struct{}{
				findings.CategoryCorrectness: {},
				findings.CategoryQuality:     {},
			},
		},
		{
			ID: AgentSecurity,
			Instructions: agentBaseInstructions + " Act as the security specialist. Focus only on injection, authentication, authorization, unsafe input, credentials, path traversal, command execution, cryptography, and data exposure. " +
				"Return only security-category findings supported by changed-line evidence.",
			Categories: map[findings.Category]struct{}{
				findings.CategorySecurity: {},
			},
		},
	}
}

func RouteAgents(changes change.ChangeSet, agents []ReviewAgent) []ReviewAgent {
	routed := make([]ReviewAgent, 0, len(agents))
	for _, agent := range agents {
		if agent.ID == AgentCorrectness || (agent.ID == AgentSecurity && hasSecuritySignal(changes)) {
			routed = append(routed, agent)
		}
	}
	return routed
}

func hasSecuritySignal(changes change.ChangeSet) bool {
	for _, file := range changes.Files {
		if containsSecurityTerm(file.Path()) {
			return true
		}
		for _, hunk := range file.Hunks {
			for _, line := range hunk.Lines {
				if line.Kind == change.LineAdded && containsSecurityTerm(line.Content) {
					return true
				}
			}
		}
	}
	return false
}

func containsSecurityTerm(value string) bool {
	value = strings.ToLower(value)
	for _, term := range []string{
		"api_key", "apikey", "auth", "command", "cookie", "credential", "crypto", "decrypt", "encrypt",
		"exec", "header", "jwt", "oauth", "passwd", "password", "permission", "private_key",
		"request", "secret", "security", "session", "shell", "sql", "token", "upload",
	} {
		if strings.Contains(value, term) {
			return true
		}
	}
	return false
}
