package detectors

import (
	"fmt"

	"code-review/internal/ai/routing"
	"code-review/internal/change"
)

// SerializationDetector signals deserialization of untrusted data and other
// serialization boundaries in changed code.
type SerializationDetector struct{}

var serializationTerms = []term{
	{word: "unmarshal", surface: routing.SurfaceSerialization, confidence: routing.ConfidenceMedium},
	{word: "marshal", surface: routing.SurfaceSerialization, confidence: routing.ConfidenceMedium},
	{word: "deserialize", surface: routing.SurfaceSerialization, confidence: routing.ConfidenceHigh},
	{word: "unserialize", surface: routing.SurfaceSerialization, confidence: routing.ConfidenceHigh},
	{word: "pickle", surface: routing.SurfaceSerialization, confidence: routing.ConfidenceHigh},
	{word: "yaml", surface: routing.SurfaceSerialization, confidence: routing.ConfidenceMedium},
	{word: "json.load", surface: routing.SurfaceSerialization, confidence: routing.ConfidenceHigh},
	{word: "json.loads", surface: routing.SurfaceSerialization, confidence: routing.ConfidenceHigh},
	{word: "objectinputstream", surface: routing.SurfaceSerialization, confidence: routing.ConfidenceHigh},
	{word: "readobject", surface: routing.SurfaceSerialization, confidence: routing.ConfidenceHigh},
	{word: "yaml.load", surface: routing.SurfaceSerialization, confidence: routing.ConfidenceHigh},
	{word: "xml.etree", surface: routing.SurfaceSerialization, confidence: routing.ConfidenceMedium},
	{word: "xml.sax", surface: routing.SurfaceSerialization, confidence: routing.ConfidenceMedium},
	{word: "xmlrpc", surface: routing.SurfaceSerialization, confidence: routing.ConfidenceHigh},
}

func (SerializationDetector) Detect(changes change.ChangeSet) []routing.Signal {
	var signals []routing.Signal
	forEachAddedLine(changes, func(path, content string) {
		for _, match := range matchingTerms(content, serializationTerms) {
			signals = append(signals, signal(routing.SignalSerialization, match.surface, match.confidence, path,
				fmt.Sprintf("added line contains serialization keyword %q (serialization surface)", match.word)))
		}
	})
	return signals
}
