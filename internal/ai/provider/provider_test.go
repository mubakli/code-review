package provider

import (
	stdcontext "context"
	"testing"

	"code-review/internal/ai/context"
	"code-review/internal/ai/request"
	"code-review/internal/change"
)

// preparedRequest builds one real request end-to-end through the context and
// request layers, so execution tests exercise nothing but provider code.
func preparedRequest(t *testing.T, secret string) request.AnalysisRequest {
	t.Helper()
	preparer, err := context.NewPreparer(context.DefaultBudget())
	if err != nil {
		t.Fatalf("NewPreparer() error = %v", err)
	}
	changes := change.ChangeSet{Files: []change.FileChange{{
		NewPath: "service.go",
		Status:  change.StatusAdded,
		Hunks: []change.Hunk{{
			OldStart: 0,
			OldLines: 0,
			NewStart: 1,
			NewLines: 2,
			Lines: []change.Line{
				{Kind: change.LineAdded, NewLine: 1, Content: "package service"},
				{Kind: change.LineAdded, NewLine: 2, Content: `password = "` + secret + `"`},
			},
		}},
	}}}
	prepared, err := preparer.Prepare(stdcontext.Background(), changes, nil, nil, request.ReviewPrompt)
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	if len(prepared.Batches) != 1 {
		t.Fatalf("len(batches) = %d, want 1", len(prepared.Batches))
	}
	analysisRequest, err := (request.RequestBuilder{}).Build(prepared.Batches[0], request.ReviewPrompt)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	return analysisRequest
}
