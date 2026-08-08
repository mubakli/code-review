package request

import (
	stdcontext "context"
	"encoding/json"
	"strings"
	"testing"

	"code-review/internal/ai/context"
	"code-review/internal/change"
)

func TestRequestBuilderBuildsWithConsistentBudgetAndState(t *testing.T) {
	t.Parallel()

	preparer, err := context.NewPreparer(context.DefaultBudget())
	if err != nil {
		t.Fatalf("NewPreparer() error = %v", err)
	}
	secret := "actual-secret-value"
	changes := change.ChangeSet{Files: []change.FileChange{{
		NewPath: "main.py",
		Status:  change.StatusAdded,
		Hunks: []change.Hunk{{
			OldStart: 0,
			OldLines: 0,
			NewStart: 1,
			NewLines: 1,
			Lines:    []change.Line{{Kind: change.LineAdded, NewLine: 1, Content: `token = "` + secret + `"`}},
		}},
	}}}
	prepared, err := preparer.Prepare(stdcontext.Background(), changes, nil, []context.ContextFile{{Path: "main.py", Content: "related content"}}, ReviewPrompt)
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	if len(prepared.Batches) != 1 {
		t.Fatalf("len(batches) = %d, want 1", len(prepared.Batches))
	}

	analysisRequest, err := (RequestBuilder{}).Build(prepared.Batches[0], ReviewPrompt)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if analysisRequest.Instructions() != ReviewPrompt {
		t.Fatalf("Instructions() = %q", analysisRequest.Instructions())
	}
	if analysisRequest.RedactionCount() <= 0 {
		t.Fatalf("RedactionCount() = %d, want > 0", analysisRequest.RedactionCount())
	}
	files := analysisRequest.ContextFiles()
	if len(files) != 1 || files[0].Path != "main.py" {
		t.Fatalf("ContextFiles() = %#v", files)
	}

	encoded, err := analysisRequest.MarshalJSON()
	if err != nil {
		t.Fatalf("MarshalJSON() error = %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("unmarshal encoded request: %v", err)
	}
	if _, ok := decoded["diff"]; !ok {
		t.Fatal("encoded request is missing diff")
	}
	if strings.Contains(string(encoded), secret) {
		t.Fatalf("encoded request leaks the secret: %s", encoded)
	}
}

func TestRequestBuildRequiresPromptAndDiff(t *testing.T) {
	t.Parallel()

	preparer, err := context.NewPreparer(context.DefaultBudget())
	if err != nil {
		t.Fatalf("NewPreparer() error = %v", err)
	}
	prepared, err := preparer.Prepare(stdcontext.Background(), change.ChangeSet{
		Files: []change.FileChange{{
			NewPath: "main.py",
			Status:  change.StatusAdded,
			Hunks: []change.Hunk{{
				OldStart: 0,
				OldLines: 0,
				NewStart: 1,
				NewLines: 1,
				Lines:    []change.Line{{Kind: change.LineAdded, NewLine: 1, Content: "print(1)"}},
			}},
		}},
	}, nil, nil, ReviewPrompt)
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	if len(prepared.Batches) != 1 {
		t.Fatalf("len(batches) = %d, want 1", len(prepared.Batches))
	}

	if _, err := (RequestBuilder{}).Build(prepared.Batches[0], ""); err == nil {
		t.Fatal("Build() accepted an empty instructions")
	}
	if _, err := (RequestBuilder{}).Build(context.PreparedBatch{}, ReviewPrompt); err == nil {
		t.Fatal("Build() accepted an empty batch")
	}
}

func TestRequestMarshalJSONIncludesFieldList(t *testing.T) {
	t.Parallel()

	request := AnalysisRequest{
		instructions:   ReviewPrompt,
		diff:           "unrelated local text",
		staticFindings: nil,
		contextFiles:   []context.ContextFile{{Path: "main.py", Content: "related"}},
	}
	encoded, err := request.MarshalJSON()
	if err != nil {
		t.Fatalf("MarshalJSON() error = %v", err)
	}
	if !strings.Contains(string(encoded), `"instructions"`) || !strings.Contains(string(encoded), `"diff"`) || !strings.Contains(string(encoded), `"relatedContext"`) {
		t.Fatalf("MarshalJSON() missing expected fields: %s", encoded)
	}
}
