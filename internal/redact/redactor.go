package redact

import (
	"regexp"
	"strings"
)

const Placeholder = "[REDACTED_SECRET]"

const credentialKeyPattern = `(?i)(api[_-]?key|access[_-]?key|secret[_-]?key|client[_-]?secret|access[_-]?token|auth[_-]?token|refresh[_-]?token|private[_-]?key|database[_-]?password|database[_-]?url|connection[_-]?string|authorization|credentials?|password|passwd|pwd|token|secret|dsn)`

var (
	privateKeyBeginPattern  = regexp.MustCompile(`-----BEGIN ([A-Z0-9]+ )*PRIVATE KEY-----`)
	privateKeyEndPattern    = regexp.MustCompile(`-----END ([A-Z0-9]+ )*PRIVATE KEY-----`)
	quotedAssignmentPattern = regexp.MustCompile(
		credentialKeyPattern + "[\"'`]?[ \\t]*(:=|=|:)[ \\t]*([\"'`])([^\"'`\\r\\n]+)([\"'`])",
	)
	unquotedAssignmentPattern = regexp.MustCompile(
		credentialKeyPattern + "[\"'`]?[ \\t]*(:=|=|:)[ \\t]*([^\"'` \\t,;}\\]\\r\\n]+)",
	)
	credentialURLPattern = regexp.MustCompile(`(?i)\b(https?|postgres|postgresql|mysql|mongodb|mongodb\+srv|redis|rediss|amqp|amqps)://([^:/@\s]+):([^@\s/]+)@`)
	bearerTokenPattern   = regexp.MustCompile(`(?i)\bBearer[ \t]+([A-Za-z0-9._~+/=-]{8,})`)
	jwtPattern           = regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\b`)
	knownSecretPatterns  = []*regexp.Regexp{
		regexp.MustCompile(`\b(AKIA|ASIA)[A-Z0-9]{16}\b`),
		regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{20,}\b`),
		regexp.MustCompile(`\bglpat-[A-Za-z0-9_-]{20,}\b`),
		regexp.MustCompile(`\bnpm_[A-Za-z0-9]{20,}\b`),
		regexp.MustCompile(`\bpypi-[A-Za-z0-9_-]{20,}\b`),
		regexp.MustCompile(`\bAIza[0-9A-Za-z_-]{35}\b`),
		regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{10,}\b`),
		regexp.MustCompile(`\b(rk|sk)_live_[A-Za-z0-9]{16,}\b`),
		regexp.MustCompile(`\bxox[baprs]-[A-Za-z0-9-]{16,}\b`),
	}
)

type Result struct {
	Text  string
	Count int
}

// Secrets redacts credential material from arbitrary source code or unified
// diffs. It preserves line count so downstream diagnostics remain stable.
func Secrets(input string) Result {
	if input == "" {
		return Result{Text: ""}
	}

	text, count := redactPrivateKeyBlocks(input)
	text, replacements := redactSubmatch(text, bearerTokenPattern, 1)
	count += replacements
	text, replacements = redactSubmatch(text, quotedAssignmentPattern, 4)
	count += replacements
	text, replacements = redactSubmatch(text, unquotedAssignmentPattern, 3)
	count += replacements
	text, replacements = redactSubmatch(text, credentialURLPattern, 3)
	count += replacements

	for _, pattern := range knownSecretPatterns {
		matches := pattern.FindAllStringIndex(text, -1)
		if len(matches) == 0 {
			continue
		}
		text = pattern.ReplaceAllString(text, Placeholder)
		count += len(matches)
	}

	matches := jwtPattern.FindAllStringIndex(text, -1)
	if len(matches) > 0 {
		text = jwtPattern.ReplaceAllString(text, Placeholder)
		count += len(matches)
	}
	return Result{Text: text, Count: count}
}

func redactSubmatch(input string, pattern *regexp.Regexp, valueGroup int) (string, int) {
	matches := pattern.FindAllStringSubmatchIndex(input, -1)
	if len(matches) == 0 {
		return input, 0
	}

	var output strings.Builder
	output.Grow(len(input))
	last := 0
	count := 0
	for _, match := range matches {
		groupIndex := valueGroup * 2
		if groupIndex+1 >= len(match) || match[groupIndex] < 0 {
			continue
		}
		start, end := match[groupIndex], match[groupIndex+1]
		if !shouldRedact(input[start:end]) {
			continue
		}
		output.WriteString(input[last:start])
		output.WriteString(Placeholder)
		last = end
		count++
	}
	if count == 0 {
		return input, 0
	}
	output.WriteString(input[last:])
	return output.String(), count
}

func shouldRedact(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return false
	}
	if value == "bearer" {
		return false
	}
	for _, prefix := range []string{
		"$",
		"%",
		"{{",
		"<redacted",
		"[redacted",
		"bearer ",
		"config.",
		"configuration",
		"env.",
		"getenv(",
		"os.getenv",
		"process.env",
		"secretstorage",
		"settings.",
		"system.getenv",
	} {
		if strings.HasPrefix(value, prefix) {
			return false
		}
	}
	for _, marker := range []string{
		"change-me",
		"changeme",
		"dummy-value",
		"example",
		"not-a-secret",
		"placeholder",
		"replace-me",
		"your-api-key",
		"your_api_key",
	} {
		if strings.Contains(value, marker) {
			return false
		}
	}
	return true
}

func redactPrivateKeyBlocks(input string) (string, int) {
	var output strings.Builder
	output.Grow(len(input))
	remaining := input
	insideBlock := false
	wrotePlaceholder := false
	count := 0

	for remaining != "" {
		line, ending, rest := nextLine(remaining)
		remaining = rest
		switch {
		case !insideBlock && privateKeyBeginPattern.MatchString(line):
			insideBlock = true
			wrotePlaceholder = false
			count++
			output.WriteString(line)
			output.WriteString(ending)
		case insideBlock && privateKeyEndPattern.MatchString(line):
			insideBlock = false
			output.WriteString(line)
			output.WriteString(ending)
		case insideBlock:
			output.WriteString(structuralPrefix(line))
			if !wrotePlaceholder {
				output.WriteString(Placeholder)
				wrotePlaceholder = true
			}
			output.WriteString(ending)
		default:
			output.WriteString(line)
			output.WriteString(ending)
		}
	}
	return output.String(), count
}

func nextLine(input string) (line, ending, rest string) {
	index := strings.IndexByte(input, '\n')
	if index < 0 {
		return input, "", ""
	}
	line = input[:index]
	ending = "\n"
	if strings.HasSuffix(line, "\r") {
		line = strings.TrimSuffix(line, "\r")
		ending = "\r\n"
	}
	return line, ending, input[index+1:]
}

func structuralPrefix(line string) string {
	index := 0
	if len(line) > 0 && (line[0] == '+' || line[0] == '-') {
		index++
	}
	for index < len(line) && (line[index] == ' ' || line[index] == '\t') {
		index++
	}
	return line[:index]
}
