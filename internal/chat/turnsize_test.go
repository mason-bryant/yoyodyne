package chat

import (
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/contextbundle"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// systemPromptAllowance is what this test reserves inside MaxTurnInputBytes for
// the system prompt, which is the role's contract and its persona and is not
// bounded by a constant of its own. The whole of one is well under this: on
// 2026-09-20 a refused product-manager turn measured 1,076,731 bytes of system
// prompt and prompt together, of which the bundle was almost all, and the
// largest persona in this repository is 7 KiB.
const systemPromptAllowance = 128 << 10

// TestTheTurnBackstopSitsAboveWhatItBackstops is the regression test for the
// day the management tier locked itself out. MaxTurnInputBytes is a backstop
// over the assembled product context, and on 2026-09-20 the context's own bound
// was raised past it, so every turn in every management conversation was refused
// for carrying an ordinary bundle. Neither constant was wrong on its own; they
// were chosen in different packages and nothing compared them. This compares
// them.
func TestTheTurnBackstopSitsAboveWhatItBackstops(t *testing.T) {
	needed := contextbundle.MaxProductBytes + MaxOperatorMessageBytes + systemPromptAllowance
	if MaxTurnInputBytes < needed {
		t.Fatalf("a turn may carry %d bytes, but the product context alone may be %d, "+
			"an operator message %d, and the system prompt needs about %d: %d bytes short. "+
			"A conversation given a bundle at its bound would be refused every turn.",
			MaxTurnInputBytes, contextbundle.MaxProductBytes, MaxOperatorMessageBytes,
			systemPromptAllowance, needed-MaxTurnInputBytes)
	}
}

// TestThePendingPictureBoundSitsAboveTheBundleItKeeps compares the same two
// numbers at the other end. A re-read waits on disk between the turn that took
// it and the turn that delivers it, and the bound on what may wait there is
// stated in the state package, which sits below the one that assembles a
// bundle and cannot read its bound. A bound that drifted under it would refuse
// to keep an ordinary picture, which is the amplifier this was built to remove
// coming back as a write that fails instead of a read that repeats.
func TestThePendingPictureBoundSitsAboveTheBundleItKeeps(t *testing.T) {
	if runstate.MaxPendingPictureBytes < contextbundle.MaxProductBytes {
		t.Fatalf("a picture may wait as %d bytes beside the record, but an assembled product context may be %d: "+
			"a refresh of an ordinary bundle would be refused, and the re-read taken for it discarded.",
			runstate.MaxPendingPictureBytes, contextbundle.MaxProductBytes)
	}
}
