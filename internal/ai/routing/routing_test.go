package routing

import (
	"testing"

	"code-review/internal/change"
)

type fixedDetector struct {
	values []Signal
}

func (d fixedDetector) Detect(change.ChangeSet) []Signal {
	return d.values
}

func TestAggregateDeduplicatesByFileAndSurface(t *testing.T) {
	t.Parallel()

	aggregate := Aggregate{Detectors: []SignalDetector{
		fixedDetector{values: []Signal{
			{Kind: SignalKeyword, Surface: SurfaceCredentials, Confidence: ConfidenceLow, File: "a.go", Reason: "low"},
			{Kind: SignalKeyword, Surface: SurfaceCredentials, Confidence: ConfidenceHigh, File: "a.go", Reason: "high"},
		}},
		fixedDetector{values: []Signal{
			{Kind: SignalEndpoint, Surface: SurfaceAuthorization, Confidence: ConfidenceMedium, File: "b.go", Reason: "endpoint"},
			{Kind: SignalKeyword, Surface: SurfaceCredentials, Confidence: ConfidenceMedium, File: "a.go", Reason: "ignored"},
		}},
	}}

	signals := aggregate.Detect(change.ChangeSet{})
	if len(signals) != 2 {
		t.Fatalf("Detect() = %d signals, want 2: %#v", len(signals), signals)
	}
	found := make(map[SignalKind]Signal, len(signals))
	for _, signal := range signals {
		found[signal.Kind] = signal
	}
	keyword, exists := found[SignalKeyword]
	if !exists || keyword.Confidence != ConfidenceHigh || keyword.Reason != "high" {
		t.Fatalf("keyword signal = %#v, want kept highest-confidence variant", found[SignalKeyword])
	}
	if endpoint, exists := found[SignalEndpoint]; !exists || endpoint.File != "b.go" {
		t.Fatalf("endpoint signal = %#v", found[SignalEndpoint])
	}
}

func TestAggregateOrdersSignalsDeterministically(t *testing.T) {
	t.Parallel()

	aggregate := Aggregate{Detectors: []SignalDetector{
		fixedDetector{values: []Signal{
			{Kind: SignalNetwork, Surface: SurfaceNetwork, Confidence: ConfidenceHigh, File: "a.go"},
			{Kind: SignalKeyword, Surface: SurfaceCredentials, Confidence: ConfidenceHigh, File: "a.go"},
		}},
	}}
	signals := aggregate.Detect(change.ChangeSet{})
	if len(signals) != 2 || signals[0].Kind != SignalKeyword || signals[1].Kind != SignalNetwork {
		t.Fatalf("Detect() order = %#v, want keyword before network", signals)
	}
}

func TestAggregateWithNoDetectorsReturnsNothing(t *testing.T) {
	t.Parallel()

	if signals := (Aggregate{}).Detect(change.ChangeSet{}); len(signals) != 0 {
		t.Fatalf("Detect() = %#v, want no signals", signals)
	}
}
