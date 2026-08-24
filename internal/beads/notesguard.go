package beads

// The writer that destroys an attribution, read in a command line before it
// runs rather than found in the wreckage afterwards.
//
// Nothing in this package destroys one: Update passes --append-notes, and
// Create passes --notes onto an item that does not exist yet. Every loss came
// from outside those paths -- `bd update <id> --notes="..."` typed into an
// agent session, twelve times across twelve items, each replacement taking the
// `Goal served:` line with it and leaving the item reading as work nobody ever
// attributed. The flag is the mechanism and the sessions are the writer, so
// this is the flag recognised where the session is still able to be stopped.
//
// What is refused is narrower than the flag. A replacement that carries an
// attribution through destroys nothing, and is allowed: the rule is that the
// record survives, not that one spelling is banned. It is also the escape hatch
// for the one legitimate reason to replace notes wholesale, which is why
// refusing outright was rejected -- a guard with no way past it is a guard
// somebody removes.
//
// The rule is decidable from the command line alone, deliberately. This runs in
// front of every shell command an agent takes, and a guard that asked the
// tracker what the item currently records would be a guard that waits on a
// locked database on every command. What that costs is a refusal on an item
// recording no goal, where the replacement would have destroyed nothing; what
// it buys is a guard that cannot itself become the reason a session stalls, and
// the refusal costs whoever meets it one flag.
//
// The same bound is what the escape hatch can and cannot check, and the refusal
// says so rather than implying otherwise. A command line carries a `Goal
// served:` line or it does not; whether that line is the one the item itself
// recorded is a fact about the item, and this never reads the item. So a
// replacement carrying some other statement passes here, and what catches that
// is the witness rather than this: the tracker still holds the statement that
// was written, `goals attribution` reads the notes against it, and the words to
// put back survive the substitution. This stops the loss that leaves nothing
// behind; the witness is what makes every other one recoverable.

import (
	"fmt"
	"path"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/goal"
)

// notesFlag replaces an item's notes wholesale, and appendNotesFlag adds to
// them. Both spellings are named here because the refusal is about the
// difference between them: the second is what a refusal sends the writer to,
// and it must not be read as the first.
const (
	notesFlag       = "--notes"
	appendNotesFlag = "--append-notes"
)

// DestroyedAttribution says why a shell command line must not run: some command
// in it replaces a work item's notes wholesale and carries no goal through, so
// an attribution recorded on that item would be gone with nothing left saying it
// was ever there. It is empty for every other command line, which is nearly all
// of them.
//
// The first such command decides the answer. A line carrying two of them is one
// refusal to act on, not two, and the writer meets the second only after
// rewriting the first.
func DestroyedAttribution(command string) string {
	for _, words := range simpleCommands(command) {
		replaced, notes, replaces := notesReplacement(words)
		if !replaces {
			continue
		}
		if _, records := goal.NamedIn(notes); records {
			continue
		}
		return fmt.Sprintf("`bd update %s %s` replaces that item's notes rather than adding to them, "+
			"and an item's notes are where the goal it serves is recorded, on a `%s` line. "+
			"A replacement that does not carry that line through destroys the attribution, and the item afterwards "+
			"reads as work nobody ever attributed -- which has already happened, to eighteen items across two occasions. "+
			"Use `%s` instead: it adds to the notes and takes nothing away. "+
			"If the notes really do have to be replaced, carry the item's own `%s ...` line into the replacement "+
			"verbatim -- read it back with `bd show %s` first -- and this will allow it. It checks only that such a "+
			"line is there, not that it is the item's, so a statement invented here is a substitution nothing stops.",
			replaced, notesFlag, goal.AttributionPrefix, appendNotesFlag, goal.AttributionPrefix, replaced)
	}
	return ""
}

// notesReplacement reads one command's words as a wholesale notes replacement:
// the item whose notes it replaces, what it would replace them with, and
// whether it is one at all.
//
// The item is what the command names first that is not a flag, and it is only
// ever used to say which item is at stake. A command that names none -- or
// whose first bare word belongs to some other flag -- is still refused, because
// what decides that is the replacement rather than the naming.
func notesReplacement(words []string) (string, string, bool) {
	if len(words) < 2 || !invokesBd(words[0]) || words[1] != "update" {
		return "", "", false
	}
	replaced, notes, replaces := "", "", false
	for index := 2; index < len(words); index++ {
		word := words[index]
		switch {
		case word == notesFlag:
			// The replacement written as two words. A trailing `--notes` with
			// nothing after it replaces the notes with nothing, which is the same
			// destruction spelled shorter.
			replaces = true
			if index+1 < len(words) {
				index++
				notes = words[index]
			}
		case strings.HasPrefix(word, notesFlag+"="):
			notes, replaces = strings.TrimPrefix(word, notesFlag+"="), true
		case strings.HasPrefix(word, "-"):
			// Every other flag, `--append-notes` among them, which is the whole
			// point: it is not this one.
		case replaced == "":
			replaced = word
		}
	}
	if replaced == "" {
		replaced = "<id>"
	}
	return replaced, notes, replaces
}

// invokesBd reports a word that runs the tracker, however it was reached: bare
// on the PATH, or by a path to it.
func invokesBd(word string) bool {
	return path.Base(strings.ReplaceAll(word, `\`, "/")) == "bd"
}

// simpleCommands splits a shell command line into the words of each command in
// it. Reading only the first command would miss `cd repo && bd update ...`,
// which is how these were actually written.
//
// It is a reading of the line rather than a shell. Quoting is honoured, because
// the replacement text is always quoted and a reading that ignored quotes would
// see a sentence as a hundred flags; substitutions are not expanded and control
// structures are not understood, because neither is how a routine writer types
// this. That bound is the honest one for what this is: it catches the command
// somebody meant to run, and a command line assembled so this cannot see it is
// not the accident it exists to prevent.
func simpleCommands(command string) [][]string {
	var commands [][]string
	var words []string
	var word strings.Builder
	// started tells an empty word that was quoted -- `--notes=""` -- from no
	// word at all, which is the difference between replacing the notes with
	// nothing and not replacing them.
	started := false
	quote := rune(0)
	flush := func() {
		if started {
			words = append(words, word.String())
			word.Reset()
			started = false
		}
	}
	endCommand := func() {
		flush()
		if len(words) > 0 {
			commands = append(commands, words)
			words = nil
		}
	}
	runes := []rune(command)
	for index := 0; index < len(runes); index++ {
		character := runes[index]
		switch {
		case quote == '\'':
			// Single quotes are literal, backslash included, so the only thing
			// that ends them is the closing quote.
			if character == '\'' {
				quote = 0
				continue
			}
			word.WriteRune(character)
			started = true
		case quote == '"':
			if character == '"' {
				quote = 0
				continue
			}
			if character == '\\' && index+1 < len(runes) && strings.ContainsRune("\"\\$`\n", runes[index+1]) {
				index++
				if runes[index] == '\n' {
					continue
				}
				word.WriteRune(runes[index])
				started = true
				continue
			}
			word.WriteRune(character)
			started = true
		case character == '\'' || character == '"':
			quote = character
			started = true
		case character == '\\':
			if index+1 < len(runes) {
				index++
				if runes[index] == '\n' {
					continue
				}
				word.WriteRune(runes[index])
				started = true
			}
		case strings.ContainsRune(";&|\n()", character):
			endCommand()
		case character == ' ' || character == '\t' || character == '\r':
			flush()
		default:
			word.WriteRune(character)
			started = true
		}
	}
	endCommand()
	return commands
}
