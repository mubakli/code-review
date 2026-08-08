package detectors

import (
	"fmt"
	"strings"

	"code-review/internal/ai/routing"
	"code-review/internal/change"
)

// PathDetector flags credential, configuration, and dependency files whose
// addition is security-relevant even without a code keyword.
type PathDetector struct{}

var credentialPathSuffixes = []string{
	".env", ".npmrc", ".pypirc", ".netrc", ".aws", ".ssh", ".kubeconfig",
	".pem", ".key", ".p12", ".pfx", ".jks", "credentials", "credential",
}

var configPathSuffixes = []string{
	"config.yaml", "config.yml", "dockerfile", "containerfile",
}

var credentialPathMarkers = []string{
	"secret", "credential", "token", "password", "session", "id_rsa", "jwt",
}

// Detect reports one signal per sensitive path, tagged by what kind of
// material the path implies.
func (PathDetector) Detect(changes change.ChangeSet) []routing.Signal {
	var signals []routing.Signal
	for _, file := range changes.Files {
		lower := strings.ToLower(file.Path())
		switch {
		case strings.HasPrefix(lower, ".env") || strings.Contains(lower, "/.env"):
			signals = append(signals, signal(routing.SignalPath, routing.SurfaceCredentials, routing.ConfidenceHigh, file.Path(),
				fmt.Sprintf("sensitive environment file %q", file.Path())))
		case hasSuffix(lower, credentialPathSuffixes):
			signals = append(signals, signal(routing.SignalPath, routing.SurfaceCredentials, routing.ConfidenceHigh, file.Path(),
				fmt.Sprintf("credential file path %q", file.Path())))
		case hasSuffix(lower, configPathSuffixes):
			signals = append(signals, signal(routing.SignalPath, routing.SurfaceDataExposure, routing.ConfidenceMedium, file.Path(),
				fmt.Sprintf("security-relevant configuration path %q", file.Path())))
		case containsAny(lower, credentialPathMarkers):
			signals = append(signals, signal(routing.SignalPath, routing.SurfaceCredentials, routing.ConfidenceMedium, file.Path(),
				fmt.Sprintf("path %q contains a credential marker", file.Path())))
		}
	}
	return signals
}

func hasSuffix(value string, suffixes []string) bool {
	for _, suffix := range suffixes {
		if strings.HasSuffix(value, suffix) {
			return true
		}
	}
	return false
}

func containsAny(value string, markers []string) bool {
	for _, marker := range markers {
		if strings.Contains(value, marker) {
			return true
		}
	}
	return false
}
