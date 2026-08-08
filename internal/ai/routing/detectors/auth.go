package detectors

import (
	"fmt"

	"code-review/internal/ai/routing"
	"code-review/internal/change"
)

// AuthDetector signals authentication and authorization surfaces: identity
// checks, sessions, access control, and privilege boundaries.
type AuthDetector struct{}

var authTerms = []term{
	// Authentication and session handling.
	{word: "auth", surface: routing.SurfaceAuthentication, confidence: routing.ConfidenceMedium},
	{word: "authenticate", surface: routing.SurfaceAuthentication, confidence: routing.ConfidenceHigh},
	{word: "login", surface: routing.SurfaceAuthentication, confidence: routing.ConfidenceHigh},
	{word: "logon", surface: routing.SurfaceAuthentication, confidence: routing.ConfidenceHigh},
	{word: "logout", surface: routing.SurfaceAuthentication, confidence: routing.ConfidenceHigh},
	{word: "signin", surface: routing.SurfaceAuthentication, confidence: routing.ConfidenceHigh},
	{word: "signup", surface: routing.SurfaceAuthentication, confidence: routing.ConfidenceHigh},
	{word: "session", surface: routing.SurfaceAuthentication, confidence: routing.ConfidenceMedium},
	{word: "cookie", surface: routing.SurfaceAuthentication, confidence: routing.ConfidenceMedium},
	{word: "oauth", surface: routing.SurfaceAuthentication, confidence: routing.ConfidenceHigh},
	{word: "jwt", surface: routing.SurfaceAuthentication, confidence: routing.ConfidenceHigh},
	// Authorization and access control.
	{word: "authoriz", surface: routing.SurfaceAuthorization, confidence: routing.ConfidenceHigh},
	{word: "role", surface: routing.SurfaceAuthorization, confidence: routing.ConfidenceMedium},
	{word: "acl", surface: routing.SurfaceAuthorization, confidence: routing.ConfidenceHigh},
	{word: "permission", surface: routing.SurfaceAuthorization, confidence: routing.ConfidenceHigh},
	{word: "privileges", surface: routing.SurfaceAuthorization, confidence: routing.ConfidenceHigh},
	{word: "admin", surface: routing.SurfaceAuthorization, confidence: routing.ConfidenceMedium},
	{word: "userid", surface: routing.SurfaceAuthorization, confidence: routing.ConfidenceMedium},
	{word: "username", surface: routing.SurfaceAuthorization, confidence: routing.ConfidenceMedium},
	{word: "csrf", surface: routing.SurfaceAuthorization, confidence: routing.ConfidenceHigh},
	{word: "cors", surface: routing.SurfaceAuthorization, confidence: routing.ConfidenceMedium},
	{word: "origin", surface: routing.SurfaceAuthorization, confidence: routing.ConfidenceMedium},
	// Availability and abuse controls.
	{word: "captcha", surface: routing.SurfaceAvailability, confidence: routing.ConfidenceMedium},
	{word: "lockout", surface: routing.SurfaceAvailability, confidence: routing.ConfidenceHigh},
	{word: "brute", surface: routing.SurfaceAvailability, confidence: routing.ConfidenceHigh},
	{word: "ratelimit", surface: routing.SurfaceAvailability, confidence: routing.ConfidenceHigh},
	{word: "throttle", surface: routing.SurfaceAvailability, confidence: routing.ConfidenceMedium},
	{word: "quota", surface: routing.SurfaceAvailability, confidence: routing.ConfidenceMedium},
	{word: "regex", surface: routing.SurfaceAvailability, confidence: routing.ConfidenceLow},
	// Guard patterns that appear in real frameworks.
	{word: "login_required", surface: routing.SurfaceAuthentication, confidence: routing.ConfidenceHigh},
	{word: "requirelogin", surface: routing.SurfaceAuthentication, confidence: routing.ConfidenceHigh},
	{word: "authenticated", surface: routing.SurfaceAuthentication, confidence: routing.ConfidenceHigh},
	{word: "isauthenticated", surface: routing.SurfaceAuthentication, confidence: routing.ConfidenceHigh},
	{word: "requireauth", surface: routing.SurfaceAuthorization, confidence: routing.ConfidenceHigh},
	{word: "isadmin", surface: routing.SurfaceAuthorization, confidence: routing.ConfidenceHigh},
	{word: "hasrole", surface: routing.SurfaceAuthorization, confidence: routing.ConfidenceHigh},
	{word: "haspermission", surface: routing.SurfaceAuthorization, confidence: routing.ConfidenceHigh},
	{word: "authorize(", surface: routing.SurfaceAuthorization, confidence: routing.ConfidenceHigh},
	{word: "permissioncheck", surface: routing.SurfaceAuthorization, confidence: routing.ConfidenceHigh},
	{word: "ownership", surface: routing.SurfaceAuthorization, confidence: routing.ConfidenceMedium},
}

func (AuthDetector) Detect(changes change.ChangeSet) []routing.Signal {
	var signals []routing.Signal
	forEachAddedLine(changes, func(path, content string) {
		for _, match := range matchingTerms(content, authTerms) {
			signals = append(signals, signal(routing.SignalAuth, match.surface, match.confidence, path,
				fmt.Sprintf("added line contains auth keyword %q (%s surface)", match.word, match.surface)))
		}
	})
	return signals
}
