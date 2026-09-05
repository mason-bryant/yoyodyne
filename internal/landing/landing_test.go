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
// landings was claimed, in words that cannot be read as the other.
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
	unclaimed := landing.Claim{}
	if unclaimed.Describe() != "" {
		t.Errorf("Describe() invented a claim nobody made: %q", unclaimed.Describe())
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
}
