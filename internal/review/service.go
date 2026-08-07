package review

import (
	"context"
	"fmt"
	"strings"

	"code-review/internal/analyzer"
	"code-review/internal/findings"
	gitrepository "code-review/internal/git"
	"code-review/internal/gitdiff"
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
	analyzers []analyzer.Analyzer
}

func New(matcher pathfilter.Matcher, analyzers ...analyzer.Analyzer) *Service {
	return &Service{
		matcher:   matcher,
		analyzers: append([]analyzer.Analyzer(nil), analyzers...),
	}
}

func NewDefault(extraExcludes ...string) *Service {
	patterns := pathfilter.DefaultPatterns()
	patterns = append(patterns, extraExcludes...)
	return New(pathfilter.New(patterns), analyzer.SecretAnalyzer{})
}

// ReviewStaged reads and analyzes only the repository's staged patch.
func (s *Service) ReviewStaged(ctx context.Context, directory string) (Result, error) {
	repository, err := gitrepository.Open(ctx, directory)
	if err != nil {
		return Result{}, err
	}
	patch, err := repository.StagedDiff(ctx)
	if err != nil {
		return Result{}, err
	}
	changes, err := gitdiff.Parse(patch)
	if err != nil {
		return Result{}, fmt.Errorf("parse staged diff: %w", err)
	}
	return s.ReviewChanges(ctx, changes)
}

// ReviewChanges runs local analysis over an already parsed change set.
func (s *Service) ReviewChanges(ctx context.Context, changes gitdiff.ChangeSet) (Result, error) {
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

	filtered := gitdiff.ChangeSet{Files: make([]gitdiff.FileChange, 0, len(changes.Files))}
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
		result.Summary.FilesReviewed++
		result.Summary.HunksReviewed += len(file.Hunks)
		for _, hunk := range file.Hunks {
			for _, line := range hunk.Lines {
				switch line.Kind {
				case gitdiff.LineAdded:
					result.Summary.AddedLines++
				case gitdiff.LineDeleted:
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
		}
		result.Findings = append(result.Findings, values...)
	}

	findings.Sort(result.Findings)
	result.Summary.FindingCount = len(result.Findings)
	return result, nil
}
