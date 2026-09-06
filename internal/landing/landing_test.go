package landing_test

import (
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/landing"
)

// The whole of what this channel decides: a reply that claims nothing closes its
// item, and a reply that claims evidence does not. Both directions are checked
// here because getting either one wrong is a defect nobody sees — a false
// closure retires a marker, and a false refusal to close strands finished work.
func TestOnlyAClaimOfEvidenceWithholdsTheClosure(t *testing.T) {
	t.Parallel()

	for _, claimed := range []struct {
		name       string
		reply      string
		discharges bool
		made       bool
	}{
		{
			name:       "a reply that claims nothing",
			reply:      "I implemented the conversion and the checks pass.",
			discharges: true,
		},
		{
			name: "a reply that claims the ordinary landing",
			reply: "Done.\n\n```yoyodyne-landing\n" +
				`{"outcome":"discharged","why":"the conversion is implemented and the suite covers it"}` + "\n```\n",
			discharges: true,
			made:       true,
		},
		{
			name: "a reply that claims evidence",
			reply: "This is not doable yet.\n\n```yoyodyne-landing\n" +
				`{"outcome":"evidence","why":"the management-conversion design has not landed, so what this run landed is the diagnosis"}` + "\n```\n",
			discharges: false,
			made:       true,
		},
	} {
		t.Run(claimed.name, func(t *testing.T) {
			t.Parallel()

			rest, claim, err := landing.Extract(claimed.reply)
			if err != nil {
				t.Fatalf("Extract() error = %v", err)
			}
			if claim.Made() != claimed.made {
				t.Errorf("Made() = %t, want %t", claim.Made(), claimed.made)
			}
			if claim.Discharges() != claimed.discharges {
				t.Errorf("Discharges() = %t, want %t", claim.Discharges(), claimed.discharges)
			}
			// The block never survives into the reply the run records as its
			// summary: it is protocol rather than prose, and the operator reads the
			// summary.
			if strings.Contains(rest, "yoyodyne-landing") || strings.Contains(rest, `"outcome"`) {
				t.Errorf("Extract() left the block in the reply: %q", rest)
			}
		})
	}
}

// A claim the harness cannot read must not be treated as a claim that was never
// made. The developer wrote a block, which means it was trying to say something
// about whether the item closes, and the reply's prose is the run's evidence
// either way.
func TestAnUnreadableClaimIsRefusedAndCostsTheReplyNothing(t *testing.T) {
	t.Parallel()

	for _, broken := range []struct {
		name  string
		block string
	}{
		{name: "an outcome outside the vocabulary", block: `{"outcome":"partial","why":"some of it"}`},
		{name: "no account of the claim", block: `{"outcome":"evidence"}`},
		{name: "a field the schema does not define", block: `{"outcome":"evidence","why":"because","closes":false}`},
		{name: "an empty block", block: ""},
		{name: "trailing content", block: `{"outcome":"evidence","why":"because"} {"outcome":"discharged"}`},
	} {
		t.Run(broken.name, func(t *testing.T) {
			t.Parallel()

			reply := "The account of the work.\n\n```yoyodyne-landing\n" + broken.block + "\n```\n"
			rest, claim, err := landing.Extract(reply)
			if err == nil {
				t.Fatalf("Extract() accepted %q as a claim: %+v", broken.block, claim)
			}
			if claim.Made() {
				t.Errorf("Extract() returned a claim alongside its refusal: %+v", claim)
			}
			if !strings.Contains(rest, "The account of the work.") {
				t.Errorf("a refused claim cost the reply its prose: %q", rest)
			}
		})
	}
}

// A reply carrying two claims is refused rather than decided from the first one.
// Which of them the item closes on is not something this package may pick.
func TestASecondClaimIsRefusedRatherThanIgnored(t *testing.T) {
	t.Parallel()

	reply := "```yoyodyne-landing\n" + `{"outcome":"evidence","why":"the first"}` + "\n```\n" +
		"```yoyodyne-landing\n" + `{"outcome":"discharged","why":"the second"}` + "\n```\n"
	if _, claim, err := landing.Extract(reply); err == nil {
		t.Fatalf("Extract() decided between two claims: %+v", claim)
	}
}

// The block is bounded so nothing can push a document into the field a work
// item's notes are written from.
func TestAnOversizedClaimIsRefused(t *testing.T) {
	t.Parallel()

	oversized := `{"outcome":"evidence","why":"` + strings.Repeat("x", landing.MaxWhyBytes+1) + `"}`
	if _, err := landing.Decode(oversized); err == nil {
		t.Fatal("Decode() accepted a why past its limit")
	}
	if _, err := landing.Decode(strings.Repeat("x", landing.MaxBlockBytes+1)); err == nil {
		t.Fatal("Decode() accepted a block past its limit")
	}
}

// What a reviewer and a work item's notes are shown has to say which of the two
// landings was claimed, in words that cannot be read as the other — and nothing
// else. Both audiences read this sentence and they want opposite things from it:
// one is being asked to decide, and the other is a record somebody consults after
// every decision has been taken.
func TestTheDescriptionSaysWhichLandingWasClaimed(t *testing.T) {
	t.Parallel()

	evidence := landing.Claim{Outcome: landing.OutcomeEvidence, Why: "the design it needs has not landed"}.Describe()
	if !strings.Contains(evidence, "does not discharge") {
		t.Errorf("Describe() of an evidence claim does not say the item stays open: %q", evidence)
	}
	if !strings.Contains(evidence, "the design it needs has not landed") {
		t.Errorf("Describe() dropped the developer's account: %q", evidence)
	}
	discharged := landing.Claim{Outcome: landing.OutcomeDischarged, Why: "the work is done"}.Describe()
	if strings.Contains(discharged, "does not discharge") {
		t.Errorf("Describe() of a discharging claim reads as the other one: %q", discharged)
	}
	// The claim nobody made is the one that closed yoyodyne-ifd.284 against a
	// diagnosis, and it used to describe nothing at all — so the reviewer, the one
	// reader that can tell a diagnosis from an implementation, was shown nothing on
	// nearly every run. It is described as what it is: a discharging landing that
	// no reply carried.
	unclaimed := landing.Claim{}.Describe()
	if !strings.Contains(unclaimed, "claimed no landing outcome") {
		t.Errorf("Describe() does not say the claim was never made: %q", unclaimed)
	}
	if strings.Contains(unclaimed, "does not discharge") {
		t.Errorf("Describe() of the default reads as the landing that leaves the item open: %q", unclaimed)
	}
	// And no description directs anybody to do anything. What a reviewer does with
	// a landing is said where the reviewer is prompted; the same words reach a work
	// item's notes, where an instruction to ask for changes is addressed to nobody
	// who can.
	for _, described := range []string{unclaimed, discharged, evidence} {
		for _, directed := range []string{"approve", "Ask for changes", "your summary"} {
			if strings.Contains(described, directed) {
				t.Errorf("Describe() directs its reader (%q): %q", directed, described)
			}
		}
	}
}

// An undischarged landing leaves its item in one of exactly two places, and the
// default is the parking. There is deliberately no third: an item put back with
// nothing marking it is one autonomous selection picks again immediately, for
// another run and another diagnosis of the impediment this run just diagnosed.
func TestAnUndischargedClaimParksUnlessItNamesTheImpediment(t *testing.T) {
	t.Parallel()

	parking := "```yoyodyne-landing\n" +
		`{"outcome":"evidence","why":"the design this needs has not landed"}` + "\n```\n"
	_, parked, err := landing.Extract(parking)
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if !parked.Parks() || parked.Impediment() != "" {
		t.Errorf("a claim naming no impediment did not take the parking default: %+v", parked)
	}

	waiting := "```yoyodyne-landing\n" +
		`{"outcome":"evidence","why":"the design this needs has not landed","blocked_by":" yoyodyne-ifd.209.25 "}` + "\n```\n"
	_, open, err := landing.Extract(waiting)
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if open.Parks() {
		t.Error("a claim that named its impediment was parked anyway")
	}
	if open.Impediment() != "yoyodyne-ifd.209.25" {
		t.Errorf("Impediment() = %q, want the item it named", open.Impediment())
	}
	// A discharging landing has nothing to wait for, so a claim carrying both is
	// two statements that cannot both be true rather than one to pick from.
	discharging := `{"outcome":"discharged","why":"done","blocked_by":"yoyodyne-ifd.209.25"}`
	if _, err := landing.Decode(discharging); err == nil ||
		!strings.Contains(err.Error(), "does not discharge") {
		t.Fatalf("Decode() accepted a discharging claim that waits on something: %v", err)
	}
}

// The marker is refused where the claim is read rather than carried as far as the
// write that would refuse it. A run whose change is already integrated must not
// fail its settlement over a marker that was never an identifier.
func TestAMarkerThatIsNotAWorkItemIdentifierIsRefused(t *testing.T) {
	t.Parallel()

	for _, refused := range []struct {
		name   string
		marker string
	}{
		{name: "prose written where an identifier goes", marker: "the design has not landed yet"},
		{name: "an argument smuggled into the identifier", marker: "--status=closed"},
		{name: "an identifier past its bound", marker: strings.Repeat("y", landing.MaxBlockedByBytes+1)},
	} {
		t.Run(refused.name, func(t *testing.T) {
			t.Parallel()

			payload := `{"outcome":"evidence","why":"not doable yet","blocked_by":"` + refused.marker + `"}`
			if _, err := landing.Decode(payload); err == nil {
				t.Fatalf("Decode() accepted %q as the item to wait on", refused.marker)
			}
		})
	}
}

// The description is what the reviewer and the item's notes are shown, and the
// two dispositions have to be tellable apart there: one waits for a person and
// the other releases itself.
func TestTheDescriptionSaysWhereAnUndischargedItemWasLeft(t *testing.T) {
	t.Parallel()

	parked := landing.Claim{Outcome: landing.OutcomeEvidence, Why: "not doable yet"}.Describe()
	if !strings.Contains(parked, "parked") {
		t.Errorf("Describe() of the parking default does not say the item is parked: %q", parked)
	}
	waiting := landing.Claim{
		Outcome:   landing.OutcomeEvidence,
		Why:       "not doable yet",
		BlockedBy: "yoyodyne-ifd.209.25",
	}.Describe()
	if strings.Contains(waiting, "parked") || !strings.Contains(waiting, "yoyodyne-ifd.209.25") {
		t.Errorf("Describe() of a leave-open claim does not name what it waits on: %q", waiting)
	}
}

// The vocabulary an agent is told about and the vocabulary the decoder accepts
// are the same one. A contract naming a value the decoder refuses teaches every
// developer to write a claim that is thrown away.
func TestTheContractNamesExactlyTheVocabularyTheDecoderAccepts(t *testing.T) {
	t.Parallel()

	for _, outcome := range landing.Outcomes() {
		if !strings.Contains(landing.Contract, `"`+string(outcome)+`"`) {
			t.Errorf("the contract never names the %q outcome the decoder accepts", outcome)
		}
	}
	if !strings.Contains(landing.Contract, landing.Fence) {
		t.Error("the contract does not show the fence the block has to open with")
	}
	// The marker is the only alternative to the parking, so a contract that never
	// names it leaves every developer taking a default it was not told about.
	if !strings.Contains(landing.Contract, `"blocked_by"`) {
		t.Error("the contract never names the marker that leaves an item open")
	}
}
