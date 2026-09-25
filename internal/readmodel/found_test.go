package readmodel

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// A reason too long for the record is cut on a rune boundary: the error that
// explains why a look could not be made can carry multi-byte text like any
// other, and half a rune stored is not a shorter reason but a broken one.
func TestAnUncheckedReasonIsCutOnARuneBoundary(t *testing.T) {
	t.Parallel()

	bounded := boundUnchecked("x" + strings.Repeat("é", triage.MaxFoundUncheckedBytes))
	if !utf8.ValidString(bounded) || len(bounded) > triage.MaxFoundUncheckedBytes || !strings.HasSuffix(bounded, "...") {
		t.Fatalf("boundUnchecked() is %d bytes, valid UTF-8 %t; want valid text within %d bytes marked as cut",
			len(bounded), utf8.ValidString(bounded), triage.MaxFoundUncheckedBytes)
	}
}
