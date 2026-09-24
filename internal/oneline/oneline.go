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
	cut := max(limit, 0)
	for cut > 0 && !utf8.RuneStart(folded[cut]) {
		cut--
	}
	return strings.TrimSpace(folded[:cut]) + Marker
}
