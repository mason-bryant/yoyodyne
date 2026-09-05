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
	headline := undischarged.Headline()
	if !strings.Contains(headline, "the item stays open") {
		t.Errorf("an undischarged item reads as an ordinary promotion: %q", headline)
	}
	if !strings.Contains(headline, "the design this needs has not landed") {
		t.Errorf("the headline does not say why the item stays open: %q", headline)
	}
	if strings.Contains(headline, "the item is closed") {
		t.Errorf("the headline claims a closure that did not happen: %q", headline)
	}
}
