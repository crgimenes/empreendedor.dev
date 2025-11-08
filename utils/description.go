package utils

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

// MaxDescriptionRunes defines the maximum number of runes allowed for descriptions.
const MaxDescriptionRunes = 500

// SanitizeDescription normalizes text and removes control/format characters, limits length by runes,
// and trims whitespace. Intended for user-provided file descriptions.
func SanitizeDescription(in string) string {
	if in == "" {
		return ""
	}
	s := norm.NFKC.String(in)
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if r == '\n' || r == '\t' {
			b.WriteRune(r)
			continue
		}
		if unicode.Is(unicode.Cc, r) || unicode.Is(unicode.Cf, r) {
			continue
		}
		b.WriteRune(r)
	}
	out := strings.TrimSpace(b.String())
	if out == "" {
		return ""
	}
	// Truncate by runes if needed
	if utf8.RuneCountInString(out) > MaxDescriptionRunes {
		runes := make([]rune, 0, MaxDescriptionRunes)
		for i, r := range out {
			if i >= MaxDescriptionRunes {
				break
			}
			runes = append(runes, r)
		}
		out = string(runes)
	}
	return out
}
