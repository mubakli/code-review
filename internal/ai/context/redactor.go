package context

import (
	"strings"

	"code-review/internal/redact"
)

// redactText applies the project's secret redaction and returns the redacted
// text with the number of placeholders introduced.
func redactText(value string) (string, int) {
	result := redact.Secrets(value)
	return result.Text, result.Count
}

// countPlaceholders reports how many redaction markers a redacted text holds.
func countPlaceholders(value string) int {
	return strings.Count(value, redact.Placeholder)
}
