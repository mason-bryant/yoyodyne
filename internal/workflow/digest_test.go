package workflow

import (
	"strings"
	"testing"
)

// deliveryDigest is the digest of testdata/delivery.yaml, written down rather
// than derived.
//
// A test that only compared one computed digest to another would pass just as
// happily if the whole scheme changed underneath it, and an instance pinned to a
// digest before that change would silently stop matching the definition it is
// running. So the expected value is a constant: it fails when the canonical form,
// the hash, or the fixture changes, and any of those changing is something
// somebody has to decide rather than discover.
const deliveryDigest = "wf-6bf036bbad1750f2f30def0303532bba930e6b968d8bd13c736dfb24e205b4d9"

func TestTheDigestOfAFixtureIsWhatItHasAlwaysBeen(t *testing.T) {
	t.Parallel()

	if digest := loadFixture(t, "delivery.yaml").Digest(); digest != deliveryDigest {
		t.Errorf("Digest() = %q, want %q; if the canonical form was meant to change, this constant is the decision to change with it", digest, deliveryDigest)
	}
}

// TestTheDigestIsOfWhatTheDefinitionSaysAndNotOfHowItWasWritten is the property
// pinning depends on. Two people writing one workflow down differently have
// written one workflow, and an instance pinned to the digest of either is
// pinned to the same sequence.
func TestTheDigestIsOfWhatTheDefinitionSaysAndNotOfHowItWasWritten(t *testing.T) {
	t.Parallel()

	written := loadFixture(t, "delivery.yaml")
	rewritten := loadFixture(t, "delivery-rewritten.yaml")
	if written.Digest() != rewritten.Digest() {
		t.Errorf("the same workflow written twice digests as %q and %q", written.Digest(), rewritten.Digest())
	}
	if string(written.Canonical()) != string(rewritten.Canonical()) {
		t.Errorf("the canonical forms differ:\n%s\n%s", written.Canonical(), rewritten.Canonical())
	}
}

// TestChangingWhatTheDefinitionSaysChangesTheDigest is the other half of it: a
// digest that did not move when the sequence did would pin an instance to
// nothing.
func TestChangingWhatTheDefinitionSaysChangesTheDigest(t *testing.T) {
	t.Parallel()

	written := loadFixture(t, "delivery.yaml")
	amended := loadFixture(t, "delivery-amended.yaml")
	if written.Digest() == amended.Digest() {
		t.Errorf("a definition with a transition changed digests as %q, the same as the one it changed", amended.Digest())
	}
	if !strings.HasPrefix(amended.Digest(), DigestPrefix) {
		t.Errorf("Digest() = %q, want it to say what it identifies with the %q prefix", amended.Digest(), DigestPrefix)
	}
}

func TestTheDigestIsTheSameEveryTimeItIsTaken(t *testing.T) {
	t.Parallel()

	first := loadFixture(t, "delivery.yaml")
	second := loadFixture(t, "delivery.yaml")
	if first.Digest() != second.Digest() {
		t.Errorf("one fixture digested as %q and then as %q", first.Digest(), second.Digest())
	}
}

// TestAValidatedDefinitionCannotBeChangedBehindItsDigest is why Validated holds
// its own copy and hands out copies. An instance pinned to a digest that stopped
// describing what it is running is the failure the type exists to prevent, and a
// shared map is all it would take.
func TestAValidatedDefinitionCannotBeChangedBehindItsDigest(t *testing.T) {
	t.Parallel()

	validated := loadFixture(t, "delivery.yaml")
	digested := validated.Digest()

	handedOut := validated.Definition()
	handedOut.ID = "something-else"
	handedOut.States["review"].On["approved"] = "abandoned"
	delete(handedOut.Terminals, "delivered")

	if validated.Digest() != digested {
		t.Errorf("Digest() moved to %q after a caller changed what Definition() handed it", validated.Digest())
	}
	unchanged := validated.Definition()
	if unchanged.ID != "delivery" {
		t.Errorf("Definition().ID = %q after a caller changed an earlier copy", unchanged.ID)
	}
	if destination := unchanged.States["review"].On["approved"]; destination != "integrate" {
		t.Errorf("an approved review now goes to %q after a caller changed an earlier copy", destination)
	}
	if _, ends := unchanged.Terminals["delivered"]; !ends {
		t.Error("a terminal a caller deleted from an earlier copy is gone from the validated definition")
	}
}
