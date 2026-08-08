package pathfilter

import (
	"fmt"
	"path"
	"strings"
)

// DefaultPatterns returns generated and dependency exclusions used for local
// review. Environment files intentionally remain reviewable by local rules.
func DefaultPatterns() []string {
	return []string{
		"node_modules/",
		"vendor/",
		"dist/",
		"build/",
		"bin/",
		"obj/",
		"coverage/",
		"generated/",
		".git/",
	}
}

// Matcher applies repository-relative path exclusion patterns. A pattern
// ending in a slash matches a directory component; other patterns match a
// path or file name using path.Match semantics.
type Matcher struct {
	patterns []string
}

func New(patterns []string) Matcher {
	return Matcher{patterns: append([]string(nil), patterns...)}
}

// ValidatePattern checks the syntax accepted by Matcher. Callers that accept
// user-provided patterns should reject invalid values rather than fail open.
func ValidatePattern(value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("exclusion pattern cannot be empty")
	}
	pattern := normalizePattern(value)
	if pattern == "" {
		return fmt.Errorf("exclusion pattern cannot be empty")
	}
	pattern = strings.TrimSuffix(pattern, "/")
	if _, err := path.Match(pattern, ""); err != nil {
		return fmt.Errorf("invalid exclusion pattern %q: %w", value, err)
	}
	return nil
}

func (m Matcher) Excludes(filePath string) bool {
	cleanPath := normalize(filePath)
	if cleanPath == "" {
		return false
	}

	for _, rawPattern := range m.patterns {
		pattern := normalizePattern(rawPattern)
		if pattern == "" {
			continue
		}
		if strings.HasSuffix(pattern, "/") {
			if matchesDirectory(cleanPath, strings.TrimSuffix(pattern, "/")) {
				return true
			}
			continue
		}

		matched, err := path.Match(pattern, cleanPath)
		if err == nil && matched {
			return true
		}
		if !strings.Contains(pattern, "/") {
			matched, err = path.Match(pattern, path.Base(cleanPath))
			if err == nil && matched {
				return true
			}
		}
	}
	return false
}

func matchesDirectory(filePath, directory string) bool {
	if strings.Contains(directory, "/") {
		return filePath == directory || strings.HasPrefix(filePath, directory+"/")
	}
	parts := strings.Split(filePath, "/")
	for _, part := range parts[:len(parts)-1] {
		if part == directory {
			return true
		}
	}
	return false
}

func normalize(value string) string {
	value = strings.ReplaceAll(value, "\\", "/")
	value = strings.TrimPrefix(value, "./")
	value = strings.TrimPrefix(path.Clean(value), "/")
	if value == "." {
		return ""
	}
	return value
}

func normalizePattern(value string) string {
	hasTrailingSlash := strings.HasSuffix(value, "/") || strings.HasSuffix(value, "\\")
	value = normalize(value)
	if hasTrailingSlash && value != "" {
		value += "/"
	}
	return value
}
