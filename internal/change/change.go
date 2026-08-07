package change

// ChangeStatus describes how a staged file differs from HEAD.
type ChangeStatus string

const (
	StatusModified ChangeStatus = "modified"
	StatusAdded    ChangeStatus = "added"
	StatusDeleted  ChangeStatus = "deleted"
	StatusRenamed  ChangeStatus = "renamed"
	StatusCopied   ChangeStatus = "copied"
)

// ChangeSet is the parsed representation of a unified Git diff.
type ChangeSet struct {
	Files []FileChange `json:"files"`
}

func (c ChangeSet) Clone() ChangeSet {
	cloned := ChangeSet{Files: make([]FileChange, len(c.Files))}
	for fileIndex, file := range c.Files {
		cloned.Files[fileIndex] = file
		cloned.Files[fileIndex].Hunks = make([]Hunk, len(file.Hunks))
		for hunkIndex, hunk := range file.Hunks {
			cloned.Files[fileIndex].Hunks[hunkIndex] = hunk
			cloned.Files[fileIndex].Hunks[hunkIndex].Lines = append([]Line(nil), hunk.Lines...)
		}
	}
	return cloned
}

// FileChange contains the hunks for one changed file.
type FileChange struct {
	OldPath string       `json:"oldPath,omitempty"`
	NewPath string       `json:"newPath,omitempty"`
	Status  ChangeStatus `json:"status"`
	Binary  bool         `json:"binary,omitempty"`
	Hunks   []Hunk       `json:"hunks"`
}

// Path returns the path that should be used for current-worktree findings.
func (f FileChange) Path() string {
	if f.NewPath != "" {
		return f.NewPath
	}
	return f.OldPath
}

// Hunk represents one @@ range in a unified diff.
type Hunk struct {
	OldStart int    `json:"oldStart"`
	OldLines int    `json:"oldLines"`
	NewStart int    `json:"newStart"`
	NewLines int    `json:"newLines"`
	Section  string `json:"section,omitempty"`
	Lines    []Line `json:"lines"`
}

// LineKind identifies a context, added, or deleted diff line.
type LineKind string

const (
	LineContext LineKind = "context"
	LineAdded   LineKind = "added"
	LineDeleted LineKind = "deleted"
)

// Line stores source text without the unified-diff prefix. A zero line number
// means that the line does not exist on that side of the change.
type Line struct {
	Kind    LineKind `json:"kind"`
	Content string   `json:"content"`
	OldLine int      `json:"oldLine,omitempty"`
	NewLine int      `json:"newLine,omitempty"`
}
