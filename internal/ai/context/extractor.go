package context

import (
	stdcontext "context"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"code-review/internal/change"
)

const (
	partialHunkMarker = "# reviewer: partial hunk split to respect the token budget\n"
	truncatedMarker   = "\n[reviewer: long diff line truncated to respect the token budget]\n"
)

// fragment is one token-budgeted slice of a single file's redacted diff text.
type fragment struct {
	file           string
	text           string
	truncated      bool
	redactionCount int
}

// extractFragments turns one file's diff into redacted, token-budgeted
// fragments. Header and hunk boundaries are never split mid-line, and a long
// line is truncated rather than overflowing its budget.
func extractFragments(ctx stdcontext.Context, file change.FileChange, tokenLimit int) ([]fragment, error) {
	header, headerRedactions := redactText(renderFileHeader(file))
	body, bodyRedactions := redactText(renderFileBody(file))
	full := header + body
	if EstimateTokens(full) <= tokenLimit {
		return []fragment{{file: file.Path(), text: full, redactionCount: headerRedactions + bodyRedactions}}, nil
	}
	if EstimateTokens(header+partialHunkMarker) >= tokenLimit {
		return nil, fmt.Errorf("file header leaves no room for diff content")
	}

	result := make([]fragment, 0)
	current := header
	lastHunkHeader := ""
	hasBody := false
	truncated := false
	for _, line := range strings.SplitAfter(body, "\n") {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if line == "" {
			continue
		}
		isHunkHeader := strings.HasPrefix(line, "@@ ")
		if isHunkHeader {
			lastHunkHeader = line
		}

		if EstimateTokens(current+line) > tokenLimit && hasBody {
			result = append(result, fragment{file: file.Path(), text: current, truncated: truncated, redactionCount: countPlaceholders(current)})
			current = header + partialHunkMarker
			if lastHunkHeader != "" && !isHunkHeader {
				current += lastHunkHeader
			}
			hasBody = false
			truncated = false
		}

		if EstimateTokens(current+line) > tokenLimit {
			available := tokenLimit - EstimateTokens(current)
			line = truncateToBudget(line, available)
			truncated = true
		}
		if EstimateTokens(current+line) > tokenLimit {
			return nil, fmt.Errorf("diff line cannot fit within token budget")
		}
		current += line
		if !isHunkHeader {
			hasBody = true
		}
	}
	if hasBody || len(result) == 0 {
		result = append(result, fragment{file: file.Path(), text: current, truncated: truncated, redactionCount: countPlaceholders(current)})
	}
	return result, nil
}

func renderFileHeader(file change.FileChange) string {
	oldPath := "/dev/null"
	if file.OldPath != "" {
		oldPath = formatDiffPath("a/", file.OldPath)
	}
	newPath := "/dev/null"
	if file.NewPath != "" {
		newPath = formatDiffPath("b/", file.NewPath)
	}
	return fmt.Sprintf(
		"diff --git %s %s\n# status: %s\n--- %s\n+++ %s\n",
		oldPath,
		newPath,
		file.Status,
		oldPath,
		newPath,
	)
}

func renderFileBody(file change.FileChange) string {
	var output strings.Builder
	for _, hunk := range file.Hunks {
		fmt.Fprintf(
			&output,
			"@@ -%d,%d +%d,%d @@",
			hunk.OldStart,
			hunk.OldLines,
			hunk.NewStart,
			hunk.NewLines,
		)
		if hunk.Section != "" {
			output.WriteString(" ")
			output.WriteString(hunk.Section)
		}
		output.WriteString("\n")
		for _, line := range hunk.Lines {
			switch line.Kind {
			case change.LineAdded:
				output.WriteByte('+')
			case change.LineDeleted:
				output.WriteByte('-')
			default:
				output.WriteByte(' ')
			}
			output.WriteString(line.Content)
			output.WriteByte('\n')
		}
	}
	return output.String()
}

func formatDiffPath(prefix, value string) string {
	value = prefix + value
	if strings.ContainsAny(value, " \t\r\n\\\"") {
		return strconv.Quote(value)
	}
	return value
}

func truncateToBudget(value string, tokenLimit int) string {
	if tokenLimit <= 0 {
		return ""
	}
	maxBytes := tokenLimit * estimatedBytesPerToken
	marker := truncatedMarker
	if len(marker) >= maxBytes {
		marker = "[truncated]\n"
	}
	if len(marker) >= maxBytes {
		return marker[:maxBytes]
	}
	cut := maxBytes - len(marker)
	if cut > len(value) {
		return value
	}
	for cut > 0 && !utf8.ValidString(value[:cut]) {
		cut--
	}
	return value[:cut] + marker
}

func truncateBytes(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	marker := contextTruncatedMarker
	if len(marker) >= limit {
		return marker[:limit]
	}
	cut := limit - len(marker)
	for cut > 0 && !utf8.ValidString(value[:cut]) {
		cut--
	}
	return value[:cut] + marker
}
