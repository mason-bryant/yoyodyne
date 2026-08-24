package beads

import (
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/goal"
)

// The command that destroyed twelve attributions, refused before it runs. The
// spellings here are the ones the diagnosis found in the session transcripts,
// not invented ones: the flag written with an `=`, the flag written as two
// words, and the whole thing behind a `cd` into the repository.
func TestTheWholesaleNotesWriterIsRefusedHoweverItIsSpelled(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name    string
		command string
	}{
		{"the flag joined to its value", `bd update yoyodyne-ifd.45 --notes="Fresh evidence 2026-08-18: a turn failed outright."`},
		{"the flag and its value as two words", `bd update yoyodyne-ifd.45 --notes 'Fresh evidence.'`},
		{"reached through a path rather than the PATH", `/opt/homebrew/bin/bd update yoyodyne-ifd.45 --notes=replaced`},
		{"behind a change of directory", `cd /Users/mbryant/github/yoyodyne && bd update yoyodyne-ifd.45 --notes="replaced"`},
		{"after a command that succeeded", "bd show yoyodyne-ifd.45; bd update yoyodyne-ifd.45 --notes=replaced"},
		{"the flag named before the item", `bd update --notes="replaced" yoyodyne-ifd.45`},
		{"replacing the notes with nothing at all", `bd update yoyodyne-ifd.45 --notes=""`},
		{"the prefix with no statement after it", `bd update yoyodyne-ifd.45 --notes="Goal served:"`},
		{"spread across a continued line", "bd update yoyodyne-ifd.45 \\\n  --notes='replaced'"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			refusal := DestroyedAttribution(test.command)
			if refusal == "" {
				t.Fatalf("DestroyedAttribution(%q) allowed the writer", test.command)
			}
			// The refusal has to be actionable on its own: it names the item at
			// stake and the flag that does the same job without destroying
			// anything. An agent that is only told "no" runs the same command with
			// the quoting changed.
			if !strings.Contains(refusal, "yoyodyne-ifd.45") || !strings.Contains(refusal, appendNotesFlag) {
				t.Fatalf("DestroyedAttribution(%q) = %q, want it to name the item and %s", test.command, refusal, appendNotesFlag)
			}
		})
	}
}

// The narrower rule, which is the whole reason this refuses rather than bans:
// what must survive is the record, so a replacement carrying the record through
// destroys nothing and is allowed. It is also the way past a refusal for the one
// legitimate reason to replace notes, and a guard with no way past it is a guard
// somebody removes.
func TestAReplacementCarryingTheAttributionThroughIsAllowed(t *testing.T) {
	t.Parallel()

	autonomy := "Run development nearly autonomously."
	carried := `bd update yoyodyne-ifd.45 --notes="Admitted by the product manager.

` + goal.Note(autonomy) + `"`
	if refusal := DestroyedAttribution(carried); refusal != "" {
		t.Fatalf("a replacement carrying the attribution through was refused: %s", refusal)
	}
}

// What the escape hatch does not check, asserted so it is a stated bound rather
// than a surprise. The rule is decidable from the command line alone, so a
// `Goal served:` line the writer invented reads exactly like the item's own and
// passes. The refusal is required to say so, because the one path this guard
// actively teaches is the one place it must not overclaim: what catches a
// substitution is the witness the tracker holds, not this.
func TestTheEscapeHatchDoesNotVerifyTheStatementIsTheItemsOwn(t *testing.T) {
	t.Parallel()

	invented := `bd update yoyodyne-ifd.45 --notes="` + goal.Note("Something nobody ever attributed this to.") + `"`
	if refusal := DestroyedAttribution(invented); refusal != "" {
		t.Fatalf("the command-line-only rule was expected to allow an invented statement, got: %s", refusal)
	}
	refusal := DestroyedAttribution(`bd update yoyodyne-ifd.45 --notes="replaced"`)
	for _, stated := range []string{"not that it is the item's", "bd show yoyodyne-ifd.45"} {
		if !strings.Contains(refusal, stated) {
			t.Fatalf("the refusal does not state its own bound: %q is missing from %q", stated, refusal)
		}
	}
}

// Everything else an agent runs passes without a word. A guard that had an
// opinion about ordinary commands would be one somebody turns off, and the
// append spelling -- the one every refusal sends the writer to -- must never be
// read as the spelling being refused.
func TestOrdinaryCommandsAndTheAppendingWriterPassUnremarked(t *testing.T) {
	t.Parallel()

	for _, allowed := range []string{
		`bd update yoyodyne-ifd.45 --append-notes="what I did"`,
		`bd update yoyodyne-ifd.45 --status=open`,
		`bd create --title="A new item" --notes="Goal served: nothing yet"`,
		`bd show yoyodyne-ifd.45 --json`,
		`git commit -m "bd update x --notes=y is what broke it"`,
		`echo 'bd update x --notes=y'`,
		"go test ./...",
		"",
	} {
		if refusal := DestroyedAttribution(allowed); refusal != "" {
			t.Fatalf("DestroyedAttribution(%q) refused an ordinary command: %s", allowed, refusal)
		}
	}
}

// `bd create` is deliberately absent from the refusals above and asserted here:
// it writes notes onto an item that does not exist yet, so there is no
// attribution for it to destroy. Refusing it would refuse the harness's own
// admission path, which is how work acquires a goal in the first place.
func TestCreatingAnItemWithNotesIsNotAReplacement(t *testing.T) {
	t.Parallel()

	if refusal := DestroyedAttribution(`bd create --type=task --title="A new item" --notes="provenance"`); refusal != "" {
		t.Fatalf("creating an item was refused: %s", refusal)
	}
}
