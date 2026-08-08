package detectors

import (
	"testing"

	"code-review/internal/ai/routing"
	"code-review/internal/change"
)

func TestKeywordDetectorFlagsCredentialLinesAndPaths(t *testing.T) {
	t.Parallel()

	changes := change.ChangeSet{Files: []change.FileChange{
		{
			NewPath: "service.go",
			Status:  change.StatusAdded,
			Hunks: []change.Hunk{{Lines: []change.Line{
				{Kind: change.LineAdded, NewLine: 1, Content: "package service"},
				{Kind: change.LineAdded, NewLine: 2, Content: `password := request.FormValue("password")`},
			}}},
		},
	}}
	signals := (KeywordDetector{}).Detect(changes)
	if len(signals) == 0 {
		t.Fatal("KeywordDetector() = no signals for a password line")
	}
	var found *routing.Signal
	for index := range signals {
		if signals[index].File == "service.go" && signals[index].Surface == routing.SurfaceCredentials {
			found = &signals[index]
		}
	}
	if found == nil {
		t.Fatalf("KeywordDetector() = %#v, want a credentials signal", signals)
	}
	pathSignals := (KeywordDetector{}).Detect(change.ChangeSet{Files: []change.FileChange{{
		NewPath: "src/secret_helper.go",
		Status:  change.StatusAdded,
	}}})
	if len(pathSignals) == 0 || pathSignals[0].Kind != routing.SignalKeyword {
		t.Fatalf("KeywordDetector() = %#v, want a path keyword signal", pathSignals)
	}
}

func TestKeywordDetectorIgnoresDeletedLines(t *testing.T) {
	t.Parallel()

	changes := change.ChangeSet{Files: []change.FileChange{{
		NewPath: "service.go",
		Status:  change.StatusModified,
		Hunks: []change.Hunk{{Lines: []change.Line{
			{Kind: change.LineDeleted, OldLine: 1, Content: "password = unsafe"},
			{Kind: change.LineAdded, NewLine: 1, Content: "value = safe"},
		}}},
	}}}
	if signals := (KeywordDetector{}).Detect(changes); len(signals) != 0 {
		t.Fatalf("KeywordDetector() = %#v, deleted lines must not signal", signals)
	}
}

func TestKeywordDetectorRulesOutOrdinaryCode(t *testing.T) {
	t.Parallel()

	changes := change.ChangeSet{Files: []change.FileChange{{
		NewPath: "main.go",
		Status:  change.StatusAdded,
		Hunks: []change.Hunk{{Lines: []change.Line{
			{Kind: change.LineAdded, NewLine: 1, Content: "func main() {"},
			{Kind: change.LineAdded, NewLine: 2, Content: "sum := 1 + 2"},
		}}},
	}}}
	if signals := (KeywordDetector{}).Detect(changes); len(signals) != 0 {
		t.Fatalf("KeywordDetector() = %#v, ordinary code produced signals", signals)
	}
}

func TestPathDetectorFlagsSensitiveMaterial(t *testing.T) {
	t.Parallel()

	changes := change.ChangeSet{Files: []change.FileChange{
		{NewPath: ".env.production", Status: change.StatusAdded},
		{NewPath: "certs/server.pem", Status: change.StatusAdded},
		{NewPath: "deploy/dockerfile", Status: change.StatusAdded},
		{NewPath: "configs/global.config.yaml", Status: change.StatusAdded},
		{NewPath: "web/app.ts", Status: change.StatusAdded},
	}}
	signals := (PathDetector{}).Detect(changes)
	if len(signals) != 4 {
		t.Fatalf("PathDetector() = %d signals, want 4: %#v", len(signals), signals)
	}
	var credentials, configuration int
	for _, signal := range signals {
		if signal.Kind != routing.SignalPath || signal.File == "web/app.ts" {
			t.Fatalf("PathDetector() = %#v", signal)
		}
		switch signal.Surface {
		case routing.SurfaceCredentials:
			credentials++
		case routing.SurfaceDataExposure:
			configuration++
		}
	}
	if credentials != 2 || configuration != 2 {
		t.Fatalf("PathDetector() surfaces = credentials %d, data-exposure %d", credentials, configuration)
	}
}

func TestAuthDetectorFlagsGuardsAndAuthKeywords(t *testing.T) {
	t.Parallel()

	changes := detectLine(`if !isAdmin(user) || role == "guest" { deny() }`, "handler.go")
	signals := (AuthDetector{}).Detect(changes)
	if len(signals) == 0 {
		t.Fatal("AuthDetector() = no signals for a privilege check")
	}
	authorization := false
	for _, signal := range signals {
		if signal.Kind != routing.SignalAuth || signal.File != "handler.go" {
			t.Fatalf("AuthDetector() = %#v", signal)
		}
		authorization = authorization || signal.Surface == routing.SurfaceAuthorization
	}
	if !authorization {
		t.Fatalf("AuthDetector() = %#v, want an authorization surface", signals)
	}
}

func TestNetworkDetectorFlagsOutboundCalls(t *testing.T) {
	t.Parallel()

	changes := detectLine(`resp, err := httpClient.Do(req) // fetch(`, "worker/main.ts")
	signals := (NetworkDetector{}).Detect(changes)
	if len(signals) == 0 {
		t.Fatal("NetworkDetector() = no signals for an HTTP call")
	}
	for _, signal := range signals {
		if signal.Kind != routing.SignalNetwork || signal.File != "worker/main.ts" {
			t.Fatalf("NetworkDetector() = %#v", signal)
		}
	}
}

func TestDatabaseDetectorFlagsQueryConstruction(t *testing.T) {
	t.Parallel()

	changes := detectLine(`rows := db.Query("SELECT * FROM users WHERE id = " + userInput)`, "store.go")
	signals := (DatabaseDetector{}).Detect(changes)
	if len(signals) == 0 {
		t.Fatal("DatabaseDetector() = no signals for a query")
	}
	for _, signal := range signals {
		if signal.Kind != routing.SignalDatabase || signal.Surface != routing.SurfaceInjection {
			t.Fatalf("DatabaseDetector() = %#v", signal)
		}
	}
}

func TestFilesystemDetectorFlagsPathHandling(t *testing.T) {
	t.Parallel()

	changes := detectLine(`unlink(tempfilePath)`, "cleanup.go")
	signals := (FilesystemDetector{}).Detect(changes)
	if len(signals) == 0 {
		t.Fatal("FilesystemDetector() = no signals for a file delete")
	}
	for _, signal := range signals {
		if signal.Kind != routing.SignalFilesystem || signal.Surface != routing.SurfaceFilesystem {
			t.Fatalf("FilesystemDetector() = %#v", signal)
		}
	}
}

func TestSerializationDetectorFlagsUntrustedDeserialization(t *testing.T) {
	t.Parallel()

	changes := detectLine(`data = pickle.loads(payload)`, "worker.py")
	signals := (SerializationDetector{}).Detect(changes)
	if len(signals) == 0 {
		t.Fatal("SerializationDetector() = no signals for pickle.loads")
	}
	for _, signal := range signals {
		if signal.Kind != routing.SignalSerialization || signal.Surface != routing.SurfaceSerialization {
			t.Fatalf("SerializationDetector() = %#v", signal)
		}
	}
}

func TestDependencyDetectorFlagsManifestsAndInstalls(t *testing.T) {
	t.Parallel()

	changes := change.ChangeSet{Files: []change.FileChange{
		{NewPath: "web/package.json", Status: change.StatusModified},
		{
			NewPath: "scripts/bootstrap.sh",
			Status:  change.StatusModified,
			Hunks: []change.Hunk{{Lines: []change.Line{
				{Kind: change.LineAdded, NewLine: 1, Content: "npm install left-pad"},
			}}},
		},
	}}
	signals := (DependencyDetector{}).Detect(changes)
	if len(signals) != 2 {
		t.Fatalf("DependencyDetector() = %d signals, want 2: %#v", len(signals), signals)
	}
	for _, signal := range signals {
		if signal.Kind != routing.SignalDependency || signal.Surface != routing.SurfaceSupplyChain {
			t.Fatalf("DependencyDetector() = %#v", signal)
		}
	}
}

func TestEndpointDetectorFlagsRouteRegistration(t *testing.T) {
	t.Parallel()

	changes := detectLine(`@app.post("/transfer") def transfer():`, "api.py")
	signals := (EndpointDetector{}).Detect(changes)
	if len(signals) == 0 {
		t.Fatal("EndpointDetector() = no signals for a route registration")
	}
	for _, signal := range signals {
		if signal.Kind != routing.SignalEndpoint {
			t.Fatalf("EndpointDetector() = %#v", signal)
		}
	}
}

func detectLine(content, path string) change.ChangeSet {
	return detectLines([]string{content}, path)
}

func detectLines(contents []string, path string) change.ChangeSet {
	lines := make([]change.Line, 0, len(contents))
	for index, content := range contents {
		lines = append(lines, change.Line{Kind: change.LineAdded, NewLine: index + 1, Content: content})
	}
	return change.ChangeSet{Files: []change.FileChange{{
		NewPath: path,
		Status:  change.StatusModified,
		Hunks:   []change.Hunk{{Lines: lines}},
	}}}
}
