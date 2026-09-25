// Package oneline folds prose into one bounded line. It is its own package
// because the fold is wanted by packages that cannot import one another —
// internal/backend and internal/cli among them — and a fold copied into each is
// a fix that has to be made in every copy.
package oneline

import (
	"strings"
	"unicode/utf8"
)

// Marker is what a folded line ends with when it was cut.
const Marker = "..."

// Fold collapses every run of whitespace in value to a single space, so a
// multi-line message becomes one line, and bounds the result to limit bytes
// before Marker. The cut falls on a rune boundary, because what is folded here
// is prose somebody wrote and half a rune is not a shorter line but a broken
// one. A value that already fits comes back folded and unmarked.
func Fold(value string, limit int) string {
	folded := strings.Join(strings.Fields(value), " ")
	if len(folded) <= limit {
		return folded
	}
	return cut(folded, limit) + Marker
}

// Bound is Fold without the marker: the folded line cut on a rune boundary to at
// most limit bytes, for a record whose bound leaves no room for one or whose
// reader is told some other way that it was cut.
func Bound(value string, limit int) string {
	folded := strings.Join(strings.Fields(value), " ")
	if len(folded) <= limit {
		return folded
	}
	return cut(folded, limit)
}

func cut(folded string, limit int) string {
	end := max(limit, 0)
	for end > 0 && !utf8.RuneStart(folded[end]) {
		end--
	}
	return strings.TrimSpace(folded[:end])
}
