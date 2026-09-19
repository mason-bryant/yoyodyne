package beads

// The writer that moves a status backwards and leaves nothing saying what moved
// it, read in a command line before it runs.
//
// Nothing in this package moves a status without an account: Block, Unblock,
// Reopen, and the claim's own stale-block correction each pass `--status` and
// `--append-notes` in one invocation, so the item afterwards says what moved it
// and who. The rewrites of 2026-09-18 came from outside those paths -- `bd
// update <id> --status=open` on its own, run ahead of a triage verb by a script
// that expected the verb to do the work and the status to be harmless. The verb
// refused, as it should have, and the status had already moved: two items
// closed on confirmed merges read as open, two escalations read as released,
// and none of the four carried a word about it
// (`docs/diagnoses/yoyodyne-ifd-392-status-rewrites-by-the-carry-out-queue.md`).
//
// The rule is that a status set from the command line carries an appended note,
// whichever way it moves. Which way it moves is a fact about the item, and this
// never reads the item, for the same reason the notes guard beside it does not:
// it stands in front of every shell command an agent session takes, and a guard
// that consulted the tracker would wait on a locked database on every one of
// them. What that costs is a note on a forward move that needed none; what it
// buys is that a backward move can never be silent, because the account is on
// the same command line as the move and the guard reads the line.
//
// `--claim` is not refused. It is the forward move every run makes, bd records
// the claim on the item itself, and refusing it would refuse the harness's own
// path onto every item. `bd close` carries its own `--reason` and is not this
// command. `bd reopen` is the backward move spelled as a verb, with no note to
// carry, and is sent to the spelling that carries one.

import (
	"fmt"
	"strings"
)

// statusFlag sets an item's status from the command line.
const statusFlag = "--status"

// UnaccountedStatusMove says why a shell command line must not run: some
// command in it sets a work item's status and appends no note saying what moved
// it, so the status would change and the item would carry no account of the
// change. It is empty for every other command line.
//
// The first such command decides the answer, as in DestroyedAttribution.
func UnaccountedStatusMove(command string) string {
	for _, words := range simpleCommands(command) {
		if reopened, ok := reopenVerb(words); ok {
			return fmt.Sprintf("`bd reopen %s` moves that item's status backwards and records nothing about why. "+
				"A status is never moved backwards without a note naming what moved it: run "+
				"`bd update %s --status=open --append-notes=\"...\"` with the note saying what reopened it and on whose decision. "+
				"An item closed because its change merged is not reopened at all; what is on the target branch does not come back.",
				reopened, reopened)
		}
		moved, status, appended, moves := statusMove(words)
		if !moves || appended {
			continue
		}
		return fmt.Sprintf("`bd update %s --status=%s` moves that item's status and appends no note saying what moved it. "+
			"A status is never moved backwards without a note naming what moved it -- four items were moved this way on 2026-09-18, "+
			"two of them closed on confirmed merges and two escalated to a person, and nothing on any of them said so. "+
			"Put the account on the same command: `bd update %s --status=%s --append-notes=\"...\"`, saying what moved it and on whose decision. "+
			"An item closed because its change merged is not reopened at all; what is on the target branch does not come back. "+
			"This decides from the command line alone and never reads the item, so it asks for a note on every status set here, "+
			"forward moves included.",
			moved, status, moved, status)
	}
	return ""
}

// statusMove reads one command's words as a status set from the command line:
// the item whose status it sets, the status, whether a note is appended beside
// it, and whether it sets a status at all.
//
// The item is what the command names first that is not a flag, as in
// notesReplacement, and is only ever used to say which item is at stake.
func statusMove(words []string) (string, string, bool, bool) {
	if len(words) < 2 || !invokesBd(words[0]) || words[1] != "update" {
		return "", "", false, false
	}
	moved, status, appended, moves := "", "", false, false
	for index := 2; index < len(words); index++ {
		word := words[index]
		switch {
		case word == statusFlag:
			moves = true
			if index+1 < len(words) {
				index++
				status = words[index]
			}
		case strings.HasPrefix(word, statusFlag+"="):
			status, moves = strings.TrimPrefix(word, statusFlag+"="), true
		case word == appendNotesFlag:
			// The account written as two words. A trailing flag with nothing
			// after it appends nothing, which is no account.
			if index+1 < len(words) {
				index++
				appended = appended || strings.TrimSpace(words[index]) != ""
			}
		case strings.HasPrefix(word, appendNotesFlag+"="):
			appended = appended || strings.TrimSpace(strings.TrimPrefix(word, appendNotesFlag+"=")) != ""
		case strings.HasPrefix(word, "-"):
			// Every other flag. `--claim` among them: it is not a status set
			// from the command line, and bd records the claim itself.
		case moved == "":
			moved = word
		}
	}
	if moved == "" {
		moved = "<id>"
	}
	if status == "" {
		status = "<status>"
	}
	return moved, status, appended, moves
}

// reopenVerb reads one command's words as `bd reopen <id>`, and names the item.
func reopenVerb(words []string) (string, bool) {
	if len(words) < 2 || !invokesBd(words[0]) || words[1] != "reopen" {
		return "", false
	}
	for _, word := range words[2:] {
		if !strings.HasPrefix(word, "-") {
			return word, true
		}
	}
	return "<id>", true
}
