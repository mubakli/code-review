package findings

import (
	"crypto/sha256"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

type Severity string

const (
	SeverityCritical Severity = "critical"
	SeverityHigh     Severity = "high"
	SeverityMedium   Severity = "medium"
	SeverityLow      Severity = "low"
	SeverityInfo     Severity = "info"
)

type Category string

const (
	CategorySecurity        Category = "security"
	CategoryCorrectness     Category = "correctness"
	CategoryPerformance     Category = "performance"
	CategoryDatabase        Category = "database"
	CategoryMaintainability Category = "maintainability"
	CategoryQuality         Category = "quality"
)

const (
	SourceLocalRule      = "local-rule"
	SourceStaticAnalysis = "static-analysis"
	SourceSQLAnalyzer    = "sql-analyzer"
	SourceAI             = "ai"
)

const (
	MaxRuleIDBytes         = 128
	MaxFixDescriptionBytes = 1000
	MaxFixReplacementBytes = 64 << 10
)

var (
	ruleIDPattern    = regexp.MustCompile(`^[a-z][a-z0-9-]*(/[a-z][a-z0-9-]*)+$`)
	findingIDPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
)

// ProposedFix replaces complete lines in the finding range. Replacement may
// be empty to delete those lines.
type ProposedFix struct {
	Description string `json:"description"`
	StartLine   int    `json:"startLine"`
	EndLine     int    `json:"endLine"`
	Replacement string `json:"replacement"`
}

// Finding is the common output contract for local and AI-assisted analyzers.
type Finding struct {
	RuleID      string       `json:"ruleId"`
	FindingID   string       `json:"findingId"`
	File        string       `json:"file"`
	StartLine   int          `json:"startLine"`
	EndLine     int          `json:"endLine"`
	Severity    Severity     `json:"severity"`
	Category    Category     `json:"category"`
	Title       string       `json:"title"`
	Message     string       `json:"message"`
	Suggestion  string       `json:"suggestion,omitempty"`
	ProposedFix *ProposedFix `json:"proposedFix,omitempty"`
	Confidence  float64      `json:"confidence"`
	Source      string       `json:"source"`
	AgentID     string       `json:"agentId,omitempty"`
}

// FinalizeID replaces FindingID with the deterministic identity derived only
// from stable, trusted finding attributes.
func (f *Finding) FinalizeID() {
	hash := sha256.New()
	for _, value := range []string{
		f.Source,
		f.RuleID,
		f.AgentID,
		string(f.Category),
		f.File,
		strconv.Itoa(f.StartLine),
		strconv.Itoa(f.EndLine),
		normalizedTitle(f.Title),
	} {
		fmt.Fprintf(hash, "%d:%s", len(value), value)
	}
	f.FindingID = fmt.Sprintf("sha256:%x", hash.Sum(nil))
}

// Clone returns a finding with independent nested fix storage.
func (f Finding) Clone() Finding {
	if f.ProposedFix != nil {
		fix := *f.ProposedFix
		f.ProposedFix = &fix
	}
	return f
}

func Clone(values []Finding) []Finding {
	cloned := make([]Finding, len(values))
	for index, finding := range values {
		cloned[index] = finding.Clone()
	}
	return cloned
}

// Validate rejects malformed analyzer output before it reaches CLI or editor
// consumers. The same validation can later be applied to untrusted AI output.
func (f Finding) Validate() error {
	if len(f.RuleID) == 0 || len(f.RuleID) > MaxRuleIDBytes || !ruleIDPattern.MatchString(f.RuleID) {
		return fmt.Errorf("rule ID must be a safe lowercase namespace of at most %d bytes", MaxRuleIDBytes)
	}
	if !findingIDPattern.MatchString(f.FindingID) {
		return fmt.Errorf("finding ID must have sha256:<64 lowercase hex> form")
	}
	if strings.TrimSpace(f.File) == "" {
		return fmt.Errorf("file is required")
	}
	if f.StartLine < 1 {
		return fmt.Errorf("start line must be positive")
	}
	if f.EndLine < f.StartLine {
		return fmt.Errorf("end line must be at or after start line")
	}
	if !validSeverity(f.Severity) {
		return fmt.Errorf("unknown severity %q", f.Severity)
	}
	if !validCategory(f.Category) {
		return fmt.Errorf("unknown category %q", f.Category)
	}
	if strings.TrimSpace(f.Title) == "" {
		return fmt.Errorf("title is required")
	}
	if strings.TrimSpace(f.Message) == "" {
		return fmt.Errorf("message is required")
	}
	if math.IsNaN(f.Confidence) || math.IsInf(f.Confidence, 0) || f.Confidence < 0 || f.Confidence > 1 {
		return fmt.Errorf("confidence must be between 0 and 1")
	}
	if !validSource(f.Source) {
		return fmt.Errorf("unknown source %q", f.Source)
	}
	if f.ProposedFix != nil {
		if err := f.validateProposedFix(); err != nil {
			return err
		}
	}
	return nil
}

func (f Finding) validateProposedFix() error {
	fix := f.ProposedFix
	if strings.TrimSpace(fix.Description) == "" || len(fix.Description) > MaxFixDescriptionBytes {
		return fmt.Errorf("proposed fix description must be nonblank and at most %d bytes", MaxFixDescriptionBytes)
	}
	if fix.StartLine != f.StartLine || fix.EndLine != f.EndLine {
		return fmt.Errorf("proposed fix range must exactly match finding range")
	}
	if len(fix.Replacement) > MaxFixReplacementBytes {
		return fmt.Errorf("proposed fix replacement exceeds %d byte limit", MaxFixReplacementBytes)
	}
	if strings.IndexByte(fix.Description, 0) >= 0 || strings.IndexByte(fix.Replacement, 0) >= 0 {
		return fmt.Errorf("proposed fix must not contain NUL bytes")
	}
	if containsPlaceholder(fix.Description) || containsPlaceholder(fix.Replacement) {
		return fmt.Errorf("proposed fix must not contain redaction or truncation placeholders")
	}
	return nil
}

func containsPlaceholder(value string) bool {
	value = strings.ToLower(value)
	for _, marker := range []string{
		"[redacted",
		"<redacted",
		"[truncated",
		"<truncated",
		"[reviewer: partial hunk",
		"[reviewer: long diff line truncated",
	} {
		if strings.Contains(value, marker) {
			return true
		}
	}
	return false
}

func normalizedTitle(value string) string {
	return strings.Join(strings.Fields(strings.ToLower(value)), " ")
}

// Sort orders findings by severity and source location for stable output.
func Sort(values []Finding) {
	sort.SliceStable(values, func(left, right int) bool {
		leftRank := severityRank(values[left].Severity)
		rightRank := severityRank(values[right].Severity)
		if leftRank != rightRank {
			return leftRank < rightRank
		}
		if values[left].File != values[right].File {
			return values[left].File < values[right].File
		}
		if values[left].StartLine != values[right].StartLine {
			return values[left].StartLine < values[right].StartLine
		}
		return values[left].Title < values[right].Title
	})
}

func validSeverity(value Severity) bool {
	switch value {
	case SeverityCritical, SeverityHigh, SeverityMedium, SeverityLow, SeverityInfo:
		return true
	default:
		return false
	}
}

func validCategory(value Category) bool {
	switch value {
	case CategorySecurity, CategoryCorrectness, CategoryPerformance, CategoryDatabase, CategoryMaintainability, CategoryQuality:
		return true
	default:
		return false
	}
}

func validSource(value string) bool {
	switch value {
	case SourceLocalRule, SourceStaticAnalysis, SourceSQLAnalyzer, SourceAI:
		return true
	default:
		return false
	}
}

func severityRank(severity Severity) int {
	switch severity {
	case SeverityCritical:
		return 0
	case SeverityHigh:
		return 1
	case SeverityMedium:
		return 2
	case SeverityLow:
		return 3
	case SeverityInfo:
		return 4
	default:
		return 5
	}
}
