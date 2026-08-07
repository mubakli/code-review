package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"code-review/internal/config"
	"code-review/internal/git"
	"code-review/internal/output"
	"code-review/internal/pathfilter"
	"code-review/internal/review"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(run(ctx, os.Args[1:], os.Stdout, os.Stderr, "."))
}

func run(ctx context.Context, arguments []string, stdout, stderr io.Writer, workDirectory string) int {
	if len(arguments) == 0 {
		writeUsage(stderr)
		return 2
	}
	if arguments[0] == "help" || arguments[0] == "-h" || arguments[0] == "--help" {
		writeUsage(stdout)
		return 0
	}
	if arguments[0] == "snapshot" {
		return runSnapshot(ctx, arguments[1:], stdout, stderr, workDirectory)
	}
	if arguments[0] != "review" {
		fmt.Fprintf(stderr, "unknown command %q\n\n", arguments[0])
		writeUsage(stderr)
		return 2
	}

	flags := flag.NewFlagSet("review", flag.ContinueOnError)
	flags.SetOutput(stderr)
	staged := flags.Bool("staged", false, "review changes staged in the Git index")
	format := flags.String("format", "human", "output format: human or json")
	repository := flags.String("repo", workDirectory, "path inside the Git repository")
	aiProvider := flags.String("ai-provider", string(config.AIProviderNone), "AI provider: none or openai")
	aiModel := flags.String("ai-model", "", "AI model name (required when AI is enabled)")
	expectedReviewID := flags.String("expected-review-id", "", "reject review if the staged snapshot no longer matches this ID")
	var excludes stringListFlag
	flags.Var(&excludes, "exclude", "additional path pattern to exclude (repeatable)")
	flags.Usage = func() {
		fmt.Fprintln(stderr, "Usage: reviewer review --staged [--format human|json] [--repo PATH] [--exclude PATTERN] [--ai-provider PROVIDER --ai-model MODEL]")
		fmt.Fprintln(stderr)
		flags.PrintDefaults()
	}
	if err := flags.Parse(arguments[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintf(stderr, "review does not accept positional arguments: %v\n", flags.Args())
		return 2
	}
	if !*staged {
		fmt.Fprintln(stderr, "review currently requires --staged")
		return 2
	}
	if *format != "human" && *format != "json" {
		fmt.Fprintf(stderr, "unsupported output format %q; use human or json\n", *format)
		return 2
	}
	aiConfig := config.DefaultAI()
	aiConfig.Provider = config.AIProvider(*aiProvider)
	aiConfig.Model = *aiModel
	if err := aiConfig.Validate(); err != nil {
		fmt.Fprintf(stderr, "invalid AI configuration: %v\n", err)
		return 2
	}

	result, err := reviewStaged(ctx, *repository, reviewOptions{ExtraExcludes: excludes, AI: aiConfig, ExpectedID: *expectedReviewID})
	if err != nil {
		fmt.Fprintf(stderr, "review failed: %v\n", err)
		return 1
	}
	if *format == "json" {
		err = output.WriteJSON(stdout, result)
	} else {
		err = output.WriteHuman(stdout, result)
	}
	if err != nil {
		fmt.Fprintf(stderr, "review failed: %v\n", err)
		return 1
	}
	return 0
}

func runSnapshot(ctx context.Context, arguments []string, stdout, stderr io.Writer, workDirectory string) int {
	flags := flag.NewFlagSet("snapshot", flag.ContinueOnError)
	flags.SetOutput(stderr)
	staged := flags.Bool("staged", false, "identify changes staged in the Git index")
	repositoryPath := flags.String("repo", workDirectory, "path inside the Git repository")
	if err := flags.Parse(arguments); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() != 0 || !*staged {
		fmt.Fprintln(stderr, "snapshot requires --staged and accepts no positional arguments")
		return 2
	}
	repository, err := git.Open(ctx, *repositoryPath)
	if err != nil {
		fmt.Fprintf(stderr, "snapshot failed: %v\n", err)
		return 1
	}
	snapshot, err := repository.StagedSnapshot(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "snapshot failed: %v\n", err)
		return 1
	}
	if err := json.NewEncoder(stdout).Encode(struct {
		SchemaVersion int    `json:"schemaVersion"`
		ReviewID      string `json:"reviewId"`
		FilesChanged  int    `json:"filesChanged"`
	}{
		SchemaVersion: review.SchemaVersion,
		ReviewID:      snapshot.ID,
		FilesChanged:  len(snapshot.Changes.Files),
	}); err != nil {
		fmt.Fprintf(stderr, "snapshot failed: write JSON output: %v\n", err)
		return 1
	}
	return 0
}

func writeUsage(writer io.Writer) {
	fmt.Fprintln(writer, "Local-first review of staged Git changes.")
	fmt.Fprintln(writer)
	fmt.Fprintln(writer, "Usage:")
	fmt.Fprintln(writer, "  reviewer review --staged [--format human|json] [--repo PATH] [--exclude PATTERN] [--ai-provider PROVIDER --ai-model MODEL]")
	fmt.Fprintln(writer, "  reviewer snapshot --staged [--repo PATH]")
}

type stringListFlag []string

func (f *stringListFlag) String() string {
	return strings.Join(*f, ",")
}

func (f *stringListFlag) Set(value string) error {
	if err := pathfilter.ValidatePattern(value); err != nil {
		return err
	}
	*f = append(*f, value)
	return nil
}
