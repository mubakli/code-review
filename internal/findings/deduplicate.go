package findings

import (
	"strings"
	"unicode"
)

// Merge combines primary and secondary findings, consolidating likely reports
// of the same issue. Primary findings keep their deterministic explanation and
// source, while severity and confidence may be raised by a duplicate.
func Merge(primary, secondary []Finding) []Finding {
	merged := append([]Finding(nil), primary...)
	for _, candidate := range secondary {
		duplicate := duplicateIndex(merged, candidate)
		if duplicate < 0 {
			merged = append(merged, candidate)
			continue
		}
		consolidate(&merged[duplicate], candidate)
	}
	Sort(merged)
	return merged
}

func duplicateIndex(values []Finding, candidate Finding) int {
	for index, existing := range values {
		if existing.File != candidate.File || existing.Category != candidate.Category {
			continue
		}
		if existing.StartLine > candidate.EndLine || candidate.StartLine > existing.EndLine {
			continue
		}
		existingTitle := normalizedText(existing.Title)
		candidateTitle := normalizedText(candidate.Title)
		if existingTitle != "" && existingTitle == candidateTitle {
			return index
		}
		existingConcept := issueConcept(existing.Title + " " + existing.Message)
		candidateConcept := issueConcept(candidate.Title + " " + candidate.Message)
		if existingConcept != "" && existingConcept == candidateConcept {
			return index
		}
		if tokenOverlap(existing.Title, candidate.Title) >= 0.67 {
			return index
		}
		if tokenOverlap(existing.Title+" "+existing.Message, candidate.Title+" "+candidate.Message) >= 0.67 {
			return index
		}
	}
	return -1
}

func issueConcept(value string) string {
	value = strings.ToLower(value)
	switch {
	case strings.Contains(value, "sql") && strings.Contains(value, "injection"):
		return "sql-injection"
	case strings.Contains(value, "command") && strings.Contains(value, "injection"):
		return "command-injection"
	case strings.Contains(value, "credential"), strings.Contains(value, "api key"), strings.Contains(value, "private key"), strings.Contains(value, "hardcoded secret"):
		return "hardcoded-secret"
	case strings.Contains(value, "cross-site scripting"), strings.Contains(value, "cross site scripting"), strings.Contains(value, "xss"):
		return "xss"
	case strings.Contains(value, "ssrf"), strings.Contains(value, "server-side request forgery"):
		return "ssrf"
	case strings.Contains(value, "path traversal"):
		return "path-traversal"
	case strings.Contains(value, "n+1"), strings.Contains(value, "n + 1"):
		return "n-plus-one"
	default:
		return ""
	}
}

func consolidate(target *Finding, duplicate Finding) {
	if severityRank(duplicate.Severity) < severityRank(target.Severity) {
		target.Severity = duplicate.Severity
	}
	if duplicate.Confidence > target.Confidence {
		target.Confidence = duplicate.Confidence
	}
	if target.Suggestion == "" && duplicate.Suggestion != "" {
		target.Suggestion = duplicate.Suggestion
	}
}

func normalizedText(value string) string {
	return strings.Join(tokens(value), " ")
}

func tokenOverlap(left, right string) float64 {
	leftTokens := tokenSet(left)
	rightTokens := tokenSet(right)
	if len(leftTokens) == 0 || len(rightTokens) == 0 {
		return 0
	}
	intersection := 0
	for token := range leftTokens {
		if _, exists := rightTokens[token]; exists {
			intersection++
		}
	}
	denominator := len(leftTokens)
	if len(rightTokens) < denominator {
		denominator = len(rightTokens)
	}
	return float64(intersection) / float64(denominator)
}

func tokenSet(value string) map[string]struct{} {
	result := make(map[string]struct{})
	for _, token := range tokens(value) {
		result[token] = struct{}{}
	}
	return result
}

func tokens(value string) []string {
	parts := strings.FieldsFunc(strings.ToLower(value), func(character rune) bool {
		return !unicode.IsLetter(character) && !unicode.IsDigit(character)
	})
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if len(part) < 2 || deduplicationStopWords[part] {
			continue
		}
		result = append(result, part)
	}
	return result
}

var deduplicationStopWords = map[string]bool{
	"an":         true,
	"and":        true,
	"appears":    true,
	"code":       true,
	"contains":   true,
	"could":      true,
	"data":       true,
	"in":         true,
	"input":      true,
	"introduced": true,
	"is":         true,
	"line":       true,
	"may":        true,
	"of":         true,
	"possible":   true,
	"potential":  true,
	"the":        true,
	"this":       true,
	"to":         true,
	"user":       true,
	"value":      true,
}
