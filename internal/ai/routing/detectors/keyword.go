package detectors

import (
	"fmt"
	"strings"

	"code-review/internal/ai/routing"
	"code-review/internal/change"
)

// KeywordDetector emits the deterministic security vocabulary of the original
// single-list matcher: credentials, secrets, cryptography, data exposure, and
// command-execution terms, each tagged with the surface it points at.
type KeywordDetector struct{}

var keywordTerms = []term{
	// Credentials and secret handling.
	{word: "api_key", surface: routing.SurfaceCredentials, confidence: routing.ConfidenceHigh},
	{word: "apikey", surface: routing.SurfaceCredentials, confidence: routing.ConfidenceHigh},
	{word: "credential", surface: routing.SurfaceCredentials, confidence: routing.ConfidenceMedium},
	{word: "keyring", surface: routing.SurfaceCredentials, confidence: routing.ConfidenceMedium},
	{word: "passwd", surface: routing.SurfaceCredentials, confidence: routing.ConfidenceMedium},
	{word: "password", surface: routing.SurfaceCredentials, confidence: routing.ConfidenceHigh},
	{word: "private_key", surface: routing.SurfaceCredentials, confidence: routing.ConfidenceHigh},
	{word: "secret", surface: routing.SurfaceCredentials, confidence: routing.ConfidenceHigh},
	{word: "token", surface: routing.SurfaceCredentials, confidence: routing.ConfidenceMedium},
	// Cryptography and randomness.
	{word: "crypto", surface: routing.SurfaceCryptography, confidence: routing.ConfidenceMedium},
	{word: "decrypt", surface: routing.SurfaceCryptography, confidence: routing.ConfidenceHigh},
	{word: "encrypt", surface: routing.SurfaceCryptography, confidence: routing.ConfidenceHigh},
	{word: "hash", surface: routing.SurfaceCryptography, confidence: routing.ConfidenceMedium},
	{word: "hmac", surface: routing.SurfaceCryptography, confidence: routing.ConfidenceHigh},
	{word: "md5", surface: routing.SurfaceCryptography, confidence: routing.ConfidenceHigh},
	{word: "sha1", surface: routing.SurfaceCryptography, confidence: routing.ConfidenceHigh},
	{word: "sha256", surface: routing.SurfaceCryptography, confidence: routing.ConfidenceMedium},
	{word: "aes", surface: routing.SurfaceCryptography, confidence: routing.ConfidenceMedium},
	{word: "rsa", surface: routing.SurfaceCryptography, confidence: routing.ConfidenceMedium},
	{word: "cipher", surface: routing.SurfaceCryptography, confidence: routing.ConfidenceHigh},
	{word: "nonce", surface: routing.SurfaceCryptography, confidence: routing.ConfidenceHigh},
	{word: "random", surface: routing.SurfaceCryptography, confidence: routing.ConfidenceMedium},
	{word: "rand.", surface: routing.SurfaceCryptography, confidence: routing.ConfidenceMedium},
	{word: "entropy", surface: routing.SurfaceCryptography, confidence: routing.ConfidenceMedium},
	{word: "pem", surface: routing.SurfaceCryptography, confidence: routing.ConfidenceMedium},
	{word: "pgp", surface: routing.SurfaceCryptography, confidence: routing.ConfidenceHigh},
	// Command and process execution.
	{word: "command", surface: routing.SurfaceCommandExecution, confidence: routing.ConfidenceMedium},
	{word: "exec.", surface: routing.SurfaceCommandExecution, confidence: routing.ConfidenceHigh},
	{word: "exec(", surface: routing.SurfaceCommandExecution, confidence: routing.ConfidenceHigh},
	{word: "exec.command", surface: routing.SurfaceCommandExecution, confidence: routing.ConfidenceHigh},
	{word: "os/exec", surface: routing.SurfaceCommandExecution, confidence: routing.ConfidenceHigh},
	{word: "system(", surface: routing.SurfaceCommandExecution, confidence: routing.ConfidenceHigh},
	{word: "eval(", surface: routing.SurfaceCommandExecution, confidence: routing.ConfidenceHigh},
	{word: "subprocess", surface: routing.SurfaceCommandExecution, confidence: routing.ConfidenceHigh},
	{word: "shell", surface: routing.SurfaceCommandExecution, confidence: routing.ConfidenceMedium},
	{word: "sudo", surface: routing.SurfaceCommandExecution, confidence: routing.ConfidenceMedium},
	// Sensitive data and logging.
	{word: "anonym", surface: routing.SurfaceDataExposure, confidence: routing.ConfidenceMedium},
	{word: "pii", surface: routing.SurfaceDataExposure, confidence: routing.ConfidenceHigh},
	{word: "privacy", surface: routing.SurfaceDataExposure, confidence: routing.ConfidenceMedium},
	{word: "redact", surface: routing.SurfaceDataExposure, confidence: routing.ConfidenceMedium},
	{word: "logger", surface: routing.SurfaceDataExposure, confidence: routing.ConfidenceLow},
	{word: "logging", surface: routing.SurfaceDataExposure, confidence: routing.ConfidenceLow},
	// Environment and process boundaries.
	{word: "getenv", surface: routing.SurfaceCredentials, confidence: routing.ConfidenceMedium},
	{word: "environ", surface: routing.SurfaceDataExposure, confidence: routing.ConfidenceMedium},
	{word: "/proc", surface: routing.SurfaceDataExposure, confidence: routing.ConfidenceMedium},
	{word: "proc.", surface: routing.SurfaceDataExposure, confidence: routing.ConfidenceMedium},
	{word: "security", surface: routing.SurfaceAuthorization, confidence: routing.ConfidenceLow},
}

// Detect scans the changed path and every added line for the security
// vocabulary. The matcher is intentionally conservative: over-triggering only
// costs router tokens, while under-triggering silently loses coverage.
func (KeywordDetector) Detect(changes change.ChangeSet) []routing.Signal {
	var signals []routing.Signal
	for _, file := range changes.Files {
		path := file.Path()
		if match := firstKeyword(path); match != nil {
			signals = append(signals, signal(routing.SignalKeyword, match.surface, match.confidence, path,
				fmt.Sprintf("path contains security keyword %q (%s surface)", match.word, match.surface)))
		}
	}
	forEachAddedLine(changes, func(path, content string) {
		for _, match := range matchingTerms(content, keywordTerms) {
			signals = append(signals, signal(routing.SignalKeyword, match.surface, match.confidence, path,
				fmt.Sprintf("added line contains security keyword %q (%s surface)", match.word, match.surface)))
		}
	})
	return signals
}

func firstKeyword(value string) *term {
	lower := strings.ToLower(value)
	for index := range keywordTerms {
		if strings.Contains(lower, keywordTerms[index].word) {
			return &keywordTerms[index]
		}
	}
	return nil
}
