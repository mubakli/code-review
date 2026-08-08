package detectors

import (
	"fmt"

	"code-review/internal/ai/routing"
	"code-review/internal/change"
)

// NetworkDetector signals outbound network surfaces: HTTP calls, redirects,
// sockets, and client-side transport that can carry SSRF or injection.
type NetworkDetector struct{}

var networkTerms = []term{
	{word: "http.", surface: routing.SurfaceNetwork, confidence: routing.ConfidenceMedium},
	{word: "https.", surface: routing.SurfaceNetwork, confidence: routing.ConfidenceMedium},
	{word: "url.", surface: routing.SurfaceNetwork, confidence: routing.ConfidenceMedium},
	{word: "fetch", surface: routing.SurfaceNetwork, confidence: routing.ConfidenceHigh},
	{word: "curl", surface: routing.SurfaceNetwork, confidence: routing.ConfidenceHigh},
	{word: "net.", surface: routing.SurfaceNetwork, confidence: routing.ConfidenceMedium},
	{word: "dial", surface: routing.SurfaceNetwork, confidence: routing.ConfidenceHigh},
	{word: "listen", surface: routing.SurfaceNetwork, confidence: routing.ConfidenceMedium},
	{word: "smtp", surface: routing.SurfaceNetwork, confidence: routing.ConfidenceHigh},
	{word: "redirect", surface: routing.SurfaceNetwork, confidence: routing.ConfidenceHigh},
	{word: "request", surface: routing.SurfaceNetwork, confidence: routing.ConfidenceLow},
	{word: "header", surface: routing.SurfaceInjection, confidence: routing.ConfidenceLow},
	{word: "http.client", surface: routing.SurfaceNetwork, confidence: routing.ConfidenceHigh},
	{word: "xmlhttprequest", surface: routing.SurfaceNetwork, confidence: routing.ConfidenceHigh},
	{word: "webhook", surface: routing.SurfaceNetwork, confidence: routing.ConfidenceMedium},
	{word: "wss://", surface: routing.SurfaceNetwork, confidence: routing.ConfidenceHigh},
	{word: "ws://", surface: routing.SurfaceNetwork, confidence: routing.ConfidenceHigh},
	{word: "socket", surface: routing.SurfaceNetwork, confidence: routing.ConfidenceMedium},
	{word: "proxy", surface: routing.SurfaceNetwork, confidence: routing.ConfidenceMedium},
	{word: "urllib", surface: routing.SurfaceNetwork, confidence: routing.ConfidenceHigh},
	{word: "requests.", surface: routing.SurfaceNetwork, confidence: routing.ConfidenceHigh},
	{word: "axios", surface: routing.SurfaceNetwork, confidence: routing.ConfidenceMedium},
	{word: "dns", surface: routing.SurfaceNetwork, confidence: routing.ConfidenceLow},
	// Client-side rendering and transport that can carry XSS.
	{word: "iframe", surface: routing.SurfaceClientSide, confidence: routing.ConfidenceMedium},
	{word: "innerhtml", surface: routing.SurfaceClientSide, confidence: routing.ConfidenceHigh},
	{word: "document.", surface: routing.SurfaceClientSide, confidence: routing.ConfidenceMedium},
	{word: "querystring", surface: routing.SurfaceClientSide, confidence: routing.ConfidenceMedium},
}

func (NetworkDetector) Detect(changes change.ChangeSet) []routing.Signal {
	var signals []routing.Signal
	forEachAddedLine(changes, func(path, content string) {
		for _, match := range matchingTerms(content, networkTerms) {
			signals = append(signals, signal(routing.SignalNetwork, match.surface, match.confidence, path,
				fmt.Sprintf("added line contains network keyword %q (%s surface)", match.word, match.surface)))
		}
	})
	return signals
}
