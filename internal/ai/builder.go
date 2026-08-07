package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"code-review/internal/change"
	"code-review/internal/findings"
	"code-review/internal/redact"
)

const ReviewInstructions = `Review only issues introduced by these staged changes. Focus on security, correctness, performance, database usage, and maintainability. Report findings on changed lines with evidence from the supplied diff. Do not invent runtime behavior or missing context. Phrase database index and query recommendations as hypotheses that require measurement or EXPLAIN. Return structured findings only.`

const (
	partialHunkMarker = "# reviewer: partial hunk split to respect the token budget\n"
	truncatedMarker   = "\n[reviewer: long diff line truncated to respect the token budget]\n"
)

type Batch struct {
	Request         AnalysisRequest
	Files           []string
	EstimatedTokens int
	DiffTokens      int
	RedactionCount  int
	Truncated       bool
	OmittedFindings int
}

type Builder struct {
	budget          Budget
	diffTokenLimit  int
	instructionCost int
}

func New(budget Budget) (Builder, error) {
	instructionCost := EstimateTokens(ReviewInstructions)
	diffLimit, err := budget.diffLimit(instructionCost)
	if err != nil {
		return Builder{}, err
	}
	return Builder{
		budget:          budget,
		diffTokenLimit:  diffLimit,
		instructionCost: instructionCost,
	}, nil
}

// Build creates language-independent, file-first review batches from an
// already scoped change set. Unsupported languages use this diff-only path;
// future symbol parsers can add context without replacing it.
func (b Builder) Build(ctx context.Context, changes change.ChangeSet, staticFindings []findings.Finding) ([]Batch, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	for index, finding := range staticFindings {
		if err := finding.Validate(); err != nil {
			return nil, fmt.Errorf("static finding %d is invalid: %w", index+1, err)
		}
	}

	fragments := make([]fragment, 0, len(changes.Files))
	for _, file := range changes.Files {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		path := file.Path()
		if path == "" || file.Binary {
			continue
		}
		fileFragments, err := splitFile(ctx, file, b.diffTokenLimit)
		if err != nil {
			return nil, fmt.Errorf("build prompt for %s: %w", path, err)
		}
		fragments = append(fragments, fileFragments...)
	}
	if len(fragments) == 0 {
		return []Batch{}, nil
	}

	batches := make([]Batch, 0, len(fragments))
	current := make([]fragment, 0)
	currentText := ""
	flush := func() {
		if len(current) == 0 {
			return
		}
		batches = append(batches, b.makeBatch(currentText, current, staticFindings))
		current = current[:0]
		currentText = ""
	}

	for _, value := range fragments {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		separator := ""
		if currentText != "" {
			separator = "\n"
		}
		candidate := currentText + separator + value.text
		if currentText != "" && EstimateTokens(candidate) > b.diffTokenLimit {
			flush()
			separator = ""
			candidate = value.text
		}
		if EstimateTokens(candidate) > b.diffTokenLimit {
			return nil, fmt.Errorf("prompt fragment for %s exceeds diff token budget", value.file)
		}
		current = append(current, value)
		currentText += separator + value.text
	}
	flush()
	return batches, nil
}

func (b Builder) makeBatch(diff string, fragments []fragment, staticFindings []findings.Finding) Batch {
	files := make([]string, 0, len(fragments))
	fileSet := make(map[string]struct{}, len(fragments))
	truncated := false
	for _, value := range fragments {
		if _, exists := fileSet[value.file]; !exists {
			fileSet[value.file] = struct{}{}
			files = append(files, value.file)
		}
		truncated = truncated || value.truncated
	}

	selected, omitted := selectFindings(staticFindings, fileSet, b.budget.MaxStaticFindingTokens)
	redactionCount := 0
	for _, value := range fragments {
		redactionCount += value.redactionCount
	}
	request := newAnalysisRequest(ReviewInstructions, diff, selected, redactionCount)
	diffTokens := EstimateTokens(request.Diff())
	estimated := b.instructionCost + diffTokens + estimateFindings(selected)
	return Batch{
		Request:         request,
		Files:           files,
		EstimatedTokens: estimated,
		DiffTokens:      diffTokens,
		RedactionCount:  request.RedactionCount(),
		Truncated:       truncated,
		OmittedFindings: omitted,
	}
}

type fragment struct {
	file           string
	text           string
	truncated      bool
	redactionCount int
}

func splitFile(ctx context.Context, file change.FileChange, tokenLimit int) ([]fragment, error) {
	headerResult := redact.Secrets(renderFileHeader(file))
	bodyResult := redact.Secrets(renderFileBody(file))
	header := headerResult.Text
	body := bodyResult.Text
	full := header + body
	if EstimateTokens(full) <= tokenLimit {
		return []fragment{{file: file.Path(), text: full, redactionCount: headerResult.Count + bodyResult.Count}}, nil
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
			result = append(result, fragment{file: file.Path(), text: current, truncated: truncated, redactionCount: strings.Count(current, redact.Placeholder)})
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
		result = append(result, fragment{file: file.Path(), text: current, truncated: truncated, redactionCount: strings.Count(current, redact.Placeholder)})
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

func selectFindings(values []findings.Finding, files map[string]struct{}, tokenLimit int) ([]findings.Finding, int) {
	selected := make([]findings.Finding, 0)
	omitted := 0
	for _, finding := range values {
		if _, relevant := files[finding.File]; !relevant {
			continue
		}
		candidate := append(selected, finding)
		if estimateFindings(candidate) > tokenLimit {
			omitted++
			continue
		}
		selected = candidate
	}
	return selected, omitted
}

func estimateFindings(values []findings.Finding) int {
	if len(values) == 0 {
		return 0
	}
	encoded, err := json.Marshal(values)
	if err != nil {
		return 0
	}
	return EstimateTokens(string(encoded))
}
