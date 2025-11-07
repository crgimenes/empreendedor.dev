package utils

import (
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

// Limits (in runes)
const (
	MaxTagsCount = 20
	MaxTagLength = 32
	MaxTotalCSV  = 512
)

// TagError provides typed errors for tag validation
type TagError struct{ msg string }

func (e TagError) Error() string   { return e.msg }
func newTagError(msg string) error { return TagError{msg: msg} }

var (
	ErrInvalidUTF8 = newTagError("invalid UTF-8 input")
	ErrEmptyTag    = newTagError("empty tag")
	ErrTagTooLong  = newTagError("tag too long")
	ErrTooManyTags = newTagError("too many tags")
	ErrCSVTooLong  = newTagError("total tags too large")
	ErrInvalidChar = newTagError("invalid characters in tag")
)

// NormalizeTagsCSV parses a CSV of tags and returns a canonical CSV and slice.
// Rules:
// - Unicode NFKC normalization
// - Trim, collapse internal spaces, remove control/format chars
// - Allowed runes: Letters (L), Marks (M), Numbers (N), '.', '_', '-', '/', and space
// - No comma allowed inside tags
// - Case-insensitive dedupe (Unicode-aware), accents preserved
// - Enforce limits: 32 runes per tag, 20 tags total, 512 runes total CSV
func NormalizeTagsCSV(in string) (string, []string, error) {
	if in == "" {
		return "", nil, nil
	}
	if !utf8.ValidString(in) {
		return "", nil, ErrInvalidUTF8
	}

	raw := strings.Split(in, ",")
	caser := cases.Fold()
	seen := make(map[string]struct{}, len(raw))
	out := make([]string, 0, len(raw))

	add := func(tag string) error {
		tag, invalid := unicodeSanitize(tag)
		if invalid {
			return ErrInvalidChar
		}
		if tag == "" {
			return nil
		}
		if runeLen(tag) > MaxTagLength {
			return ErrTagTooLong
		}
		key := caser.String(tag)
		if _, ok := seen[key]; ok {
			return nil
		}
		if len(out) >= MaxTagsCount {
			return ErrTooManyTags
		}
		seen[key] = struct{}{}
		out = append(out, tag)
		return nil
	}

	for _, p := range raw {
		if err := add(p); err != nil {
			return "", nil, err
		}
	}

	csv := strings.Join(out, ",")
	if runeLen(csv) > MaxTotalCSV {
		return "", nil, ErrCSVTooLong
	}
	return csv, append([]string(nil), out...), nil
}

// unicodeSanitize normalizes to NFKC, trims, collapses spaces, filters allowed runes
// unicodeSanitize normalizes input and enforces allowed runes.
// It returns the cleaned string and a flag indicating an invalid (disallowed) rune was found.
func unicodeSanitize(s string) (string, bool) {
	if s == "" {
		return "", false
	}
	// Normalize to NFKC
	s = norm.NFKC.String(s)
	s = strings.TrimSpace(s)
	if s == "" {
		return "", false
	}

	var b strings.Builder
	b.Grow(len(s))
	spacePending := false
	invalid := false

	for _, r := range s {
		if r == ',' { // never allow comma inside a tag
			invalid = true
			continue
		}
		if unicode.Is(unicode.Cc, r) || unicode.Is(unicode.Cf, r) { // drop control/format
			continue
		}
		if unicode.Is(unicode.L, r) || unicode.Is(unicode.M, r) || unicode.Is(unicode.N, r) ||
			r == '.' || r == '_' || r == '-' || r == '/' {
			if spacePending {
				b.WriteRune(' ')
				spacePending = false
			}
			b.WriteRune(r)
			continue
		}
		if unicode.IsSpace(r) {
			spacePending = true
			continue
		}
		// Any other visible character is considered invalid
		invalid = true
	}

	out := strings.TrimSpace(b.String())
	return out, invalid
}

func runeLen(s string) int { return utf8.RuneCountInString(s) }

// Helper to validate a single tag according to the same constraints.
// Returns nil if valid, error otherwise.
func ValidateTag(t string) error {
	t, invalid := unicodeSanitize(t)
	if invalid {
		return ErrInvalidChar
	}
	if t == "" {
		return ErrEmptyTag
	}
	if runeLen(t) > MaxTagLength {
		return ErrTagTooLong
	}
	// If unicodeSanitize produced a non-empty string, it contains only allowed runes.
	return nil
}

// Optional deterministic sort if callers want it.
func SortTags(tags []string) []string {
	cp := append([]string(nil), tags...)
	sort.Strings(cp)
	return cp
}
