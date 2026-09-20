package chat

import (
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/contextbundle"
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
