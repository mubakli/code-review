package review

import (
	"context"
	"fmt"
	"strings"

	"code-review/internal/change"
	"code-review/internal/findings"
	"code-review/internal/pathfilter"
)

const SchemaVersion = 1

type Summary struct {
	FilesChanged  int `json:"filesChanged"`
	FilesReviewed int `json:"filesReviewed"`
	FilesSkipped  int `json:"filesSkipped"`
	HunksReviewed int `json:"hunksReviewed"`
	AddedLines    int `json:"addedLines"`
	DeletedLines  int `json:"deletedLines"`
	FindingCount  int `json:"findingCount"`
}

// Result is the stable envelope consumed by the CLI and future editor clients.
type Result struct {
	SchemaVersion int                `json:"schemaVersion"`
	Summary       Summary            `json:"summary"`
	Findings      []findings.Finding `json:"findings"`
}

type Service struct {
	matcher   pathfilter.Matcher
	analyzers []Analyzer
}

// Analyzer is the extension point for deterministic local analysis. The
// consumer owns this interface so implementations depend inward on review data.
type Analyzer interface {
	Name() string
	Analyze(context.Context, change.ChangeSet) ([]findings.Finding, error)
}

func New(matcher pathfilter.Matcher, analyzers ...Analyzer) *Service {
	return &Service{
		matcher:   matcher,
		analyzers: append([]Analyzer(nil), analyzers...),
	}
}

// ReviewChanges runs local analysis over an already parsed change set.
func (s *Service) ReviewChanges(ctx context.Context, changes change.ChangeSet) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	result := Result{
		SchemaVersion: SchemaVersion,
		Summary: Summary{
			FilesChanged: len(changes.Files),
		},
		Findings: make([]findings.Finding, 0),
	}

	filtered := change.ChangeSet{Files: make([]change.FileChange, 0, len(changes.Files))}
	reviewedPaths := make(map[string]struct{}, len(changes.Files))
	for _, file := range changes.Files {
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		path := file.Path()
		if path == "" || file.Binary || s.matcher.Excludes(path) {
			result.Summary.FilesSkipped++
			continue
		}

		filtered.Files = append(filtered.Files, file)
		reviewedPaths[path] = struct{}{}
		result.Summary.FilesReviewed++
		result.Summary.HunksReviewed += len(file.Hunks)
		for _, hunk := range file.Hunks {
			for _, line := range hunk.Lines {
				switch line.Kind {
				case change.LineAdded:
					result.Summary.AddedLines++
				case change.LineDeleted:
					result.Summary.DeletedLines++
				}
			}
		}
	}

	for _, localAnalyzer := range s.analyzers {
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		if localAnalyzer == nil {
			return Result{}, fmt.Errorf("run local analyzer: analyzer is nil")
		}
		name := strings.TrimSpace(localAnalyzer.Name())
		if name == "" {
			return Result{}, fmt.Errorf("run local analyzer: analyzer name is empty")
		}
		values, err := localAnalyzer.Analyze(ctx, filtered)
		if err != nil {
			return Result{}, fmt.Errorf("run %s analyzer: %w", name, err)
		}
		for index, finding := range values {
			if err := finding.Validate(); err != nil {
				return Result{}, fmt.Errorf("run %s analyzer: finding %d is invalid: %w", name, index+1, err)
			}
			if _, exists := reviewedPaths[finding.File]; !exists {
				return Result{}, fmt.Errorf("run %s analyzer: finding %d references a file outside the reviewed changes", name, index+1)
			}
		}
		result.Findings = append(result.Findings, values...)
	}

	findings.Sort(result.Findings)
	result.Summary.FindingCount = len(result.Findings)
	return result, nil
}
