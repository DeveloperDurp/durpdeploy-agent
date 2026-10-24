package executor

import (
	"errors"
	"os"
	"regexp"
	"sort"
	"strings"
)

// commonSecretPatterns match well-known credential formats that show up in
// deploy logs even when they were never registered as a secret variable
// (e.g. a script echoes a token it fetched at runtime).
var commonSecretPatterns = []string{
	`Bearer\s+[A-Za-z0-9._~+/-]+=*`,
	`ghp_[A-Za-z0-9]{36,}`,
	`AKIA[0-9A-Z]{16}`,
	`xox[bap]-[A-Za-z0-9-]+`,
	`(?i:\b(password|token|key)\s*=\s*[^\s"']+)`,
}

// Scrubber redacts known secret values and common credential patterns from
// arbitrary text using a single pre-compiled regex. Redaction is best-effort:
// it catches literal secret values and a handful of common token formats, not
// every possible secret shape.
type Scrubber struct {
	re *regexp.Regexp
}

// NewScrubber compiles a single regex matching every literal secret (longest
// first, so a longer secret is redacted whole rather than leaving a suffix
// behind) plus the common credential patterns. Empty secrets are ignored.
func NewScrubber(secrets []string) *Scrubber {
	scrubber, err := NewScrubberWithPatterns(secrets, os.Getenv("DURPDEPLOY_EXTRA_SCRUB_PATTERNS"))
	if err != nil {
		// The legacy signature cannot report invalid patterns. Redact all output.
		return &Scrubber{re: regexp.MustCompile("(?s).+")}
	}
	return scrubber
}

// NewScrubberWithPatterns keeps built-in and literal-secret redaction while
// adding captured comma-separated patterns. Errors never include their input.
func NewScrubberWithPatterns(secrets []string, extra string) (*Scrubber, error) {
	patterns, err := scrubPatterns(extra)
	if err != nil {
		return nil, err
	}
	literals := scrubLiterals(secrets)
	parts := make([]string, 0, len(literals)+len(patterns))
	for _, s := range literals {
		parts = append(parts, regexp.QuoteMeta(s))
	}
	parts = append(parts, patterns...)
	if len(parts) == 0 {
		return &Scrubber{}, nil
	}
	// (?s) also protects literal secrets containing newlines.
	re, err := regexp.Compile("(?s)(" + strings.Join(parts, "|") + ")")
	if err != nil {
		return nil, errors.New("invalid log scrub patterns")
	}
	return &Scrubber{re: re}, nil
}

func scrubPatterns(extra string) ([]string, error) {
	patterns := append([]string(nil), commonSecretPatterns...)
	for _, pattern := range strings.Split(extra, ",") {
		pattern = strings.TrimSpace(pattern)
		if pattern == "" {
			continue
		}
		if _, err := regexp.Compile(pattern); err != nil {
			return nil, errors.New("invalid extra scrub pattern")
		}
		patterns = append(patterns, pattern)
	}
	return patterns, nil
}

func scrubLiterals(secrets []string) []string {
	literals := make([]string, 0, len(secrets))
	for _, s := range secrets {
		if s == "" {
			continue
		}
		literals = append(literals, s)
		// Logs can arrive one line at a time. Protect each non-empty component
		// of a multiline secret as well as the complete value.
		if strings.Contains(s, "\n") {
			for _, line := range strings.Split(s, "\n") {
				if line != "" {
					literals = append(literals, line)
				}
			}
		}
	}
	sort.Slice(literals, func(i, j int) bool {
		return len(literals[i]) > len(literals[j])
	})

	return literals
}

// Scrub replaces every match with "[REDACTED]". A zero-value Scrubber (no
// secrets, no patterns) is a no-op.
func (s *Scrubber) Scrub(text string) string {
	if s == nil || s.re == nil {
		return text
	}
	return s.re.ReplaceAllString(text, "[REDACTED]")
}
