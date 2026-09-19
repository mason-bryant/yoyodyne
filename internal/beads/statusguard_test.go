package beads

import (
	"strings"
	"testing"
)

// The 297 shape, replayed at the writer. yoyodyne-ifd.297 was closed on
// 2026-09-15 by the run that integrated it, its pull request was confirmed
// merged on the forge on 2026-09-16, and on 2026-09-18 the command below ran
// against it -- ahead of a re-run verb that then refused, correctly, because the
// stoppage had already been re-run. The refusal cost nothing; the status had
// already moved, and nothing on the item said so. The spellings here are the
// script's own line and the ways the same command is otherwise typed.
func TestABareStatusMoveIsRefusedHoweverItIsSpelled(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name    string
		command string
	}{
		{"the line the carry-out queue ran", `bd update "yoyodyne-ifd.297" --status=open >/dev/null 2>&1`},
		{"the flag and its value as two words", `bd update yoyodyne-ifd.297 --status open`},
		{"reached through a path rather than the PATH", `/usr/local/bin/bd update yoyodyne-ifd.297 --status=open`},
		{"behind a change of directory", `cd /Users/mbryant/github/yoyodyne && bd update yoyodyne-ifd.297 --status=open`},
		{"ahead of the verb that was meant to do the work", `bd update yoyodyne-ifd.297 --status=open; bin/yoyo triage rerun run-d8dcac18f57e41b2893750740b3ca287`},
		{"the flag named before the item", `bd update --status=open yoyodyne-ifd.297`},
		{"a move to blocked with no reason", `bd update yoyodyne-ifd.297 --status=blocked`},
		{"a forward move, because the direction is not readable here", `bd update yoyodyne-ifd.297 --status=in_progress`},
		{"an appended note with nothing in it", `bd update yoyodyne-ifd.297 --status=open --append-notes=""`},
		{"the appending flag trailing with nothing after it", `bd update yoyodyne-ifd.297 --status=open --append-notes`},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			refusal := UnaccountedStatusMove(test.command)
			if refusal == "" {
				t.Fatalf("UnaccountedStatusMove(%q) allowed the writer", test.command)
			}
			// The refusal names the item and the spelling that carries an
			// account, so the writer's next command is the accounted one rather
			// than the same one re-quoted.
			if !strings.Contains(refusal, "yoyodyne-ifd.297") || !strings.Contains(refusal, appendNotesFlag) {
				t.Fatalf("UnaccountedStatusMove(%q) = %q, want it to name the item and %s", test.command, refusal, appendNotesFlag)
			}
		})
	}
}

// The verb spelling of the same move. Whether this tracker version carries the
// verb at all is not something the guard reads; a verb that does not exist is
// refused at no cost, and one that does is sent to the spelling that carries a
// note.
func TestTheReopenVerbIsSentToTheSpellingThatCarriesANote(t *testing.T) {
	t.Parallel()

	refusal := UnaccountedStatusMove(`bd reopen yoyodyne-ifd.349`)
	if refusal == "" {
		t.Fatal("bd reopen was allowed")
	}
	if !strings.Contains(refusal, "yoyodyne-ifd.349") || !strings.Contains(refusal, "--status=open --append-notes") {
		t.Fatalf("refusal = %q, want it to name the item and the accounted spelling", refusal)
	}
}

// The accounted move is what every refusal sends the writer to, and it is what
// the harness's own writers run, so it must never be read as the thing refused:
// Block, Unblock, Reopen, and the claim's stale-block correction each set the
// status and append the account in one invocation.
func TestAStatusMoveCarryingItsAccountIsAllowed(t *testing.T) {
	t.Parallel()

	for _, allowed := range []string{
		`bd update yoyodyne-ifd.272 --status=open --append-notes="Released for the repair the development manager handed back at turn 529."`,
		`bd update yoyodyne-ifd.272 --status=blocked --append-notes="Escalated to the operator by triage." --json`,
		`bd update yoyodyne-ifd.272 --append-notes="what moved it" --status=open`,
		`bd update yoyodyne-ifd.272 --status open --append-notes 'what moved it'`,
	} {
		if refusal := UnaccountedStatusMove(allowed); refusal != "" {
			t.Fatalf("UnaccountedStatusMove(%q) refused the accounted move: %s", allowed, refusal)
		}
	}
}

// Everything else passes without a word, and the two neighbours of this rule
// most of all: the claim is the forward move every run makes and bd records it
// on the item itself, and `--status` on a listing reads rather than writes.
func TestOrdinaryCommandsAndTheClaimPassUnremarked(t *testing.T) {
	t.Parallel()

	for _, allowed := range []string{
		`bd update yoyodyne-ifd.45 --claim --json`,
		`bd update yoyodyne-ifd.45 --append-notes="what I did"`,
		`bd list --status=open --json`,
		`bd list --status open`,
		`bd close yoyodyne-ifd.45 --reason="landed on main"`,
		`bd show yoyodyne-ifd.45 --json`,
		`bd ready --json`,
		`git commit -m "bd update x --status=open is what broke it"`,
		`echo 'bd update x --status=open'`,
		"go test ./...",
		"",
	} {
		if refusal := UnaccountedStatusMove(allowed); refusal != "" {
			t.Fatalf("UnaccountedStatusMove(%q) refused an ordinary command: %s", allowed, refusal)
		}
	}
}
