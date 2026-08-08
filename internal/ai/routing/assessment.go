package routing

import (
	"fmt"
	"sort"
	"strings"
)

// SecurityAssessment is the tiny structured output of the triage router. It
// is routing context for the deep security agent, never a diagnosis: surfaces
// name what the deep agent must examine first, reasons say why the review was
// escalated. Kept intentionally small (a few surfaces, a few short reasons)
// because it feeds directly into provider prompts.
type SecurityAssessment struct {
	Escalate   bool              `json:"escalate"`
	Confidence Confidence        `json:"confidence"`
	Surfaces   []SecuritySurface `json:"surfaces"`
	Reasons    []string          `json:"reasons"`
}

const (
	maxAssessmentSurfaces = 20
	maxAssessmentReasons  = 5
	maxSurfaceLength      = 500
	maxReasonLength       = 300
)

// Validate enforces the tight bounds that keep a triage assessment cheap to
// carry through the pipeline. It returns nil for an assessment is well-formed
// within those bounds.
func (a *SecurityAssessment) Validate() error {
	if a == nil {
		return fmt.Errorf("security assessment is nil")
	}
	if confidenceRank(a.Confidence) == 0 {
		return fmt.Errorf("invalid triage confidence %q", a.Confidence)
	}
	if len(a.Surfaces) > maxAssessmentSurfaces {
		return fmt.Errorf("%d triage surfaces exceed the limit of %d", len(a.Surfaces), maxAssessmentSurfaces)
	}
	for _, surface := range a.Surfaces {
		if strings.ContainsRune(string(surface), 0) {
			return fmt.Errorf("triage surface contains NUL byte")
		}
		if len(surface) > maxSurfaceLength {
			return fmt.Errorf("triage surface exceeds %d bytes", maxSurfaceLength)
		}
	}
	if len(a.Reasons) > maxAssessmentReasons {
		return fmt.Errorf("%d triage reasons exceed the limit of %d", len(a.Reasons), maxAssessmentReasons)
	}
	for _, reason := range a.Reasons {
		if strings.ContainsRune(reason, 0) {
			return fmt.Errorf("triage reason contains NUL byte")
		}
		if len(reason) > maxReasonLength {
			return fmt.Errorf("triage reason exceeds %d bytes", maxReasonLength)
		}
	}
	return nil
}

// MergeAssessments unions two escalated assessments into one compact
// assessment. Used when several staged batches escalate: surfaces and reasons
// are merged, deduplicated, and hard-capped so the deep agent still receives
// one tiny routing context.
func MergeAssessments(left, right *SecurityAssessment) *SecurityAssessment {
	if left == nil || !left.Escalate {
		return right
	}
	if right == nil || !right.Escalate {
		return left
	}
	merged := &SecurityAssessment{
		Escalate:   true,
		Confidence: left.Confidence,
	}
	if confidenceRank(right.Confidence) > confidenceRank(left.Confidence) {
		merged.Confidence = right.Confidence
	}
	seenSurfaces := make(map[SecuritySurface]struct{}, len(left.Surfaces)+len(right.Surfaces))
	for _, surface := range left.Surfaces {
		seenSurfaces[surface] = struct{}{}
	}
	for _, surface := range right.Surfaces {
		seenSurfaces[surface] = struct{}{}
	}
	surfaces := make([]SecuritySurface, 0, len(seenSurfaces))
	for surface := range seenSurfaces {
		surfaces = append(surfaces, surface)
	}
	sort.Slice(surfaces, func(i, j int) bool { return surfaces[i] < surfaces[j] })
	merged.Surfaces = surfaces
	seenReasons := make(map[string]struct{}, 0)
	for _, reason := range left.Reasons {
		if _, exists := seenReasons[reason]; exists {
			continue
		}
		seenReasons[reason] = struct{}{}
		merged.Reasons = append(merged.Reasons, reason)
	}
	for _, reason := range right.Reasons {
		if _, exists := seenReasons[reason]; exists {
			continue
		}
		seenReasons[reason] = struct{}{}
		merged.Reasons = append(merged.Reasons, reason)
		if len(merged.Reasons) >= maxAssessmentReasons {
			break
		}
	}
	return merged
}
