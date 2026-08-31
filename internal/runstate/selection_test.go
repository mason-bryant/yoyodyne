package runstate

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

var selectionMoment = time.Date(2026, 8, 30, 11, 0, 0, 0, time.UTC)

// The reason a run exists is assembled from a fixed sentence and prose that
// arrived with somebody's instruction, and neither end of that can see the
// bound. A reason that crosses it must lose its tail, not the whole record: a
// run carrying no selection is what an unaccounted run looks like, and it must
// not be reachable by writing at length.
func TestAnOverLengthReasonIsTruncatedRatherThanDropped(t *testing.T) {
	t.Parallel()

	opening := "the development manager weighed the queue and chose this item because "
	reason := opening + strings.Repeat("the ground moved. ", MaxSelectionReasonBytes/8)
	if len(reason) <= MaxSelectionReasonBytes {
		t.Fatalf("the reason under test is %d bytes, which does not cross the %d byte bound", len(reason), MaxSelectionReasonBytes)
	}

	stamped, stated := Selection{By: SelectedByDevelopmentManager, Reason: reason}.Stamped(selectionMoment)
	if !stated {
		t.Fatal("Stamped() dropped a stated selection, so the run would record no reason at all")
	}
	if err := stamped.Validate(); err != nil {
		t.Fatalf("Validate() = %v, want a selection a run can record", err)
	}
	if len(stamped.Reason) > MaxSelectionReasonBytes {
		t.Fatalf("recorded reason is %d bytes, which exceeds the %d byte bound", len(stamped.Reason), MaxSelectionReasonBytes)
	}
	if !strings.HasPrefix(stamped.Reason, opening) {
		t.Fatalf("recorded reason = %q, want the argument kept from its beginning", stamped.Reason)
	}
	// Somebody reading the record has to be able to tell a cut argument from one
	// that ended where it ends.
	if !strings.HasSuffix(stamped.Reason, "truncated to the recorded bound") {
		t.Fatalf("recorded reason ends %q, want it to say where it was cut", stamped.Reason[len(stamped.Reason)-64:])
	}
	if !stamped.At.Equal(selectionMoment) {
		t.Fatalf("recorded moment = %s, want the moment the run was reserved", stamped.At)
	}
}

// The bound is on bytes and a reason is prose somebody wrote, so the cut can
// land inside a rune. One that does is not text, and it is what a record
// rendered to an operator would show them.
func TestATruncatedReasonIsStillText(t *testing.T) {
	t.Parallel()

	// Three-byte runes end on the bound only every third repetition, so at least
	// two of these three walk the cut back off a rune boundary.
	for _, pad := range []int{0, 1, 2} {
		reason := strings.Repeat("x", pad) + strings.Repeat("…", MaxSelectionReasonBytes)
		stamped, stated := Selection{By: SelectedByScheduler, Reason: reason}.Stamped(selectionMoment)
		if !stated {
			t.Fatalf("pad %d: Stamped() dropped a stated selection", pad)
		}
		if !utf8.ValidString(stamped.Reason) {
			t.Fatalf("pad %d: recorded reason is not valid UTF-8, so it was cut mid-rune", pad)
		}
		if len(stamped.Reason) > MaxSelectionReasonBytes {
			t.Fatalf("pad %d: recorded reason is %d bytes, which exceeds the %d byte bound", pad, len(stamped.Reason), MaxSelectionReasonBytes)
		}
	}
}

// Whether a caller said who chose the work and why is the whole of what makes a
// selection recordable. Everything else about a stated one is settled on the way
// in, so no stated selection reaches the record as nothing.
func TestNoStatedSelectionIsDropped(t *testing.T) {
	t.Parallel()

	long := strings.Repeat("a", MaxSelectionReasonBytes*4)
	for name, selection := range map[string]Selection{
		"exactly at the bound":  {By: SelectedByScheduler, Reason: strings.Repeat("a", MaxSelectionReasonBytes)},
		"one byte over":         {By: SelectedByScheduler, Reason: strings.Repeat("a", MaxSelectionReasonBytes+1)},
		"far over":              {By: SelectedByDevelopmentManager, Reason: long},
		"over, and undated":     {By: SelectedByDevelopmentManager, Reason: long, At: time.Time{}},
		"over, with its moment": {By: SelectedByOperator, Reason: long, At: selectionMoment.Add(-time.Hour)},
		"over, padded":          {By: "  " + SelectedByOperator + "  ", Reason: "  " + long + "  "},
	} {
		stamped, stated := selection.Stamped(selectionMoment)
		if !stated {
			t.Fatalf("%s: Stamped() reported nothing to record, so the run would be unaccounted for", name)
		}
		if err := stamped.Validate(); err != nil {
			t.Fatalf("%s: Validate() = %v, so a run recording this selection would be refused", name, err)
		}
		if stamped.At.IsZero() {
			t.Fatalf("%s: recorded selection carries no moment: %#v", name, stamped)
		}
	}
}

// Nothing above weakens the one case that is genuinely nothing: a caller who
// named nobody, or gave no grounds, still records no selection rather than a
// record of something that did not happen.
func TestAnUnstatedSelectionIsStillNothing(t *testing.T) {
	t.Parallel()

	for name, selection := range map[string]Selection{
		"nobody chose it":  {Reason: "the queue was ready"},
		"no grounds given": {By: SelectedByScheduler},
		"blank throughout": {By: "   ", Reason: "  \n ", At: selectionMoment},
	} {
		if stamped, stated := selection.Stamped(selectionMoment); stated {
			t.Fatalf("%s: Stamped() = %#v, true, want nothing recorded rather than a half-formed record", name, stamped)
		}
	}
}
