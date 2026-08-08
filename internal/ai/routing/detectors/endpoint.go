package detectors

import (
	"fmt"

	"code-review/internal/ai/routing"
	"code-review/internal/change"
)

// EndpointDetector signals route and endpoint registrations: mutable endpoints
// need authentication, authorization, CSRF, and abuse controls.
type EndpointDetector struct{}

var endpointTerms = []term{
	{word: "handlefunc", surface: routing.SurfaceAuthorization, confidence: routing.ConfidenceMedium},
	{word: "handle(", surface: routing.SurfaceAuthorization, confidence: routing.ConfidenceMedium},
	{word: "app.get", surface: routing.SurfaceAuthorization, confidence: routing.ConfidenceHigh},
	{word: "app.post", surface: routing.SurfaceAuthorization, confidence: routing.ConfidenceHigh},
	{word: "app.put", surface: routing.SurfaceAuthorization, confidence: routing.ConfidenceHigh},
	{word: "app.delete", surface: routing.SurfaceAuthorization, confidence: routing.ConfidenceHigh},
	{word: "app.patch", surface: routing.SurfaceAuthorization, confidence: routing.ConfidenceHigh},
	{word: "app.route", surface: routing.SurfaceAuthorization, confidence: routing.ConfidenceHigh},
	{word: "@app.route", surface: routing.SurfaceAuthorization, confidence: routing.ConfidenceHigh},
	{word: "@app.get", surface: routing.SurfaceAuthorization, confidence: routing.ConfidenceHigh},
	{word: "@app.post", surface: routing.SurfaceAuthorization, confidence: routing.ConfidenceHigh},
	{word: "@app.put", surface: routing.SurfaceAuthorization, confidence: routing.ConfidenceHigh},
	{word: "@app.delete", surface: routing.SurfaceAuthorization, confidence: routing.ConfidenceHigh},
	{word: "router.", surface: routing.SurfaceAuthorization, confidence: routing.ConfidenceMedium},
	{word: "mux.", surface: routing.SurfaceAuthorization, confidence: routing.ConfidenceMedium},
	{word: "addroute", surface: routing.SurfaceAuthorization, confidence: routing.ConfidenceHigh},
	{word: "registerhandler", surface: routing.SurfaceAuthorization, confidence: routing.ConfidenceHigh},
	{word: "route(", surface: routing.SurfaceAuthorization, confidence: routing.ConfidenceMedium},
	{word: "endpoint(", surface: routing.SurfaceAuthorization, confidence: routing.ConfidenceMedium},
	{word: "gin.", surface: routing.SurfaceAuthorization, confidence: routing.ConfidenceMedium},
	{word: "echo.", surface: routing.SurfaceAuthorization, confidence: routing.ConfidenceMedium},
	{word: "fastapi", surface: routing.SurfaceAuthorization, confidence: routing.ConfidenceMedium},
	{word: "flask", surface: routing.SurfaceAuthorization, confidence: routing.ConfidenceMedium},
	{word: "express", surface: routing.SurfaceAuthorization, confidence: routing.ConfidenceMedium},
	{word: "spring", surface: routing.SurfaceAuthorization, confidence: routing.ConfidenceMedium},
	{word: "controller", surface: routing.SurfaceAuthorization, confidence: routing.ConfidenceLow},
	{word: "upload", surface: routing.SurfaceFilesystem, confidence: routing.ConfidenceHigh},
}

func (EndpointDetector) Detect(changes change.ChangeSet) []routing.Signal {
	var signals []routing.Signal
	forEachAddedLine(changes, func(path, content string) {
		for _, match := range matchingTerms(content, endpointTerms) {
			signals = append(signals, signal(routing.SignalEndpoint, match.surface, match.confidence, path,
				fmt.Sprintf("added line registers or serves an endpoint (keyword %q)", match.word)))
		}
	})
	return signals
}
