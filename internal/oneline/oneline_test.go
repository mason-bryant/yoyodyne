package oneline

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestFoldCollapsesWhitespaceAndLeavesAFittingLineUnmarked(t *testing.T) {
	got := Fold("  git failed:\n\tnot a repository  \n", 160)
	if got != "git failed: not a repository" {
		t.Fatalf("Fold = %q", got)
	}
}

func TestFoldCutsOnARuneBoundaryAndMarksTheCut(t *testing.T) {
	// Every em dash is three bytes, so a ten-byte bound lands inside one.
	got := Fold(strings.Repeat("—", 10), 10)
	if !utf8.ValidString(got) {
		t.Fatalf("Fold cut mid-rune: %q", got)
	}
	if got != "———"+Marker {
		t.Fatalf("Fold = %q, want three whole dashes and the marker", got)
	}
}

func TestFoldTrimsTheSpaceTheCutLeavesBeforeTheMarker(t *testing.T) {
	got := Fold("abcd efgh", 5)
	if got != "abcd"+Marker {
		t.Fatalf("Fold = %q", got)
	}
}

func TestFoldAtAZeroBoundIsTheMarkerAlone(t *testing.T) {
	if got := Fold("anything", 0); got != Marker {
		t.Fatalf("Fold = %q", got)
	}
	if got := Fold("", 0); got != "" {
		t.Fatalf("Fold of nothing = %q", got)
	}
}
