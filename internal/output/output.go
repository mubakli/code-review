package output

import (
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode"

	"code-review/internal/findings"
	"code-review/internal/review"
)

func WriteJSON(writer io.Writer, result review.Result) error {
	encoder := json.NewEncoder(writer)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(result); err != nil {
		return fmt.Errorf("write JSON output: %w", err)
	}
	return nil
}

func WriteHuman(writer io.Writer, result review.Result) error {
	var output strings.Builder
	output.WriteString("Reviewing staged changes...\n\n")
	if result.Summary.FilesChanged == 0 {
		output.WriteString("No staged changes found.\n")
		_, err := io.WriteString(writer, output.String())
		return err
	}

	fmt.Fprintf(
		&output,
		"%d %s changed, %d reviewed, %d skipped (%d %s, %d %s).\n",
		result.Summary.FilesChanged,
		plural(result.Summary.FilesChanged, "file", "files"),
		result.Summary.FilesReviewed,
		result.Summary.FilesSkipped,
		result.Summary.AddedLines,
		plural(result.Summary.AddedLines, "addition", "additions"),
		result.Summary.DeletedLines,
		plural(result.Summary.DeletedLines, "deletion", "deletions"),
	)
	if result.AI != nil {
		fmt.Fprintf(
			&output,
			"AI review (%s/%s): %d of %d batches succeeded, %d failed.\n",
			result.AI.Provider,
			result.AI.Model,
			result.AI.SuccessfulBatches,
			result.AI.BatchCount,
			result.AI.FailedBatches,
		)
		if len(result.AI.Agents) > 0 {
			fmt.Fprintf(&output, "AI agents: %s.\n", strings.Join(result.AI.Agents, ", "))
		}
		for _, failure := range result.AI.Failures {
			files := make([]string, 0, len(failure.Files))
			for _, file := range failure.Files {
				files = append(files, displayPath(file))
			}
			fmt.Fprintf(
				&output,
				"AI agent %s batch %d failed for %s: %s\n",
				failure.AgentID,
				failure.Batch,
				strings.Join(files, ", "),
				displayText(failure.Message),
			)
		}
	}
	if len(result.Findings) == 0 {
		output.WriteString("No findings.\n")
		_, err := io.WriteString(writer, output.String())
		return err
	}

	fmt.Fprintf(&output, "%d %s.\n", len(result.Findings), plural(len(result.Findings), "finding", "findings"))
	for _, finding := range result.Findings {
		output.WriteString("\n---\n")
		writeFinding(&output, finding)
	}
	_, err := io.WriteString(writer, output.String())
	return err
}

func writeFinding(output *strings.Builder, finding findings.Finding) {
	location := fmt.Sprintf("%s:%d", displayPath(finding.File), finding.StartLine)
	if finding.EndLine > finding.StartLine {
		location = fmt.Sprintf("%s:%d-%d", displayPath(finding.File), finding.StartLine, finding.EndLine)
	}
	fmt.Fprintf(
		output,
		"%s [%s]\n%s\n\n%s\n\n%s\n",
		strings.ToUpper(string(finding.Severity)),
		finding.Category,
		location,
		displayText(finding.Title),
		displayText(finding.Message),
	)
	if finding.Suggestion != "" {
		fmt.Fprintf(output, "\nSuggestion:\n%s\n", displayText(finding.Suggestion))
	}
	fmt.Fprintf(output, "Source: %s | Confidence: %.0f%%\n", finding.Source, finding.Confidence*100)
}

func displayText(value string) string {
	return strings.Map(func(character rune) rune {
		if character == '\n' || character == '\t' {
			return character
		}
		if unicode.IsControl(character) || unicode.Is(unicode.Cf, character) {
			return '\uFFFD'
		}
		return character
	}, value)
}

func displayPath(value string) string {
	if strings.IndexFunc(value, func(character rune) bool {
		return unicode.IsControl(character) || unicode.Is(unicode.Cf, character)
	}) >= 0 {
		return strconv.QuoteToASCII(value)
	}
	return value
}

func plural(count int, singular, plural string) string {
	if count == 1 {
		return singular
	}
	return plural
}
