package detectors

import (
	"strings"

	"code-review/internal/ai/routing"
	"code-review/internal/change"
)

// term is one data-driven keyword entry: a substring, the surface it points
// at, and how strongly it points there.
type term struct {
	word       string
	surface    routing.SecuritySurface
	confidence routing.Confidence
}

// forEachAddedLine visits every added line of a change set in order. Deleted
// and context lines are intentionally ignored: routing escalates only for
// code this change introduces.
func forEachAddedLine(changes change.ChangeSet, visit func(path, content string)) {
	for _, file := range changes.Files {
		path := file.Path()
		for _, hunk := range file.Hunks {
			for _, line := range hunk.Lines {
				if line.Kind == change.LineAdded {
					visit(path, line.Content)
				}
			}
		}
	}
}

// signal is a tiny builder for the routing.Signal values detectors produce.
func signal(kind routing.SignalKind, surface routing.SecuritySurface, confidence routing.Confidence, path, reason string) routing.Signal {
	return routing.Signal{
		Kind:       kind,
		Surface:    surface,
		Confidence: confidence,
		File:       path,
		Reason:     reason,
	}
}

// matchingTerms returns every term whose word appears case-insensitively in
// the value. Detectors use it to emit one signal per observed marker.
func matchingTerms(value string, terms []term) []term {
	lower := strings.ToLower(value)
	var matches []term
	for _, candidate := range terms {
		if strings.Contains(lower, candidate.word) {
			matches = append(matches, candidate)
		}
	}
	return matches
}
