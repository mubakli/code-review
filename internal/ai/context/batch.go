package context

import (
	stdcontext "context"
	"encoding/json"
	"fmt"

	"code-review/internal/change"
	"code-review/internal/findings"
)

// PreparedContext is the redacted, token-budgeted view of a change set, ready
// to be turned into provider requests by the request layer. It never contains
// a provider credential or raw secret.
type PreparedContext struct {
	Batches []PreparedBatch
}

// PreparedBatch carries one budgeted slice of redacted diff text with the
// findings and related staged context selected for it.
type PreparedBatch struct {
	Diff            string
	Files           []string
	EstimatedTokens int
	DiffTokens      int
	RedactionCount  int
	Truncated       bool
	OmittedFindings int
	StaticFindings  []findings.Finding
	RelatedContext  []ContextFile
}

// Preparer extracts, redacts, budgets, and batches a change set into
// PreparedContext. It performs no provider calls and knows nothing about
// providers; the only prompt it sees is the token cost of the instructions.
type Preparer struct {
	budget Budget
}

func NewPreparer(budget Budget) (Preparer, error) {
	if err := budget.Validate(); err != nil {
		return Preparer{}, err
	}
	return Preparer{budget: budget}, nil
}

// Prepare builds the prepared context for one agent prompt. The diff budget
// for each batch is derived from the prompt's token cost, so a longer prompt
// leaves less room for diff content.
func (p Preparer) Prepare(ctx stdcontext.Context, changes change.ChangeSet, staticFindings []findings.Finding, relatedContext []ContextFile, prompt string) (*PreparedContext, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	for index, finding := range staticFindings {
		if err := finding.Validate(); err != nil {
			return nil, fmt.Errorf("static finding %d is invalid: %w", index+1, err)
		}
	}
	diffTokenLimit, err := p.budget.DiffLimit(EstimateTokens(prompt))
	if err != nil {
		return nil, err
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
		fileFragments, err := extractFragments(ctx, file, diffTokenLimit)
		if err != nil {
			return nil, fmt.Errorf("prepare context for %s: %w", path, err)
		}
		fragments = append(fragments, fileFragments...)
	}
	if len(fragments) == 0 {
		return &PreparedContext{}, nil
	}

	batches := make([]PreparedBatch, 0, len(fragments))
	current := make([]fragment, 0)
	currentText := ""
	flush := func() {
		if len(current) == 0 {
			return
		}
		batches = append(batches, p.makeBatch(currentText, current, staticFindings, relatedContext, prompt))
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
		if currentText != "" && EstimateTokens(candidate) > diffTokenLimit {
			flush()
			separator = ""
			candidate = value.text
		}
		if EstimateTokens(candidate) > diffTokenLimit {
			return nil, fmt.Errorf("prepared fragment for %s exceeds diff token budget", value.file)
		}
		current = append(current, value)
		currentText += separator + value.text
	}
	flush()
	return &PreparedContext{Batches: batches}, nil
}

// makeBatch selects the static findings and related context for one batch and
// records its token cost against the prompt and diff budgets.
func (p Preparer) makeBatch(diff string, fragments []fragment, staticFindings []findings.Finding, relatedContext []ContextFile, prompt string) PreparedBatch {
	files := make([]string, 0, len(fragments))
	fileSet := make(map[string]struct{}, len(fragments))
	truncated := false
	redactionCount := 0
	for _, value := range fragments {
		if _, exists := fileSet[value.file]; !exists {
			fileSet[value.file] = struct{}{}
			files = append(files, value.file)
		}
		truncated = truncated || value.truncated
		redactionCount += value.redactionCount
	}

	selected, omitted := selectFindings(staticFindings, fileSet, p.budget.MaxStaticFindingTokens)
	batchContext := selectContext(fileSet, relatedContext)
	diffTokens := EstimateTokens(diff)
	estimated := EstimateTokens(prompt) + diffTokens + estimateFindings(selected)
	return PreparedBatch{
		Diff:            diff,
		Files:           files,
		EstimatedTokens: estimated,
		DiffTokens:      diffTokens,
		RedactionCount:  redactionCount,
		Truncated:       truncated,
		OmittedFindings: omitted,
		StaticFindings:  selected,
		RelatedContext:  batchContext,
	}
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

// selectContext attaches the related context files relevant to one batch. A
// context file with a non-empty RelatedTo list is attached only when the batch
// covers one of the changed files it references; a file with an empty
// RelatedTo (explicitly requested, or resolved without an owner match) is
// attached to every batch. Total content stays within the per-request byte
// budget.
func selectContext(files map[string]struct{}, context []ContextFile) []ContextFile {
	if len(context) == 0 {
		return nil
	}
	selected := make([]ContextFile, 0, len(context))
	total := 0
	for _, candidate := range context {
		if len(candidate.RelatedTo) > 0 && !relatesToBatch(candidate.RelatedTo, files) {
			continue
		}
		content := truncateBytes(candidate.Content, MaxContextFileBytes)
		remaining := MaxContextTotalBytes - total
		if remaining <= len(contextTruncatedMarker) {
			break
		}
		if len(content) > remaining {
			content = truncateBytes(content, remaining)
		}
		total += len(content)
		selected = append(selected, ContextFile{Path: candidate.Path, Content: content, RelatedTo: candidate.RelatedTo})
	}
	return selected
}

// relatesToBatch reports whether any related changed file is part of the batch.
func relatesToBatch(relatedTo []string, files map[string]struct{}) bool {
	for _, path := range relatedTo {
		if _, ok := files[path]; ok {
			return true
		}
	}
	return false
}
