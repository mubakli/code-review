package detectors

import (
	"fmt"

	"code-review/internal/ai/routing"
	"code-review/internal/change"
)

// DatabaseDetector signals injection surfaces and dynamic construction:
// queries, ORMs, templates, and format-based assembly that can carry
// SQL/NoSQL, LDAP, XPath, or template injection.
type DatabaseDetector struct{}

var databaseTerms = []term{
	{word: "sql", surface: routing.SurfaceInjection, confidence: routing.ConfidenceHigh},
	{word: "query", surface: routing.SurfaceInjection, confidence: routing.ConfidenceHigh},
	{word: "orm", surface: routing.SurfaceInjection, confidence: routing.ConfidenceMedium},
	{word: "raw", surface: routing.SurfaceInjection, confidence: routing.ConfidenceMedium},
	{word: "ldap", surface: routing.SurfaceInjection, confidence: routing.ConfidenceHigh},
	{word: "xpath", surface: routing.SurfaceInjection, confidence: routing.ConfidenceHigh},
	{word: "template", surface: routing.SurfaceInjection, confidence: routing.ConfidenceMedium},
	{word: "sprintf", surface: routing.SurfaceInjection, confidence: routing.ConfidenceHigh},
	{word: "fprintf", surface: routing.SurfaceInjection, confidence: routing.ConfidenceHigh},
	{word: "reflect", surface: routing.SurfaceInjection, confidence: routing.ConfidenceMedium},
	{word: "preparedstatement", surface: routing.SurfaceInjection, confidence: routing.ConfidenceMedium},
	{word: "executesql", surface: routing.SurfaceInjection, confidence: routing.ConfidenceHigh},
	{word: "cursor.execute", surface: routing.SurfaceInjection, confidence: routing.ConfidenceHigh},
	{word: "db.", surface: routing.SurfaceInjection, confidence: routing.ConfidenceMedium},
	{word: "gorm", surface: routing.SurfaceInjection, confidence: routing.ConfidenceMedium},
	{word: "sqlite", surface: routing.SurfaceInjection, confidence: routing.ConfidenceMedium},
	{word: "mysql", surface: routing.SurfaceInjection, confidence: routing.ConfidenceMedium},
	{word: "postgres", surface: routing.SurfaceInjection, confidence: routing.ConfidenceMedium},
	{word: "nosql", surface: routing.SurfaceInjection, confidence: routing.ConfidenceMedium},
	{word: "mongodb", surface: routing.SurfaceInjection, confidence: routing.ConfidenceMedium},
	{word: "sqlalchemy", surface: routing.SurfaceInjection, confidence: routing.ConfidenceMedium},
	{word: "entitymanager", surface: routing.SurfaceInjection, confidence: routing.ConfidenceMedium},
	{word: "insert into", surface: routing.SurfaceInjection, confidence: routing.ConfidenceHigh},
	{word: "delete from", surface: routing.SurfaceInjection, confidence: routing.ConfidenceMedium},
	{word: "select ", surface: routing.SurfaceInjection, confidence: routing.ConfidenceLow},
}

func (DatabaseDetector) Detect(changes change.ChangeSet) []routing.Signal {
	var signals []routing.Signal
	forEachAddedLine(changes, func(path, content string) {
		for _, match := range matchingTerms(content, databaseTerms) {
			signals = append(signals, signal(routing.SignalDatabase, match.surface, match.confidence, path,
				fmt.Sprintf("added line contains database keyword %q (injection surface)", match.word)))
		}
	})
	return signals
}
