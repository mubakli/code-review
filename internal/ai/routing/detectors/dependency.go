package detectors

import (
	"fmt"
	"strings"

	"code-review/internal/ai/routing"
	"code-review/internal/change"
)

// DependencyDetector signals dependency manifest changes and package manager
// operations: supply-chain movement in the staged diff.
type DependencyDetector struct{}

var dependencyManifests = []string{
	"package.json", "package-lock.json", "npm-shrinkwrap.json", "yarn.lock",
	"go.mod", "go.sum", "vendor/modules.txt",
	"requirements.txt", "pipfile", "poetry.lock",
	"pom.xml", "build.gradle", "gemfile", "gemfile.lock",
	"composer.json", "composer.lock", "cargo.toml", "cargo.lock",
	".nvmrc",
}

var dependencyTerms = []term{
	{word: "npm install", surface: routing.SurfaceSupplyChain, confidence: routing.ConfidenceMedium},
	{word: "npm add", surface: routing.SurfaceSupplyChain, confidence: routing.ConfidenceMedium},
	{word: "yarn add", surface: routing.SurfaceSupplyChain, confidence: routing.ConfidenceMedium},
	{word: "yarn install", surface: routing.SurfaceSupplyChain, confidence: routing.ConfidenceMedium},
	{word: "go get", surface: routing.SurfaceSupplyChain, confidence: routing.ConfidenceMedium},
	{word: "go install", surface: routing.SurfaceSupplyChain, confidence: routing.ConfidenceMedium},
	{word: "go mod", surface: routing.SurfaceSupplyChain, confidence: routing.ConfidenceMedium},
	{word: "pip install", surface: routing.SurfaceSupplyChain, confidence: routing.ConfidenceMedium},
	{word: "gem install", surface: routing.SurfaceSupplyChain, confidence: routing.ConfidenceMedium},
	{word: "cargo add", surface: routing.SurfaceSupplyChain, confidence: routing.ConfidenceMedium},
	{word: "cargo install", surface: routing.SurfaceSupplyChain, confidence: routing.ConfidenceMedium},
	{word: "brew install", surface: routing.SurfaceSupplyChain, confidence: routing.ConfidenceLow},
	{word: "apt-get install", surface: routing.SurfaceSupplyChain, confidence: routing.ConfidenceLow},
}

func (DependencyDetector) Detect(changes change.ChangeSet) []routing.Signal {
	var signals []routing.Signal
	for _, file := range changes.Files {
		path := file.Path()
		for _, manifest := range dependencyManifests {
			if strings.HasSuffix(strings.ToLower(path), manifest) {
				signals = append(signals, signal(routing.SignalDependency, routing.SurfaceSupplyChain, routing.ConfidenceHigh, path,
					fmt.Sprintf("dependency manifest %q changed", path)))
				break
			}
		}
	}
	forEachAddedLine(changes, func(path, content string) {
		for _, match := range matchingTerms(content, dependencyTerms) {
			signals = append(signals, signal(routing.SignalDependency, match.surface, match.confidence, path,
				fmt.Sprintf("added line performs package operation %q", match.word)))
		}
	})
	return signals
}
