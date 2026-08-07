package review

import (
	"context"
	"fmt"
	"strings"

	"code-review/internal/change"
	"code-review/internal/findings"
	"code-review/internal/pathfilter"
)

const SchemaVersion = 2

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
	ReviewID      string             `json:"reviewId"`
	Summary       Summary            `json:"summary"`
	Files         []ReviewedFile     `json:"files"`
	Findings      []findings.Finding `json:"findings"`
	AI            *AISummary         `json:"ai,omitempty"`
}

type ReviewedFile struct {
	Path         string `json:"path"`
	PreviousPath string `json:"previousPath,omitempty"`
	Status       string `json:"status"`
	Binary       bool   `json:"binary,omitempty"`
}

type AISummary struct {
	Provider          string      `json:"provider"`
	Model             string      `json:"model"`
	Agents            []string    `json:"agents"`
	ReviewedFiles     []string    `json:"reviewedFiles"`
	BatchCount        int         `json:"batchCount"`
	SuccessfulBatches int         `json:"successfulBatches"`
	FailedBatches     int         `json:"failedBatches"`
	Failures          []AIFailure `json:"failures,omitempty"`
}

type AIFailure struct {
	AgentID string   `json:"agentId"`
	Batch   int      `json:"batch"`
	Files   []string `json:"files"`
	Message string   `json:"message"`
}

type Service struct {
	matcher   pathfilter.Matcher
	analyzers []Analyzer
}

// Scope is the single filtered view shared by local and optional AI review.
// Its fields are private so callers cannot construct an unfiltered scope.
type Scope struct {
	changes       change.ChangeSet
	summary       Summary
	reviewedPaths map[string]struct{}
}

func (s Scope) Changes() change.ChangeSet {
	return s.changes.Clone()
}

func (s Scope) Summary() Summary {
	return s.summary
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
	scope, err := s.ScopeChanges(ctx, changes)
	if err != nil {
		return Result{}, err
	}
	return s.ReviewScope(ctx, scope)
}

// ScopeChanges applies path and binary policy once so local and AI review can
// consume exactly the same files.
func (s *Service) ScopeChanges(ctx context.Context, changes change.ChangeSet) (Scope, error) {
	if err := ctx.Err(); err != nil {
		return Scope{}, err
	}
	scope := Scope{
		summary: Summary{
			FilesChanged: len(changes.Files),
		},
		changes:       change.ChangeSet{Files: make([]change.FileChange, 0, len(changes.Files))},
		reviewedPaths: make(map[string]struct{}, len(changes.Files)),
	}

	for _, file := range changes.Files {
		if err := ctx.Err(); err != nil {
			return Scope{}, err
		}
		path := file.Path()
		if path == "" || file.Binary || s.matcher.Excludes(path) {
			scope.summary.FilesSkipped++
			continue
		}

		scope.changes.Files = append(scope.changes.Files, file)
		scope.reviewedPaths[path] = struct{}{}
		scope.summary.FilesReviewed++
		scope.summary.HunksReviewed += len(file.Hunks)
		for _, hunk := range file.Hunks {
			for _, line := range hunk.Lines {
				switch line.Kind {
				case change.LineAdded:
					scope.summary.AddedLines++
				case change.LineDeleted:
					scope.summary.DeletedLines++
				}
			}
		}
	}
	scope.changes = scope.changes.Clone()
	return scope, nil
}

// ReviewScope executes deterministic analyzers over a previously filtered
// scope.
func (s *Service) ReviewScope(ctx context.Context, scope Scope) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	result := Result{
		SchemaVersion: SchemaVersion,
		Summary:       scope.summary,
		Files:         make([]ReviewedFile, 0, len(scope.changes.Files)),
		Findings:      make([]findings.Finding, 0),
	}
	for _, file := range scope.changes.Files {
		previousPath := ""
		if file.Status == change.StatusRenamed || file.Status == change.StatusCopied {
			previousPath = file.OldPath
		}
		result.Files = append(result.Files, ReviewedFile{
			Path:         file.Path(),
			PreviousPath: previousPath,
			Status:       string(file.Status),
			Binary:       file.Binary,
		})
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
		values, err := localAnalyzer.Analyze(ctx, scope.changes)
		if err != nil {
			return Result{}, fmt.Errorf("run %s analyzer: %w", name, err)
		}
		for index, finding := range values {
			if err := finding.Validate(); err != nil {
				return Result{}, fmt.Errorf("run %s analyzer: finding %d is invalid: %w", name, index+1, err)
			}
			if _, exists := scope.reviewedPaths[finding.File]; !exists {
				return Result{}, fmt.Errorf("run %s analyzer: finding %d references a file outside the reviewed changes", name, index+1)
			}
		}
		result.Findings = append(result.Findings, values...)
	}

	findings.Sort(result.Findings)
	result.Summary.FindingCount = len(result.Findings)
	return result, nil
}
