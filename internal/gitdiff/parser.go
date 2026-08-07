package gitdiff

import (
	"bufio"
	"bytes"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

const maxDiffLineBytes = 16 << 20

var hunkHeaderPattern = regexp.MustCompile(`^@@ -([0-9]+)(,([0-9]+))? \+([0-9]+)(,([0-9]+))? @@ ?(.*)$`)

// Parse converts the unified patch emitted by Git into line-addressable
// changes. It rejects malformed hunk ranges rather than returning partial data.
func Parse(patch []byte) (ChangeSet, error) {
	parser := diffParser{changes: ChangeSet{Files: make([]FileChange, 0)}}
	scanner := bufio.NewScanner(bytes.NewReader(patch))
	scanner.Buffer(make([]byte, 64*1024), maxDiffLineBytes)
	scanner.Split(splitDiffLines)

	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		if err := parser.consume(scanner.Text()); err != nil {
			return ChangeSet{}, fmt.Errorf("parse diff line %d: %w", lineNumber, err)
		}
	}
	if err := scanner.Err(); err != nil {
		return ChangeSet{}, fmt.Errorf("read diff: %w", err)
	}
	if err := parser.finishFile(); err != nil {
		return ChangeSet{}, fmt.Errorf("finish diff: %w", err)
	}
	return parser.changes, nil
}

// splitDiffLines removes the patch's LF delimiter without Scanner's default
// behavior of also discarding a source line's trailing carriage return.
func splitDiffLines(data []byte, atEOF bool) (advance int, token []byte, err error) {
	if index := bytes.IndexByte(data, '\n'); index >= 0 {
		return index + 1, data[:index], nil
	}
	if atEOF && len(data) > 0 {
		return len(data), data, nil
	}
	return 0, nil, nil
}

type diffParser struct {
	changes ChangeSet
	file    *FileChange
	hunk    *Hunk
	oldLine int
	newLine int
	seenOld int
	seenNew int
}

func (p *diffParser) consume(line string) error {
	if strings.HasPrefix(line, "diff --git ") {
		if err := p.finishFile(); err != nil {
			return err
		}
		oldPath, newPath, err := parseDiffHeaderPaths(strings.TrimPrefix(line, "diff --git "))
		if err != nil {
			return err
		}
		p.file = &FileChange{
			OldPath: oldPath,
			NewPath: newPath,
			Status:  StatusModified,
			Hunks:   make([]Hunk, 0),
		}
		return nil
	}

	if p.file == nil {
		if line == "" {
			return nil
		}
		return fmt.Errorf("unexpected content before file header")
	}
	if strings.HasPrefix(line, "@@") {
		if err := p.finishHunk(); err != nil {
			return err
		}
		return p.startHunk(line)
	}
	if p.hunk != nil {
		return p.consumeHunkLine(line)
	}

	switch {
	case strings.HasPrefix(line, "new file mode "):
		p.file.Status = StatusAdded
	case strings.HasPrefix(line, "deleted file mode "):
		p.file.Status = StatusDeleted
	case strings.HasPrefix(line, "rename from "):
		path, err := decodeGitPath(strings.TrimPrefix(line, "rename from "))
		if err != nil {
			return fmt.Errorf("decode rename source: %w", err)
		}
		p.file.OldPath = path
		p.file.Status = StatusRenamed
	case strings.HasPrefix(line, "rename to "):
		path, err := decodeGitPath(strings.TrimPrefix(line, "rename to "))
		if err != nil {
			return fmt.Errorf("decode rename destination: %w", err)
		}
		p.file.NewPath = path
		p.file.Status = StatusRenamed
	case strings.HasPrefix(line, "copy from "):
		path, err := decodeGitPath(strings.TrimPrefix(line, "copy from "))
		if err != nil {
			return fmt.Errorf("decode copy source: %w", err)
		}
		p.file.OldPath = path
		p.file.Status = StatusCopied
	case strings.HasPrefix(line, "copy to "):
		path, err := decodeGitPath(strings.TrimPrefix(line, "copy to "))
		if err != nil {
			return fmt.Errorf("decode copy destination: %w", err)
		}
		p.file.NewPath = path
		p.file.Status = StatusCopied
	case strings.HasPrefix(line, "--- "):
		path, err := parsePatchPath(strings.TrimPrefix(line, "--- "), "a/")
		if err != nil {
			return fmt.Errorf("decode old path: %w", err)
		}
		p.file.OldPath = path
		if path == "" {
			p.file.Status = StatusAdded
		}
	case strings.HasPrefix(line, "+++ "):
		path, err := parsePatchPath(strings.TrimPrefix(line, "+++ "), "b/")
		if err != nil {
			return fmt.Errorf("decode new path: %w", err)
		}
		p.file.NewPath = path
		if path == "" {
			p.file.Status = StatusDeleted
		}
	case strings.HasPrefix(line, "Binary files "), line == "GIT binary patch":
		p.file.Binary = true
	}
	return nil
}

func (p *diffParser) startHunk(header string) error {
	matches := hunkHeaderPattern.FindStringSubmatch(header)
	if matches == nil {
		return fmt.Errorf("invalid hunk header %q", header)
	}

	oldStart, err := strconv.Atoi(matches[1])
	if err != nil {
		return fmt.Errorf("parse old hunk start: %w", err)
	}
	oldLines, err := parseHunkCount(matches[3])
	if err != nil {
		return fmt.Errorf("parse old hunk count: %w", err)
	}
	newStart, err := strconv.Atoi(matches[4])
	if err != nil {
		return fmt.Errorf("parse new hunk start: %w", err)
	}
	newLines, err := parseHunkCount(matches[6])
	if err != nil {
		return fmt.Errorf("parse new hunk count: %w", err)
	}

	p.hunk = &Hunk{
		OldStart: oldStart,
		OldLines: oldLines,
		NewStart: newStart,
		NewLines: newLines,
		Section:  matches[7],
		Lines:    make([]Line, 0, oldLines+newLines),
	}
	p.oldLine = oldStart
	p.newLine = newStart
	p.seenOld = 0
	p.seenNew = 0
	return nil
}

func parseHunkCount(value string) (int, error) {
	if value == "" {
		return 1, nil
	}
	return strconv.Atoi(value)
}

func (p *diffParser) consumeHunkLine(line string) error {
	if line == `\ No newline at end of file` {
		return nil
	}
	if line == "" {
		return fmt.Errorf("hunk line has no change marker")
	}

	diffLine := Line{Content: line[1:]}
	switch line[0] {
	case ' ':
		diffLine.Kind = LineContext
		diffLine.OldLine = p.oldLine
		diffLine.NewLine = p.newLine
		p.oldLine++
		p.newLine++
		p.seenOld++
		p.seenNew++
	case '+':
		diffLine.Kind = LineAdded
		diffLine.NewLine = p.newLine
		p.newLine++
		p.seenNew++
	case '-':
		diffLine.Kind = LineDeleted
		diffLine.OldLine = p.oldLine
		p.oldLine++
		p.seenOld++
	default:
		return fmt.Errorf("unexpected hunk line marker %q", line[0])
	}
	if p.seenOld > p.hunk.OldLines || p.seenNew > p.hunk.NewLines {
		return fmt.Errorf("hunk contains more lines than declared")
	}
	p.hunk.Lines = append(p.hunk.Lines, diffLine)
	return nil
}

func (p *diffParser) finishHunk() error {
	if p.hunk == nil {
		return nil
	}
	if p.seenOld != p.hunk.OldLines || p.seenNew != p.hunk.NewLines {
		return fmt.Errorf(
			"hunk line count mismatch: expected -%d/+%d, got -%d/+%d",
			p.hunk.OldLines,
			p.hunk.NewLines,
			p.seenOld,
			p.seenNew,
		)
	}
	p.file.Hunks = append(p.file.Hunks, *p.hunk)
	p.hunk = nil
	return nil
}

func (p *diffParser) finishFile() error {
	if p.file == nil {
		return nil
	}
	if err := p.finishHunk(); err != nil {
		return err
	}

	switch p.file.Status {
	case StatusAdded:
		p.file.OldPath = ""
	case StatusDeleted:
		p.file.NewPath = ""
	case StatusModified:
		if p.file.OldPath != "" && p.file.NewPath != "" && p.file.OldPath != p.file.NewPath {
			p.file.Status = StatusRenamed
		}
	}
	if p.file.Path() == "" {
		return fmt.Errorf("file change has no path")
	}
	p.changes.Files = append(p.changes.Files, *p.file)
	p.file = nil
	return nil
}

func parseDiffHeaderPaths(raw string) (string, string, error) {
	if strings.HasPrefix(raw, `"`) {
		oldPath, rest, err := consumeQuotedPath(raw)
		if err != nil {
			return "", "", fmt.Errorf("decode old diff path: %w", err)
		}
		rest = strings.TrimLeft(rest, " \t")
		if rest == "" {
			return "", "", fmt.Errorf("diff header has no new path")
		}

		var newPath string
		if strings.HasPrefix(rest, `"`) {
			newPath, rest, err = consumeQuotedPath(rest)
			if err != nil {
				return "", "", fmt.Errorf("decode new diff path: %w", err)
			}
			if strings.TrimSpace(rest) != "" {
				return "", "", fmt.Errorf("unexpected content after diff paths")
			}
		} else {
			newPath = rest
		}
		return stripPathPrefix(oldPath, "a/"), stripPathPrefix(newPath, "b/"), nil
	}

	if separator := strings.LastIndex(raw, ` "b/`); separator >= 0 {
		newPath, trailing, err := consumeQuotedPath(raw[separator+1:])
		if err != nil {
			return "", "", fmt.Errorf("decode new diff path: %w", err)
		}
		if strings.TrimSpace(trailing) != "" {
			return "", "", fmt.Errorf("unexpected content after diff paths")
		}
		return stripPathPrefix(raw[:separator], "a/"), stripPathPrefix(newPath, "b/"), nil
	}

	type pathPair struct {
		oldPath string
		newPath string
	}
	var fallback *pathPair
	for offset := 0; offset < len(raw); {
		relative := strings.Index(raw[offset:], " b/")
		if relative < 0 {
			break
		}
		boundary := offset + relative
		candidate := pathPair{oldPath: raw[:boundary], newPath: raw[boundary+1:]}
		if fallback == nil {
			value := candidate
			fallback = &value
		}
		if stripPathPrefix(candidate.oldPath, "a/") == stripPathPrefix(candidate.newPath, "b/") {
			return stripPathPrefix(candidate.oldPath, "a/"), stripPathPrefix(candidate.newPath, "b/"), nil
		}
		offset = boundary + 1
	}
	if fallback == nil {
		return "", "", fmt.Errorf("invalid diff file header %q", raw)
	}
	return stripPathPrefix(fallback.oldPath, "a/"), stripPathPrefix(fallback.newPath, "b/"), nil
}

func parsePatchPath(raw, prefix string) (string, error) {
	if raw == "/dev/null" {
		return "", nil
	}
	path, err := decodeGitPath(raw)
	if err != nil {
		return "", err
	}
	return stripPathPrefix(path, prefix), nil
}

func decodeGitPath(raw string) (string, error) {
	if !strings.HasPrefix(raw, `"`) {
		return raw, nil
	}
	path, rest, err := consumeQuotedPath(raw)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(rest) != "" {
		return "", fmt.Errorf("unexpected content after quoted path")
	}
	return path, nil
}

func consumeQuotedPath(raw string) (string, string, error) {
	if !strings.HasPrefix(raw, `"`) {
		return "", "", fmt.Errorf("path is not quoted")
	}
	escaped := false
	for index := 1; index < len(raw); index++ {
		switch {
		case escaped:
			escaped = false
		case raw[index] == '\\':
			escaped = true
		case raw[index] == '"':
			path, err := strconv.Unquote(raw[:index+1])
			if err != nil {
				return "", "", err
			}
			return path, raw[index+1:], nil
		}
	}
	return "", "", fmt.Errorf("unterminated quoted path")
}

func stripPathPrefix(value, prefix string) string {
	return strings.TrimPrefix(value, prefix)
}
