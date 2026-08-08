package detectors

import (
	"fmt"

	"code-review/internal/ai/routing"
	"code-review/internal/change"
)

// FilesystemDetector signals file, path, and archive handling: reads and
// writes, traversal-sensitive operations, symlinks, permissions, and archive
// extraction.
type FilesystemDetector struct{}

var filesystemTerms = []term{
	{word: "path", surface: routing.SurfaceFilesystem, confidence: routing.ConfidenceLow},
	{word: "readdir", surface: routing.SurfaceFilesystem, confidence: routing.ConfidenceHigh},
	{word: "unlink", surface: routing.SurfaceFilesystem, confidence: routing.ConfidenceHigh},
	{word: "removefile", surface: routing.SurfaceFilesystem, confidence: routing.ConfidenceHigh},
	{word: "chmod", surface: routing.SurfaceFilesystem, confidence: routing.ConfidenceHigh},
	{word: "chown", surface: routing.SurfaceFilesystem, confidence: routing.ConfidenceHigh},
	{word: "symlink", surface: routing.SurfaceFilesystem, confidence: routing.ConfidenceHigh},
	{word: "readlink", surface: routing.SurfaceFilesystem, confidence: routing.ConfidenceHigh},
	{word: "realpath", surface: routing.SurfaceFilesystem, confidence: routing.ConfidenceMedium},
	{word: "tmpdir", surface: routing.SurfaceFilesystem, confidence: routing.ConfidenceMedium},
	{word: "tempfile", surface: routing.SurfaceFilesystem, confidence: routing.ConfidenceMedium},
	{word: "zip", surface: routing.SurfaceFilesystem, confidence: routing.ConfidenceMedium},
	{word: "unzip", surface: routing.SurfaceFilesystem, confidence: routing.ConfidenceHigh},
	{word: "archive", surface: routing.SurfaceFilesystem, confidence: routing.ConfidenceMedium},
	{word: "mkdir", surface: routing.SurfaceFilesystem, confidence: routing.ConfidenceLow},
	{word: "fs.", surface: routing.SurfaceFilesystem, confidence: routing.ConfidenceMedium},
	{word: "download", surface: routing.SurfaceFilesystem, confidence: routing.ConfidenceMedium},
	{word: "os.open", surface: routing.SurfaceFilesystem, confidence: routing.ConfidenceHigh},
	{word: "readfile", surface: routing.SurfaceFilesystem, confidence: routing.ConfidenceHigh},
	{word: "writefile", surface: routing.SurfaceFilesystem, confidence: routing.ConfidenceHigh},
	{word: "os.remove", surface: routing.SurfaceFilesystem, confidence: routing.ConfidenceHigh},
	{word: "filepath", surface: routing.SurfaceFilesystem, confidence: routing.ConfidenceMedium},
	{word: "multipart", surface: routing.SurfaceFilesystem, confidence: routing.ConfidenceMedium},
	{word: "tar", surface: routing.SurfaceFilesystem, confidence: routing.ConfidenceMedium},
	{word: "gzip", surface: routing.SurfaceFilesystem, confidence: routing.ConfidenceMedium},
	{word: "shutil", surface: routing.SurfaceFilesystem, confidence: routing.ConfidenceMedium},
}

func (FilesystemDetector) Detect(changes change.ChangeSet) []routing.Signal {
	var signals []routing.Signal
	forEachAddedLine(changes, func(path, content string) {
		for _, match := range matchingTerms(content, filesystemTerms) {
			signals = append(signals, signal(routing.SignalFilesystem, match.surface, match.confidence, path,
				fmt.Sprintf("added line contains filesystem keyword %q (filesystem surface)", match.word)))
		}
	})
	return signals
}
