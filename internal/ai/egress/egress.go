// Package egress gates what may leave the local machine toward an AI
// provider. The egress policy is the first gate in the provider pipeline:
// Context Resolver -> Egress Policy -> Secret Redactor -> Token Budget ->
// Provider. It classifies every repository-relative file path that could be
// sent, so local rules and analyzers still see the file while the AI provider
// never does (deny), or sees it only after secret redaction (redact).
package egress

import (
	"fmt"
	"regexp"
	"strings"

	"code-review/internal/change"
)

// EgressAction is the policy verdict for one file path.
type EgressAction string

const (
	// EgressAllow permits the path to reach the pipeline; secrets are still
	// redacted by the redactor before any provider sees them.
	EgressAllow EgressAction = "allow"
	// EgressRedact permits the path but marks it for secret redaction.
	EgressRedact EgressAction = "redact"
	// EgressDeny drops the path from everything a provider can observe.
	EgressDeny EgressAction = "deny"
)

// EgressRule maps one glob pattern to an action. Patterns are
// repository-relative: "**" crosses directories, "*" and "?" do not.
type EgressRule struct {
	Pattern string
	Action  EgressAction
}

// Policy is an immutable first-match rule set. An empty policy allows
// everything; the last matching rule wins.
type Policy struct {
	rules []compiledRule
}

type compiledRule struct {
	action     EgressAction
	pattern    string
	expression *regexp.Regexp
}

// New compiles a rule set. Invalid patterns or actions are rejected so a
// policy never fails open.
func New(rules []EgressRule) (Policy, error) {
	compiled := make([]compiledRule, 0, len(rules))
	for _, rule := range rules {
		action := EgressAction(strings.TrimSpace(string(rule.Action)))
		switch action {
		case EgressAllow, EgressRedact, EgressDeny:
		default:
			return Policy{}, fmt.Errorf("unsupported egress action %q", rule.Action)
		}
		expression, err := compilePattern(rule.Pattern)
		if err != nil {
			return Policy{}, fmt.Errorf("invalid egress pattern %q: %w", rule.Pattern, err)
		}
		compiled = append(compiled, compiledRule{action: action, pattern: rule.Pattern, expression: expression})
	}
	return Policy{rules: compiled}, nil
}

// DefaultRules is the seed policy for AI egress: secrets and local
// configuration must never reach a provider, and the redactor's noise budget
// is not spent on config files that were never secret.
func DefaultRules() []EgressRule {
	return []EgressRule{
		{Pattern: ".env", Action: EgressDeny},
		{Pattern: ".env.*", Action: EgressDeny},
		{Pattern: "*.pem", Action: EgressDeny},
		{Pattern: "*.key", Action: EgressDeny},
		{Pattern: "secrets/**", Action: EgressDeny},
		{Pattern: "*.config.json", Action: EgressRedact},
		{Pattern: "src/**", Action: EgressAllow},
		{Pattern: "tests/**", Action: EgressAllow},
	}
}

// Classify returns the action for a repository-relative path. An empty policy
// or an unmatched path yields EgressAllow. Patterns without a slash also match
// against the file name, so "*.pem" denies "certs/server.pem".
func (p Policy) Classify(path string) EgressAction {
	clean := normalize(path)
	if clean == "" {
		return EgressAllow
	}
	for _, rule := range p.rules {
		if matches(rule, clean) {
			return rule.action
		}
	}
	return EgressAllow
}

func matches(rule compiledRule, clean string) bool {
	if rule.expression.MatchString(clean) {
		return true
	}
	if strings.Contains(rule.pattern, "/") {
		return false
	}
	base := clean
	if index := strings.LastIndex(base, "/"); index >= 0 {
		base = base[index+1:]
	}
	return rule.expression.MatchString(base)
}

// Allow reports whether a path may enter the provider pipeline at all.
func (p Policy) Allow(path string) bool {
	return p.Classify(path) != EgressDeny
}

// Redact reports whether a path must pass through secret redaction before a
// provider can observe it. Denied paths never reach redaction.
func (p Policy) Redact(path string) bool {
	return p.Classify(path) == EgressRedact
}

// FilterChanges drops every file the policy denies from a change set, so
// denied content can never reach the preparation or provider layers.
func (p Policy) FilterChanges(changes change.ChangeSet) change.ChangeSet {
	filtered := changes
	filtered.Files = make([]change.FileChange, 0, len(changes.Files))
	for _, file := range changes.Files {
		if p.Allow(file.Path()) {
			filtered.Files = append(filtered.Files, file)
		}
	}
	return filtered
}

func normalize(value string) string {
	value = strings.ReplaceAll(value, "\\", "/")
	value = strings.TrimPrefix(value, "./")
	value = strings.TrimPrefix(value, "/")
	return value
}

// compilePattern turns a glob into an anchored regular expression. "**" is
// the only pattern that crosses directory boundaries; "?" matches one
// character that is not a slash.
func compilePattern(pattern string) (*regexp.Regexp, error) {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return nil, fmt.Errorf("pattern cannot be empty")
	}
	if err := validateBrackets(pattern); err != nil {
		return nil, err
	}
	var builder strings.Builder
	builder.WriteString("^")
	for index := 0; index < len(pattern); index++ {
		character := pattern[index]
		switch character {
		case '*':
			if index+1 < len(pattern) && pattern[index+1] == '*' {
				builder.WriteString(".*")
				index++
				continue
			}
			builder.WriteString("[^/]*")
		case '?':
			builder.WriteString("[^/]")
		case '[':
			closeIndex := strings.IndexByte(pattern[index:], ']')
			if closeIndex < 0 {
				return nil, fmt.Errorf("unbalanced '[' in pattern")
			}
			body := pattern[index+1 : index+closeIndex]
			builder.WriteString("[")
			for bodyIndex := 0; bodyIndex < len(body); bodyIndex++ {
				character := body[bodyIndex]
				if character == '^' {
					builder.WriteString("^")
					continue
				}
				if character == ']' || character == '\\' || character == '[' {
					builder.WriteString(regexp.QuoteMeta(string(character)))
					continue
				}
				builder.WriteByte(character)
			}
			builder.WriteString("]")
			index += closeIndex
		default:
			builder.WriteString(regexp.QuoteMeta(string(character)))
		}
	}
	builder.WriteString("$")
	return regexp.Compile(builder.String())
}

func validateBrackets(pattern string) error {
	depth := 0
	for index := 0; index < len(pattern); index++ {
		switch pattern[index] {
		case '[':
			depth++
		case ']':
			if depth == 0 {
				return fmt.Errorf("unbalanced ']' in pattern")
			}
			depth--
		}
	}
	if depth != 0 {
		return fmt.Errorf("unbalanced '[' in pattern")
	}
	return nil
}
