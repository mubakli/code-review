package main

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"code-review/internal/ai"
	aicontext "code-review/internal/ai/context"
	"code-review/internal/ai/egress"
	"code-review/internal/ai/provider"
	"code-review/internal/ai/request"
	"code-review/internal/config"
	"code-review/internal/git"
)

func newTestResolver(t *testing.T, repository string) stagedContextResolver {
	t.Helper()
	repo, err := git.Open(context.Background(), repository)
	if err != nil {
		t.Fatalf("git.Open() error = %v", err)
	}
	policy, err := egress.New(egress.DefaultRules())
	if err != nil {
		t.Fatalf("egress.New() error = %v", err)
	}
	return stagedContextResolver{repository: repo, egress: policy}
}

func initTestRepository(t *testing.T) string {
	t.Helper()
	repository := t.TempDir()
	runTestGit(t, repository, "init", "--quiet")
	runTestGit(t, repository, "config", "user.email", "reviewer@example.com")
	runTestGit(t, repository, "config", "user.name", "Reviewer Test")
	return repository
}

// TestStagedContextResolverReturnsRelatedFilesWithOwners verifies that
// surrounding files referencing the diff's symbols are returned, that changed
// files are never re-sent as context, and that each related file is attributed
// back to the changed file whose symbols it references (the pipeline fix that
// lets related context reach the provider's batch selection).
func TestStagedContextResolverReturnsRelatedFilesWithOwners(t *testing.T) {
	requireGit(t)

	repository := initTestRepository(t)
	writeSource(t, repository, "shared/auth.go", "package shared\n\nfunc Authenticate(req string) bool { return true }\n")
	writeSource(t, repository, "services/user_service.go", "package services\n\nfunc FindUser(id string) {}\n")
	writeSource(t, repository, "controllers/user.go", "package controllers\n\nfunc User() {}\n")
	runTestGit(t, repository, "add", "--", "shared/auth.go", "services/user_service.go", "controllers/user.go")
	runTestGit(t, repository, "commit", "-qm", "init")

	writeSource(t, repository, "controllers/user.go", "package controllers\n\nfunc User() {\n\tAuthenticate(req)\n\tFindUser(\"42\")\n}\n")
	runTestGit(t, repository, "add", "--", "controllers/user.go")

	resolver := newTestResolver(t, repository)
	snapshot, err := resolver.repository.StagedSnapshot(context.Background())
	if err != nil {
		t.Fatalf("StagedSnapshot() error = %v", err)
	}
	result, err := resolver.Resolve(context.Background(), snapshot.Changes, aicontext.ContextRequest{
		Symbols: []string{"Authenticate", "FindUser"},
		Intent:  aicontext.ContextIntentAuthorization,
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if len(result.Files) != 2 {
		t.Fatalf("len(Files) = %d, want 2: %#v", len(result.Files), result.Files)
	}
	paths := make(map[string]struct{}, len(result.Files))
	for _, file := range result.Files {
		if file.Path == "controllers/user.go" {
			t.Fatalf("changed file was re-sent as related context")
		}
		if !strings.Contains(file.Content, "Authenticate") && !strings.Contains(file.Content, "FindUser") {
			t.Fatalf("context file %s does not reference the requested symbols:\n%s", file.Path, file.Content)
		}
		if !containsString(file.RelatedTo, "controllers/user.go") {
			t.Fatalf("context file %s RelatedTo = %#v, want controllers/user.go", file.Path, file.RelatedTo)
		}
		paths[file.Path] = struct{}{}
	}
	for _, want := range []string{"shared/auth.go", "services/user_service.go"} {
		if _, ok := paths[want]; !ok {
			t.Fatalf("related file %q was not returned: %#v", want, result.Files)
		}
	}
}

// TestStagedContextResolverBoundsRelatedFiles is the regression test for the
// 3-file cap: the old limit subtracted the count of changed files (len(seen))
// instead of the count of already-resolved context files, so a diff with three
// or more changed files disabled slicing and could return every candidate.
func TestStagedContextResolverBoundsRelatedFiles(t *testing.T) {
	requireGit(t)

	repository := initTestRepository(t)
	for index := 1; index <= 6; index++ {
		writeSource(t, repository, filepath.Join("helpers", fmt.Sprintf("util%d.go", index)),
			fmt.Sprintf("package helpers\n\nfunc sharedHelper() {}\nfunc unique%d() {}\n", index))
	}
	runTestGit(t, repository, "add", "--", "helpers")
	runTestGit(t, repository, "commit", "-qm", "init")

	for index := 1; index <= 5; index++ {
		writeSource(t, repository, filepath.Join("apps", fmt.Sprintf("app%d.go", index)),
			fmt.Sprintf("package apps\n\nfunc Run%d() {\n\tunique%d()\n}\n", index, index))
	}
	runTestGit(t, repository, "add", "--", "apps")

	resolver := newTestResolver(t, repository)
	snapshot, err := resolver.repository.StagedSnapshot(context.Background())
	if err != nil {
		t.Fatalf("StagedSnapshot() error = %v", err)
	}
	symbols := []string{"unique1", "unique2", "unique3", "unique4", "unique5", "unique6"}
	result, err := resolver.Resolve(context.Background(), snapshot.Changes, aicontext.ContextRequest{
		Symbols: symbols,
		Intent:  aicontext.ContextIntentAuthorization,
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if len(result.Files) != maxRelatedContextFiles {
		t.Fatalf("len(Files) = %d, want %d; five changed files were counted against the limit", len(result.Files), maxRelatedContextFiles)
	}
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

// TestReviewStagedAttachesRelatedContextToDeepSecurity is the end-to-end
// regression for the dropped-context bug: a deep security review of a changed
// file that calls into surrounding code must receive that surrounding code as
// context in the provider request, and changed files must still be excluded.
func TestReviewStagedAttachesRelatedContextToDeepSecurity(t *testing.T) {
	requireGit(t)

	repository := initTestRepository(t)
	writeSource(t, repository, "middleware/auth.go", "package middleware\n\nfunc Authenticate(req string) bool { return true }\n")
	writeSource(t, repository, "controllers/user.go", "package controllers\n\nfunc User() {}\n")
	runTestGit(t, repository, "add", "--", "middleware/auth.go", "controllers/user.go")
	runTestGit(t, repository, "commit", "-qm", "init")

	writeSource(t, repository, "controllers/user.go", "package controllers\n\nfunc User() {\n\tAuthenticate(req)\n\tpassword := request.FormValue(\"password\")\n}\n")
	runTestGit(t, repository, "add", "--", "controllers/user.go")

	var contextFiles []aicontext.ContextFile
	provider := provider.Mock{AnalyzeFunc: func(_ context.Context, analysisRequest request.AnalysisRequest) (*provider.AnalysisResponse, error) {
		contextFiles = analysisRequest.ContextFiles()
		return &provider.AnalysisResponse{Status: provider.ResponseStatusComplete}, nil
	}}

	result, err := reviewStaged(context.Background(), repository, reviewOptions{
		AI: config.AI{
			Provider:        config.AIProviderOpenAI,
			Model:           "review-model",
			MaxOutputTokens: 1000,
			Agents:          []string{"security"},
		},
		Provider: provider,
	})
	if err != nil {
		t.Fatalf("reviewStaged() error = %v", err)
	}
	if result.AI == nil || !containsString(result.AI.Agents, string(ai.AgentSecurity)) {
		t.Fatalf("deep security agent did not run: %#v", result.AI)
	}
	if len(contextFiles) != 1 || contextFiles[0].Path != "middleware/auth.go" {
		t.Fatalf("deep security context = %#v, want middleware/auth.go", contextFiles)
	}
	if !strings.Contains(contextFiles[0].Content, "Authenticate") {
		t.Fatalf("context content does not reference the changed symbol:\n%s", contextFiles[0].Content)
	}
}
