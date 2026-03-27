package security

import (
	"fmt"
	"regexp"
	"strings"
)

// Pattern represents a known prompt injection signature.
type Pattern struct {
	Name     string
	Regex    *regexp.Regexp
	Severity int    // 1=low, 2=medium, 3=high
	Category string // "instruction_override", "role_hijack", "data_exfil", "invisible_text"
}

// Match represents a single pattern match within scanned text.
type Match struct {
	Pattern  string
	Content  string // matched text (truncated to 100 chars)
	Severity int
	Position int // character offset
}

// ScanResult contains the outcome of scanning text for injection patterns.
type ScanResult struct {
	RiskScore float64 // 0.0 - 1.0
	Matches   []Match
	Clean     bool
}

// SignatureDB holds a collection of injection patterns.
type SignatureDB struct {
	patterns []Pattern
}

// NewSignatureDB creates a SignatureDB pre-loaded with common injection patterns.
func NewSignatureDB() *SignatureDB {
	db := &SignatureDB{}
	db.loadDefaults()
	return db
}

// AddPattern adds a custom pattern to the database.
func (db *SignatureDB) AddPattern(name, regex string, severity int, category string) error {
	re, err := regexp.Compile(regex)
	if err != nil {
		return fmt.Errorf("compile pattern %q: %w", name, err)
	}
	db.patterns = append(db.patterns, Pattern{
		Name:     name,
		Regex:    re,
		Severity: severity,
		Category: category,
	})
	return nil
}

// Scan checks text for injection patterns and returns a ScanResult.
func (db *SignatureDB) Scan(text string) ScanResult {
	if text == "" {
		return ScanResult{Clean: true}
	}

	var matches []Match
	maxSeverity := 0

	for _, p := range db.patterns {
		locs := p.Regex.FindAllStringIndex(text, -1)
		for _, loc := range locs {
			matched := text[loc[0]:loc[1]]
			if len(matched) > 100 {
				matched = matched[:100]
			}
			matches = append(matches, Match{
				Pattern:  p.Name,
				Content:  matched,
				Severity: p.Severity,
				Position: loc[0],
			})
			if p.Severity > maxSeverity {
				maxSeverity = p.Severity
			}
		}
	}

	if len(matches) == 0 {
		return ScanResult{Clean: true}
	}

	// Risk score: weighted by severity and match count, capped at 1.0
	score := 0.0
	for _, m := range matches {
		switch m.Severity {
		case 1:
			score += 0.1
		case 2:
			score += 0.25
		case 3:
			score += 0.4
		}
	}
	if score > 1.0 {
		score = 1.0
	}

	return ScanResult{
		RiskScore: score,
		Matches:   matches,
		Clean:     false,
	}
}

// ScanPage is a convenience that scans HTML content, stripping tags to focus on text.
func (db *SignatureDB) ScanPage(htmlContent string) ScanResult {
	// Scan both raw HTML and stripped text to catch patterns in attributes
	result := db.Scan(htmlContent)

	// Also scan stripped version for patterns hidden in tag attributes
	stripped := stripHTML(htmlContent)
	textResult := db.Scan(stripped)

	// Merge results, deduplicating
	if result.Clean && textResult.Clean {
		return ScanResult{Clean: true}
	}

	// Use whichever has the higher risk score
	if textResult.RiskScore > result.RiskScore {
		return textResult
	}
	return result
}

// stripHTML removes HTML tags (basic, not a full parser).
func stripHTML(s string) string {
	var b strings.Builder
	inTag := false
	for _, r := range s {
		switch {
		case r == '<':
			inTag = true
		case r == '>':
			inTag = false
			b.WriteRune(' ')
		case !inTag:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func (db *SignatureDB) loadDefaults() {
	defaults := []struct {
		name     string
		regex    string
		severity int
		category string
	}{
		// Instruction overrides (severity 3)
		{
			name:     "ignore_previous_instructions",
			regex:    `(?i)ignore\s+(all\s+)?previous\s+instructions`,
			severity: 3,
			category: "instruction_override",
		},
		{
			name:     "disregard_instructions",
			regex:    `(?i)disregard\s+(all\s+)?(previous\s+|prior\s+|above\s+)?instructions`,
			severity: 3,
			category: "instruction_override",
		},
		{
			name:     "forget_instructions",
			regex:    `(?i)forget\s+(all\s+)?(previous\s+|prior\s+)?instructions`,
			severity: 3,
			category: "instruction_override",
		},
		// Role hijacking (severity 2)
		{
			name:     "role_hijack_you_are",
			regex:    `(?i)you\s+are\s+now\s+(a|an|the)\s+`,
			severity: 2,
			category: "role_hijack",
		},
		{
			name:     "role_hijack_pretend",
			regex:    `(?i)pretend\s+you\s+are\s+`,
			severity: 2,
			category: "role_hijack",
		},
		{
			name:     "role_hijack_act_as",
			regex:    `(?i)act\s+as\s+(a|an|the|if)\s+`,
			severity: 2,
			category: "role_hijack",
		},
		// System prompt markers (severity 3)
		{
			name:     "system_prompt_marker",
			regex:    `(?m)^[\s]*[Ss][Yy][Ss][Tt][Ee][Mm]\s*:`,
			severity: 3,
			category: "instruction_override",
		},
		// Instruction markers (severity 3)
		{
			name:     "inst_markers",
			regex:    `\[\[INST\]\]|\[/INST\]|\[INST\]`,
			severity: 3,
			category: "instruction_override",
		},
		// Zero-width characters (severity 2)
		{
			name:     "zero_width_chars",
			regex:    `[\x{200B}\x{200C}\x{200D}\x{FEFF}]{2,}`,
			severity: 2,
			category: "invisible_text",
		},
		// Base64 instructions (severity 1) — long base64 strings in suspicious contexts
		{
			name:     "base64_instructions",
			regex:    `(?i)(?:eval|decode|execute|run)\s*\(\s*(?:atob|base64)\s*\(\s*['"]([A-Za-z0-9+/=]{40,})['"]`,
			severity: 1,
			category: "data_exfil",
		},
		// Data URI in hidden elements (severity 2)
		{
			name:     "data_uri_text",
			regex:    `data:text/[^;]+;base64,[A-Za-z0-9+/=]{20,}`,
			severity: 2,
			category: "data_exfil",
		},
		// New instruction injection (severity 2)
		{
			name:     "new_instructions",
			regex:    `(?i)(?:new|updated|revised|real)\s+(?:instructions?|prompt|directive)s?\s*:`,
			severity: 2,
			category: "instruction_override",
		},
		// Do not follow / override (severity 3)
		{
			name:     "do_not_follow",
			regex:    `(?i)do\s+not\s+follow\s+(the\s+)?(previous|original|above)\s+`,
			severity: 3,
			category: "instruction_override",
		},
	}

	for _, d := range defaults {
		_ = db.AddPattern(d.name, d.regex, d.severity, d.category)
	}
}
