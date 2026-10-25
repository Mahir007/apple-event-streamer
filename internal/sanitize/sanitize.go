// Package sanitize makes untrusted text safe to print to a terminal.
package sanitize

import (
	"strings"
	"unicode"
)

// Text replaces control characters (including escape sequences' ESC, tabs
// and line breaks), invalid UTF-8 and bidirectional overrides with U+FFFD,
// so text from the network can't move the cursor, recolour or retitle the
// terminal, or reorder what is shown.
func Text(s string) string {
	if isClean(s) {
		return s
	}
	return strings.Map(func(r rune) rune {
		if bad(r) {
			return unicode.ReplacementChar
		}
		return r
	}, strings.ToValidUTF8(s, string(unicode.ReplacementChar)))
}

func isClean(s string) bool {
	for _, r := range s {
		if r == unicode.ReplacementChar || bad(r) {
			return false
		}
	}
	return true
}

func bad(r rune) bool {
	switch {
	case unicode.IsControl(r): // C0, DEL and C1
		return true
	case r == '\u061c', r == '\u200e', r == '\u200f', // bidi marks
		r >= '\u202a' && r <= '\u202e', // embeddings and overrides
		r >= '\u2066' && r <= '\u2069': // isolates
		return true
	}
	return false
}
