package embedded

import (
	"strings"
	"unicode/utf8"
)

// Unicode confusable-character detection and normalization.
//
// Ported (thinly) from grok-build's unicode_confusables.rs + search_replace
// normalized-match helpers. Narrow typography-focused map only: smart quotes,
// dashes, ellipsis, NBSP. Used solely for match-position discovery; replacements
// always apply to original file bytes.

// confusableMap maps visually confusable Unicode runes to ASCII equivalents.
// Replacement may be multi-byte (em-dash → "--", ellipsis → "...").
var confusableMap = map[rune]string{
	'\u201C': `"`,   // left double quotation mark
	'\u201D': `"`,   // right double quotation mark
	'\u2018': "'",   // left single quotation mark
	'\u2019': "'",   // right single quotation mark
	'\u2014': "--",  // em-dash
	'\u2013': "-",   // en-dash
	'\u2026': "...", // horizontal ellipsis
	'\u00A0': " ",   // non-breaking space
}

// hasConfusables reports whether s contains any mapped typography confusable.
func hasConfusables(s string) bool {
	for _, r := range s {
		if _, ok := confusableMap[r]; ok {
			return true
		}
	}
	return false
}

// normalizeConfusables replaces mapped confusables with their ASCII equivalents.
// Non-mapped runes (including CJK/emoji) pass through unchanged.
func normalizeConfusables(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if rep, ok := confusableMap[r]; ok {
			b.WriteString(rep)
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// buildOffsetMap returns the confusable-normalized form of s and a map from
// each normalized byte index to the corresponding original byte index.
// offsetMap has length len(normalized)+1; the terminal sentinel equals len(s).
//
// For a normalized match [normStart:normEnd]:
//
//	origStart = offsetMap[normStart]
//	origEnd   = offsetMap[normEnd]
//	normalizeConfusables(s[origStart:origEnd]) == normalized[normStart:normEnd]
func buildOffsetMap(s string) (normalized string, offsetMap []int) {
	var b strings.Builder
	b.Grow(len(s))
	offsetMap = make([]int, 0, len(s)+1)

	for origByteOffset, r := range s {
		if rep, ok := confusableMap[r]; ok {
			// One map entry per replacement *byte* (ASCII replacements are 1:1).
			for i := 0; i < len(rep); i++ {
				offsetMap = append(offsetMap, origByteOffset)
			}
			b.WriteString(rep)
			continue
		}
		// Map each UTF-8 byte of the rune to its own original position.
		runeStart := origByteOffset
		runeLen := utf8.RuneLen(r)
		if runeLen < 0 {
			runeLen = 1
		}
		for i := 0; i < runeLen; i++ {
			offsetMap = append(offsetMap, runeStart+i)
		}
		b.WriteRune(r)
	}
	// Terminal sentinel.
	offsetMap = append(offsetMap, len(s))
	return b.String(), offsetMap
}

// normalizedMatch is a match found via confusable-normalized comparison,
// expressed in the original text's byte coordinates.
type normalizedMatch struct {
	originalStart int
	originalLen   int
}

// normalizedMatchKind classifies findNormalizedMatchPositions results.
type normalizedMatchKind int

const (
	normNoMatch normalizedMatchKind = iota
	normMatches
	normAmbiguous
)

// findNormalizedMatchPositions finds matches using confusable-normalized
// comparison and remaps them back to original byte coordinates.
//
// Safety:
//   - Roundtrip: normalizeConfusables(originalSlice) must equal normPattern
//     (rejects partial expansions like "-" inside em-dash "—").
//   - Overlapping remapped spans → Ambiguous (fail-closed).
//   - Candidates that all fail roundtrip → Ambiguous (not NoMatch).
func findNormalizedMatchPositions(text, pattern string) (normalizedMatchKind, []normalizedMatch) {
	normText, offsetMap := buildOffsetMap(text)
	normPattern := normalizeConfusables(pattern)
	if normPattern == "" {
		return normNoMatch, nil
	}

	var validated []normalizedMatch
	hadRejected := false

	// Non-overlapping scan in normalized space (mirrors Rust match_indices).
	searchFrom := 0
	for searchFrom <= len(normText)-len(normPattern) {
		idx := strings.Index(normText[searchFrom:], normPattern)
		if idx < 0 {
			break
		}
		normStart := searchFrom + idx
		normEnd := normStart + len(normPattern)

		origStart := offsetMap[normStart]
		origEnd := offsetMap[normEnd]

		if origEnd <= origStart {
			hadRejected = true
			searchFrom = normStart + 1 // advance past this candidate
			continue
		}

		origSlice := text[origStart:origEnd]
		if normalizeConfusables(origSlice) != normPattern {
			hadRejected = true
			searchFrom = normStart + 1
			continue
		}

		validated = append(validated, normalizedMatch{
			originalStart: origStart,
			originalLen:   origEnd - origStart,
		})
		// Non-overlapping: advance past this match in normalized space.
		searchFrom = normEnd
	}

	if len(validated) == 0 {
		if hadRejected {
			return normAmbiguous, nil
		}
		return normNoMatch, nil
	}

	// Reject overlapping remapped spans (fail closed).
	for i := 1; i < len(validated); i++ {
		endPrev := validated[i-1].originalStart + validated[i-1].originalLen
		if endPrev > validated[i].originalStart {
			return normAmbiguous, nil
		}
	}

	return normMatches, validated
}

// replaceNormalizedMatches replaces each match region with newText.
// Matches must be non-overlapping and sorted by originalStart.
func replaceNormalizedMatches(text string, matches []normalizedMatch, newText string) string {
	var b strings.Builder
	b.Grow(len(text))
	lastEnd := 0
	for _, m := range matches {
		b.WriteString(text[lastEnd:m.originalStart])
		b.WriteString(newText)
		lastEnd = m.originalStart + m.originalLen
	}
	b.WriteString(text[lastEnd:])
	return b.String()
}
