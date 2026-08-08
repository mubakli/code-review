package routing

import (
	"strings"
	"testing"
)

func TestSecurityAssessmentValidate(t *testing.T) {
	t.Parallel()

	valid := &SecurityAssessment{
		Escalate:   true,
		Confidence: ConfidenceHigh,
		Surfaces:   []SecuritySurface{SurfaceAuthorization},
		Reasons:    []string{"the surface awaits confirmation"},
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}

	tests := []struct {
		name   string
		value  *SecurityAssessment
		reason string
	}{
		{name: "nil", value: nil, reason: "nil"},
		{name: "unknown confidence", value: &SecurityAssessment{Escalate: true, Confidence: "certain"}, reason: "confidence"},
		{name: "too many surfaces", value: &SecurityAssessment{Escalate: true, Confidence: ConfidenceHigh, Surfaces: make([]SecuritySurface, 21)}, reason: "surfaces"},
		{name: "NUL surface", value: &SecurityAssessment{Escalate: true, Confidence: ConfidenceHigh, Surfaces: []SecuritySurface{"authorization\x00"}}, reason: "NUL"},
		{name: "long surface", value: &SecurityAssessment{Escalate: true, Confidence: ConfidenceHigh, Surfaces: []SecuritySurface{SecuritySurface(strings.Repeat("x", 501))}}, reason: "surface"},
		{name: "too many reasons", value: &SecurityAssessment{Escalate: true, Confidence: ConfidenceHigh, Reasons: []string{"a", "b", "c", "d", "e", "f"}}, reason: "reasons"},
		{name: "NUL reason", value: &SecurityAssessment{Escalate: true, Confidence: ConfidenceHigh, Reasons: []string{"bad\x00"}}, reason: "NUL"},
		{name: "long reason", value: &SecurityAssessment{Escalate: true, Confidence: ConfidenceHigh, Reasons: []string{strings.Repeat("y", 301)}}, reason: "reason"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if err := test.value.Validate(); err == nil {
				t.Fatalf("Validate() error = nil, want rejection for %s", test.reason)
			}
		})
	}
}

func TestMergeAssessments(t *testing.T) {
	t.Parallel()

	left := &SecurityAssessment{
		Escalate:   true,
		Confidence: ConfidenceMedium,
		Surfaces:   []SecuritySurface{SurfaceAuthorization, SurfaceInjection},
		Reasons:    []string{"reason one"},
	}
	right := &SecurityAssessment{
		Escalate:   true,
		Confidence: ConfidenceHigh,
		Surfaces:   []SecuritySurface{SurfaceInjection, SurfaceNetwork},
		Reasons:    []string{"reason one", "reason two"},
	}
	merged := MergeAssessments(left, right)
	if !merged.Escalate || merged.Confidence != ConfidenceHigh {
		t.Fatalf("merged = %#v", merged)
	}
	if len(merged.Surfaces) != 3 {
		t.Fatalf("merged surfaces = %#v, want 3 unique", merged.Surfaces)
	}
	if len(merged.Reasons) != 2 {
		t.Fatalf("merged reasons = %#v, want 2 unique", merged.Reasons)
	}

	if merged := MergeAssessments(nil, right); merged != right {
		t.Fatalf("MergeAssessments(nil, right) = %#v", merged)
	}
	if merged := MergeAssessments(left, nil); merged != left {
		t.Fatalf("MergeAssessments(left, nil) = %#v", merged)
	}
	if merged := MergeAssessments(nil, nil); merged != nil {
		t.Fatalf("MergeAssessments(nil, nil) = %#v", merged)
	}
}
