package secrets

import (
	"context"
	"regexp"
	"strings"

	"code-review/internal/change"
	"code-review/internal/findings"
)

var (
	privateKeyPattern     = regexp.MustCompile(`-----BEGIN (RSA |EC |DSA |OPENSSH |ENCRYPTED )?PRIVATE KEY-----`)
	knownTokenPattern     = regexp.MustCompile(`\b((AKIA|ASIA)[0-9A-Z]{16}|gh[pousr]_[A-Za-z0-9]{20,}|sk-[A-Za-z0-9_-]{20,}|sk_live_[A-Za-z0-9]{16,}|xox[baprs]-[A-Za-z0-9-]{16,})\b`)
	quotedSecretPattern   = regexp.MustCompile("(?i)(api[_-]?key|client[_-]?secret|access[_-]?token|auth[_-]?token|password|passwd|secret)[\"'`]?\\s*(:=|=|:)\\s*[\"'`]([^\"'`]+)[\"'`]")
	unquotedSecretPattern = regexp.MustCompile("(?i)(api[_-]?key|client[_-]?secret|access[_-]?token|auth[_-]?token|password|passwd|secret)[\"'`]?\\s*(:=|=|:)\\s*([^\"'` \\t,;}\\]\\r\\n]+)")
)

type secretMatch struct {
	severity   findings.Severity
	title      string
	message    string
	confidence float64
}

// SecretAnalyzer conservatively checks added lines for credential material. It
// reports locations only and never copies a matched value into a finding.
type Analyzer struct{}

func (Analyzer) Name() string {
	return "secrets"
}

func (Analyzer) Analyze(ctx context.Context, changes change.ChangeSet) ([]findings.Finding, error) {
	result := make([]findings.Finding, 0)
	for _, file := range changes.Files {
		if file.Binary {
			continue
		}
		for _, hunk := range file.Hunks {
			for _, line := range hunk.Lines {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				if line.Kind != change.LineAdded {
					continue
				}
				match, detected := detectSecret(line.Content)
				if !detected {
					continue
				}
				result = append(result, findings.Finding{
					File:       file.Path(),
					StartLine:  line.NewLine,
					EndLine:    line.NewLine,
					Severity:   match.severity,
					Category:   findings.CategorySecurity,
					Title:      match.title,
					Message:    match.message,
					Suggestion: "Remove the credential from source control, rotate it if it may be active, and load it from secure secret storage or the environment.",
					Confidence: match.confidence,
					Source:     findings.SourceLocalRule,
				})
			}
		}
	}
	return result, nil
}

func detectSecret(line string) (secretMatch, bool) {
	if privateKeyPattern.MatchString(line) {
		return secretMatch{
			severity:   findings.SeverityCritical,
			title:      "Private key added to source control",
			message:    "An added line contains a private key header.",
			confidence: 0.99,
		}, true
	}
	if token := knownTokenPattern.FindString(line); token != "" && !isPlaceholder(token) {
		return secretMatch{
			severity:   findings.SeverityHigh,
			title:      "Potential provider credential",
			message:    "An added line contains a value matching a known credential format.",
			confidence: 0.98,
		}, true
	}
	matches := quotedSecretPattern.FindStringSubmatch(line)
	if len(matches) != 4 {
		matches = unquotedSecretPattern.FindStringSubmatch(line)
	}
	if len(matches) == 4 && isLiteralSecret(matches[3]) {
		return secretMatch{
			severity:   findings.SeverityMedium,
			title:      "Potential hardcoded secret",
			message:    "An added credential-like assignment appears to contain a literal value.",
			confidence: 0.80,
		}, true
	}
	return secretMatch{}, false
}

func isLiteralSecret(value string) bool {
	value = strings.TrimSpace(value)
	if len(value) < 4 || isPlaceholder(value) {
		return false
	}
	switch strings.ToLower(value) {
	case "false", "none", "null", "test", "true", "undefined":
		return false
	default:
		return true
	}
}

func isPlaceholder(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	if strings.HasPrefix(value, "<") && strings.HasSuffix(value, ">") {
		return true
	}
	for _, marker := range []string{
		"[redacted]",
		"changeme",
		"change-me",
		"dummy",
		"example",
		"not-a-secret",
		"placeholder",
		"replace_",
		"replace-",
		"replace with",
		"your_",
		"your-",
	} {
		if strings.Contains(value, marker) {
			return true
		}
	}
	for _, prefix := range []string{
		"$",
		"%",
		"{{",
		"config.",
		"configuration",
		"env.",
		"getenv(",
		"os.getenv",
		"process.env",
		"settings.",
		"system.getenv",
	} {
		if strings.HasPrefix(value, prefix) {
			return true
		}
	}
	return false
}
