package insight

import (
	"regexp"
	"strings"
)

// RedactionStats reports what the deterministic redactor removed, so the send
// preview can tell the user how much was scrubbed. It is a floor, not a
// guarantee: the UI must state that this cannot catch every natural-language
// PII, only known credential shapes.
type RedactionStats struct {
	Secrets   int `json:"secrets"`
	HomePaths int `json:"home_paths"`
	Emails    int `json:"emails"`
}

// Total is the combined count of redactions applied.
func (s RedactionStats) Total() int { return s.Secrets + s.HomePaths + s.Emails }

var (
	// Known credential shapes: bearer/authorization headers, OpenAI-style
	// keys, AWS access keys, generic "token=/api_key=/secret=" assignments, and
	// PEM private-key blocks. These are deterministic formats, not heuristics.
	reBearer     = regexp.MustCompile(`(?i)(authorization\s*[:=]\s*)(bearer\s+)?[A-Za-z0-9._\-]{12,}`)
	reOpenAIKey  = regexp.MustCompile(`\bsk-[A-Za-z0-9._\-]{16,}\b`)
	reAWSKey     = regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`)
	reGHToken    = regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{20,}\b`)
	reAssignment = regexp.MustCompile(`(?i)\b(api[_\-]?key|secret|token|password|passwd|access[_\-]?key)\b(\s*[:=]\s*)("?)[^\s"']{6,}("?)`)
	rePEM        = regexp.MustCompile(`(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?-----END [A-Z ]*PRIVATE KEY-----`)
	reEmail      = regexp.MustCompile(`\b[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}\b`)
	reHomePath   = regexp.MustCompile(`(/(?:home|Users)/[^/\s]+)`)
)

// Redact returns a copy of the bundle with known credential formats, emails and
// absolute home paths scrubbed from all model-visible text. It never mutates
// the input. Redaction runs before any bytes leave the machine; the API key of
// the model source itself, DB credentials, encrypted reasoning and unrelated
// system messages are excluded upstream (they never enter the bundle).
func Redact(b Bundle) (Bundle, RedactionStats) {
	var stats RedactionStats
	scrub := func(s string) string {
		return redactString(s, &stats)
	}

	out := b
	out.Session.Project = scrub(b.Session.Project)

	out.Facts = make([]EvidenceFact, len(b.Facts))
	for i, f := range b.Facts {
		nf := f
		nf.Statement = scrub(f.Statement)
		out.Facts[i] = nf
	}
	out.EvidenceGaps = make([]string, len(b.EvidenceGaps))
	for i, g := range b.EvidenceGaps {
		out.EvidenceGaps[i] = scrub(g)
	}
	// Findings metrics are numeric facts, not free text; refs are IDs. They are
	// left untouched.
	return out, stats
}

func redactString(s string, stats *RedactionStats) string {
	if s == "" {
		return s
	}
	replaceCount := func(re *regexp.Regexp, repl string, counter *int) {
		s = re.ReplaceAllStringFunc(s, func(m string) string {
			*counter++
			return repl
		})
	}
	// Secrets first (order matters: PEM before generic assignments).
	replaceCount(rePEM, "[REDACTED_PRIVATE_KEY]", &stats.Secrets)
	replaceCount(reOpenAIKey, "[REDACTED_KEY]", &stats.Secrets)
	replaceCount(reAWSKey, "[REDACTED_KEY]", &stats.Secrets)
	replaceCount(reGHToken, "[REDACTED_TOKEN]", &stats.Secrets)
	s = reBearer.ReplaceAllStringFunc(s, func(m string) string {
		stats.Secrets++
		// Keep the header name, drop the value.
		if i := strings.IndexAny(m, ":="); i >= 0 {
			return m[:i+1] + " [REDACTED]"
		}
		return "[REDACTED]"
	})
	s = reAssignment.ReplaceAllStringFunc(s, func(m string) string {
		stats.Secrets++
		return reAssignment.ReplaceAllString(m, "$1$2$3[REDACTED]$4")
	})
	replaceCount(reEmail, "[REDACTED_EMAIL]", &stats.Emails)
	replaceCount(reHomePath, "/home/[user]", &stats.HomePaths)
	return s
}
