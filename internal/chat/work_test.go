package chat

import (
	"strings"
	"testing"
)

// An integrated change whose developer said it does not discharge its item has
// to read as that in the one line an operator reads first. The two runs are
// otherwise identical — both promoted a change into the target branch — and only
// one of them is work still to do, so a headline that mentioned neither the
// closure nor its absence would leave the harder of the two invisible.
func TestTheHeadlineSaysWhenAnIntegratedChangeLeftItsItemOpen(t *testing.T) {
	t.Parallel()

	promoted := RunReport{WorkItemID: "yoyodyne-1", Integrated: true, TargetBranch: "main", Commit: "0f1e2d3c"}

	discharged := promoted
	discharged.WorkItemClosed = true
	if headline := discharged.Headline(); !strings.Contains(headline, "the item is closed") {
		t.Errorf("a discharged item does not read as closed: %q", headline)
	}

	undischarged := promoted
	undischarged.Undischarged = true
	undischarged.UndischargedReason = "the design this needs has not landed"
	undischarged.UndischargedDisposition = "is parked until somebody releases it"
	headline := undischarged.Headline()
	// Where the item was left is as much of the answer as that it was not closed:
	// a parking waits for a person and a dependency releases itself, and a reader
	// told only "still open" goes looking for the item in a queue nothing is going
	// to offer it from.
	if !strings.Contains(headline, "the item is parked until somebody releases it") {
		t.Errorf("an undischarged item does not say where it was left: %q", headline)
	}
	if !strings.Contains(headline, "the design this needs has not landed") {
		t.Errorf("the headline does not say why the item stays open: %q", headline)
	}
	if strings.Contains(headline, "the item is closed") {
		t.Errorf("the headline claims a closure that did not happen: %q", headline)
	}

	waiting := undischarged
	waiting.UndischargedDisposition = "stays open waiting on yoyodyne-impediment"
	if headline := waiting.Headline(); !strings.Contains(headline, "waiting on yoyodyne-impediment") {
		t.Errorf("an item left waiting on its impediment does not name it: %q", headline)
	}
}
