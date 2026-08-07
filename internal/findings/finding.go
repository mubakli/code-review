package findings

import (
	"fmt"
	"math"
	"sort"
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

// Finding is the common output contract for local and AI-assisted analyzers.
type Finding struct {
	File       string   `json:"file"`
	StartLine  int      `json:"startLine"`
	EndLine    int      `json:"endLine"`
	Severity   Severity `json:"severity"`
	Category   Category `json:"category"`
	Title      string   `json:"title"`
	Message    string   `json:"message"`
	Suggestion string   `json:"suggestion,omitempty"`
	Confidence float64  `json:"confidence"`
	Source     string   `json:"source"`
	AgentID    string   `json:"agentId,omitempty"`
}

// Validate rejects malformed analyzer output before it reaches CLI or editor
// consumers. The same validation can later be applied to untrusted AI output.
func (f Finding) Validate() error {
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
	return nil
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
