// Package routing defines the deterministic signal contract used by routing
// policies. Detectors turn a change set into security signals; a policy like
// SecurityEscalationPolicy uses those signals to skip the triage router and
// escalate directly, or to let the router decide.
package routing

import (
	"sort"

	"code-review/internal/change"
)

// SignalKind names the detector that produced a signal.
type SignalKind string

const (
	SignalKeyword       SignalKind = "keyword"
	SignalPath          SignalKind = "path"
	SignalAuth          SignalKind = "auth"
	SignalNetwork       SignalKind = "network"
	SignalDatabase      SignalKind = "database"
	SignalFilesystem    SignalKind = "filesystem"
	SignalSerialization SignalKind = "serialization"
	SignalDependency    SignalKind = "dependency"
	SignalEndpoint      SignalKind = "endpoint"
)

// SecuritySurface is the security coverage class a signal points to. It
// mirrors the coverage classes the deep security agent must evaluate, so a
// detector signal names what the deep agent should examine.
type SecuritySurface string

const (
	SurfaceAuthentication   SecuritySurface = "authentication"
	SurfaceAuthorization    SecuritySurface = "authorization"
	SurfaceCredentials      SecuritySurface = "credentials"
	SurfaceInjection        SecuritySurface = "injection"
	SurfaceCommandExecution SecuritySurface = "command-execution"
	SurfaceNetwork          SecuritySurface = "network"
	SurfaceFilesystem       SecuritySurface = "filesystem"
	SurfaceSerialization    SecuritySurface = "serialization"
	SurfaceCryptography     SecuritySurface = "cryptography"
	SurfaceDataExposure     SecuritySurface = "data-exposure"
	SurfaceClientSide       SecuritySurface = "client-side"
	SurfaceAvailability     SecuritySurface = "availability"
	SurfaceSupplyChain      SecuritySurface = "supply-chain"
)

// Confidence expresses how strongly a signal points at its surface. Policies
// may prefer high-confidence signals over low-confidence noise.
type Confidence string

const (
	ConfidenceHigh   Confidence = "high"
	ConfidenceMedium Confidence = "medium"
	ConfidenceLow    Confidence = "low"
)

// Signal is one deterministic security observation about a change set. It is
// a routing input, never a finding: it describes what the deep security agent
// must examine, not a confirmed vulnerability.
type Signal struct {
	Kind       SignalKind
	Surface    SecuritySurface
	Confidence Confidence
	File       string
	Reason     string
}

// SignalDetector is the contract every deterministic security detector
// implements: a change set in, security signals out. Detectors are stateless
// and must not perform context, repository, or provider access.
type SignalDetector interface {
	Detect(changes change.ChangeSet) []Signal
}

// Aggregate runs several detectors over one change set and merges their
// signals. When detectors agree on the same file, kind, and surface, the
// highest-confidence signal wins.
type Aggregate struct {
	Detectors []SignalDetector
}

// Detect runs every configured detector and returns a deduplicated,
// deterministically ordered signal list.
func (a Aggregate) Detect(changes change.ChangeSet) []Signal {
	type key struct {
		kind    SignalKind
		surface SecuritySurface
		file    string
	}
	best := make(map[key]Signal)
	for _, detector := range a.Detectors {
		for _, signal := range detector.Detect(changes) {
			id := key{kind: signal.Kind, surface: signal.Surface, file: signal.File}
			current, exists := best[id]
			if !exists || confidenceRank(signal.Confidence) > confidenceRank(current.Confidence) {
				best[id] = signal
			}
		}
	}
	signals := make([]Signal, 0, len(best))
	for _, signal := range best {
		signals = append(signals, signal)
	}
	sort.Slice(signals, func(i, j int) bool {
		left, right := signals[i], signals[j]
		if left.Kind != right.Kind {
			return left.Kind < right.Kind
		}
		if left.File != right.File {
			return left.File < right.File
		}
		if left.Surface != right.Surface {
			return left.Surface < right.Surface
		}
		return confidenceRank(left.Confidence) > confidenceRank(right.Confidence)
	})
	return signals
}

func confidenceRank(value Confidence) int {
	switch value {
	case ConfidenceHigh:
		return 3
	case ConfidenceMedium:
		return 2
	case ConfidenceLow:
		return 1
	default:
		return 0
	}
}
