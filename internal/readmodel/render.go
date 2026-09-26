package readmodel

// The four lines, as they are printed.
//
// This is the one place the ratified format is written down. It is here rather
// than in the CLI because the format is the contract: the same four lines are
// printed in a terminal and said in a channel, and two renderings of one
// standing is exactly the disagreement an operator would then have to
// adjudicate.
//
// Three rules hold every line, and each of them exists because its opposite
// happened. Every line is always printed, so silence never has to be
// interpreted. A line with nothing in it says "nothing" in words, because a
// blank reads as a bug in the printing rather than as an empty state. A line
// whose source could not be read says so instead of saying "nothing", because a
// confident emptiness assembled from a file nobody could open is the worst
// answer this could give.

import (
	"fmt"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// maxListed bounds how many entries one line names before it counts the rest.
// The counts stay exact either way: what a status owes a reader is what is
// happening, not an export of the queue.
const maxListed = 10

// partialRead opens the line a count carries when its source answered in part.
// It is a constant because two things read it: the renderers that write it, and
// the brief rendering that keeps it while dropping every other indented line.
const partialRead = "  not fully read: "

// Render is the four lines. It is deterministic, so the same standing always
// prints the same text, and it always returns exactly four labelled lines with
// whatever they carry indented under them.
//
// One thing can come above them, and only one: the harness being paused on the
// provider's usage window. It is a banner rather than a fifth line — the four
// are unchanged and are still all printed every time — and it is there because
// the operator asked that the cause be the first words of any message that
// reaches him when the system is paused on a window. Nothing else is ever put
// above them: a state that will not render into the four is a bug in the state.
func (s Standing) Render() string {
	if s.Paused == "" {
		return s.RenderLines()
	}
	return s.Paused + "\n" + s.RenderLines()
}

// RenderLines is the four lines without the banner, for the one caller that has
// already said what the banner says: the channel's own message about the
// provider's usage window, which opens with that sentence and then shows where
// the harness stands underneath it. Saying it twice in one message is repetition
// rather than emphasis, and every other caller wants Render.
func (s Standing) RenderLines() string {
	var rendered strings.Builder
	rendered.WriteString(s.renderRunning())
	rendered.WriteString(s.renderWorking())
	rendered.WriteString(s.renderNotStartable())
	rendered.WriteString(s.renderNeedsHuman())
	return rendered.String()
}

// RenderBrief is the same four lines with the queues counted and not listed, and
// it is what a message nobody asked for carries.
//
// It exists because pushing and answering are different acts. A person at a
// terminal typed `yoyo status`, and what they want is every run with its phase,
// its age and what it has spent — the whole point of having asked. A message
// that arrives on its own hour after hour is read by somebody who did not ask
// anything, and the operator's standard for those is an exec's: brief, and about
// what needs a decision. Under it an enumerated queue is a screen of detail
// nobody requested, repeated every hour, in front of the one sentence that says
// the line has stopped.
//
// It is not a second rendering of the standing. The labels, the words, the
// counts and the stated absences are the ones above and come from the same
// derivation; what it drops is the entries under a line and nothing else, so the
// two can differ in how much they say and never in what they say. The banner is
// carried for the reason Render carries it: the operator asked that a pause name
// its cause in the first words of any message that reaches him.
func (s Standing) RenderBrief() string {
	if s.Paused == "" {
		return s.RenderBriefLines()
	}
	return s.Paused + "\n" + s.RenderBriefLines()
}

// RenderBriefLines is the brief four lines without the banner, for the caller
// that has already said what the banner says. It stands to RenderBrief exactly as
// RenderLines stands to Render, and for the same one caller: the channel's
// message about the provider's usage window opens with that sentence.
func (s Standing) RenderBriefLines() string {
	var rendered strings.Builder
	rendered.WriteString(brief(s.renderRunning()))
	rendered.WriteString(brief(s.renderWorking()))
	rendered.WriteString(brief(s.renderNotStartable()))
	rendered.WriteString(brief(s.renderNeedsHuman()))
	return rendered.String()
}

// brief is one rendered line with the entries under it dropped. It reads the
// full rendering rather than re-deriving a short one, which is what makes the
// two renderings incapable of disagreeing: the line a reader sees here is
// character for character the line the terminal prints, and what is removed is
// only what was indented under it.
//
// The trailing colon goes with them. "Running (2 developer runs):" promises a
// list that is not there, and a promise a message does not keep reads as
// something having been lost rather than as something having been left out.
//
// One indented line survives, and it is the one that is not an entry: a count
// assembled from a source that could not be fully read says so under itself, and
// a brief rendering that dropped the caveat and kept the number would be the
// confident emptiness this format exists to refuse.
func brief(rendered string) string {
	lines := strings.SplitAfter(rendered, "\n")
	head, rest := lines[0], lines[1:]
	kept := strings.TrimSuffix(strings.TrimSuffix(head, "\n"), ":") + "\n"
	for _, line := range rest {
		if strings.HasPrefix(line, partialRead) {
			kept += line
		}
	}
	return kept
}

func (s Standing) renderRunning() string {
	if s.RunningProblem != "" {
		return unreadable("Running", s.RunningProblem)
	}
	var rendered strings.Builder
	// A dispatch waiting out the tracker before it claims anything holds a slot
	// with no run record, so it is said in the head of the line as well as under
	// it: the brief rendering the channel carries keeps only the head, and a slot
	// held for two hours by a line that read "nothing" is the hang this is here to
	// tell apart from a wait.
	waiting := ""
	if len(s.Dispatching) > 0 {
		waiting = dispatches(len(s.Dispatching)) + " waiting out a tracker failure before claiming anything"
	}
	switch {
	case len(s.Running) == 0 && waiting == "":
		rendered.WriteString("Running: nothing\n")
	case len(s.Running) == 0:
		fmt.Fprintf(&rendered, "Running: no run yet, and %s:\n", waiting)
	case waiting == "":
		fmt.Fprintf(&rendered, "Running (%s):\n", count(len(s.Running), "developer run"))
	default:
		fmt.Fprintf(&rendered, "Running (%s, and %s):\n", count(len(s.Running), "developer run"), waiting)
	}
	if len(s.Running) > 0 {
		listed, further := bound(len(s.Running))
		for _, run := range s.Running[:listed] {
			fmt.Fprintf(&rendered, "  %s — %s, %s elapsed, %s%s\n",
				run.WorkItemID, phaseOf(run), age(run.Elapsed), spendOf(run), slotOf(run))
		}
		rendered.WriteString(remainder(further, "developer run"))
	}
	listed, further := bound(len(s.Dispatching))
	for _, wait := range s.Dispatching[:listed] {
		fmt.Fprintf(&rendered, "  %s\n", wait.Says())
	}
	if further > 0 {
		fmt.Fprintf(&rendered, "  and %s not named here\n", dispatches(further))
	}
	if s.DispatchingProblem != "" {
		fmt.Fprintf(&rendered, "%s%s\n", partialRead, s.DispatchingProblem)
	}
	// The slots with nothing in them, each with what it prefers, said under the
	// runs where some slot prefers a label. A free slot that prefers a label is
	// the one fact about capacity a reader cannot infer from the runs: it says
	// what the next pull will look for first. Nothing is said where no slot
	// prefers anything, so the line reads as it always did.
	for _, slot := range s.DeveloperSlots {
		if slot.Free() {
			fmt.Fprintf(&rendered, "  developer slot %d is free and prefers %s\n", slot.Number, slot.Preference())
		}
	}
	return rendered.String()
}

// dispatches counts dispatches, which count cannot: its plural is the noun with
// an s on it.
func dispatches(number int) string {
	if number == 1 {
		return "1 dispatch"
	}
	return fmt.Sprintf("%d dispatches", number)
}

// slotOf is which developer slot a run occupies, and what that slot prefers,
// said after the spend where some configured slot prefers a label. It is empty
// where none does — a project that configured no preference reads exactly as it
// did — and for a run in flight beyond the configured capacity, which no slot
// holds.
func slotOf(run RunningRun) string {
	if run.Slot == 0 {
		return ""
	}
	return ", in " + (DeveloperSlotStanding{Number: run.Slot, Preferred: run.SlotPrefers}).Says()
}

func (s Standing) renderWorking() string {
	if s.WorkingProblem != "" && len(s.Working) == 0 {
		return unreadable("Working", s.WorkingProblem)
	}
	var rendered strings.Builder
	if len(s.Working) == 0 {
		rendered.WriteString("Working: nothing\n")
	} else {
		fmt.Fprintf(&rendered, "Working (%s):\n", count(len(s.Working), "conversation"))
		listed, further := bound(len(s.Working))
		for _, turn := range s.Working[:listed] {
			fmt.Fprintf(&rendered, "  %s — %s, a turn in flight for %s after %s\n",
				turn.Agent, turn.Role, age(turn.Elapsed), count(turn.Turns, "recorded turn"))
		}
		rendered.WriteString(remainder(further, "conversation"))
	}
	// A partial answer is said under the count rather than in place of it: the
	// conversations that did answer are still worth reporting, and a count nobody
	// was told was partial is a count somebody trusts.
	if s.WorkingProblem != "" {
		fmt.Fprintf(&rendered, "%s%s\n", partialRead, s.WorkingProblem)
	}
	return rendered.String()
}

func (s Standing) renderNotStartable() string {
	if s.NotStartableProblem != "" && len(s.NotStartable) == 0 {
		return unreadable("Not startable", s.NotStartableProblem)
	}
	var rendered strings.Builder
	if len(s.NotStartable) == 0 {
		fmt.Fprintf(&rendered, "Not startable: nothing, of %s\n", count(s.Admitted, "admitted item"))
	} else {
		fmt.Fprintf(&rendered, "Not startable (%d of %s%s):\n",
			len(s.NotStartable), count(s.Admitted, "admitted item"), s.heldSplit())
		listed, further := bound(len(s.NotStartable))
		for _, refused := range s.NotStartable[:listed] {
			fmt.Fprintf(&rendered, "  %s — %s\n", refused.WorkItemID, refused.Reason)
		}
		rendered.WriteString(remainder(further, "refused item"))
	}
	if s.NotStartableProblem != "" {
		fmt.Fprintf(&rendered, "%s%s\n", partialRead, s.NotStartableProblem)
	}
	return rendered.String()
}

func (s Standing) renderNeedsHuman() string {
	if s.NeedsHumanProblem != "" && len(s.NeedsHuman) == 0 {
		return unreadable("Needs a human", s.NeedsHumanProblem)
	}
	var rendered strings.Builder
	if len(s.NeedsHuman) == 0 {
		rendered.WriteString("Needs a human: nothing\n")
	} else {
		fmt.Fprintf(&rendered, "Needs a human (%d):\n", len(s.NeedsHuman))
		listed, further := bound(len(s.NeedsHuman))
		for _, waiting := range s.NeedsHuman[:listed] {
			fmt.Fprintf(&rendered, "  %s — %s\n", waiting.What(), waiting.Whose())
		}
		rendered.WriteString(remainder(further, "thing waiting on somebody"))
	}
	if s.NeedsHumanProblem != "" {
		fmt.Fprintf(&rendered, "%s%s\n", partialRead, s.NeedsHumanProblem)
	}
	return rendered.String()
}

// heldSplit is how much of the not-startable line is held work, said in the head
// and split by whose move it is: items the development manager has still to
// decide about, and items whose decision she recorded and the harness has still
// to act on.
//
// It is in the head rather than under it because the head is the whole of what
// an hourly message carries — the brief rendering drops every entry and keeps
// the heads — so a distinction only the entries made would be invisible in
// exactly the message that woke somebody. It says nothing at all when neither
// kind is present, which is a queue held by dependencies and directives and
// where a clause about triage would be noise.
//
// The decisions the harness has not carried out are counted beside it by what
// became of them — refused by a gate, or never attempted by a pass — whenever
// either kind stands, because the two are fixed in different places and the
// second is the one that used to be invisible.
func (s Standing) heldSplit() string {
	decision := fmt.Sprintf("%d %s the development manager's decision", s.AwaitingDecision, awaits(s.AwaitingDecision))
	carryOut := fmt.Sprintf("%d %s the harness carrying out a decision already recorded", s.AwaitingCarryOut, awaits(s.AwaitingCarryOut))
	var split string
	switch {
	case s.AwaitingDecision > 0 && s.AwaitingCarryOut > 0:
		split = "; " + decision + ", " + carryOut
	case s.AwaitingDecision > 0:
		split = "; " + decision
	case s.AwaitingCarryOut > 0:
		split = "; " + carryOut
	}
	if s.CarryOutsRefused > 0 || s.CarryOutsUnattempted > 0 {
		split += fmt.Sprintf("; decisions not carried out: %d refused, %d unattempted", s.CarryOutsRefused, s.CarryOutsUnattempted)
	}
	return split
}

// unreadable is a line whose source could not be read. It is never "nothing":
// what this says is that the harness does not know, which is a different answer
// and the one a reader has to act on.
func unreadable(label, problem string) string {
	return fmt.Sprintf("%s: could not be read — %s\n", label, problem)
}

// phaseOf is where a run has got to, or the stated absence. A record written
// before the run reached a phase has none, and a blank in the middle of a line
// reads as a bug in the printing.
//
// A run promoting again after the environment stopped it says so in the read
// model's own words rather than as the bare phase: what an operator who signed
// overrides for that stop is reading the line for is that the approval stood
// and nothing was spent.
//
// A run in its checks says where the stage stands rather than the bare phase,
// for the same reason: "checks: 14m of 30m" is what an operator watching a
// slow stage is reading the line for, and the bound is the number that says
// whether it is slow.
func phaseOf(run RunningRun) string {
	if run.ResumingIntegration {
		return runstate.ResumingIntegrationSays
	}
	if run.Checks != "" {
		return run.Checks
	}
	if strings.TrimSpace(string(run.Phase)) == "" {
		return "no phase recorded yet"
	}
	return string(run.Phase)
}

// spendOf is what a run has spent so far. A run whose evidence cannot be read is
// stated as unpriceable rather than as free, and it says why: a figure of zero
// against an hour of provider work is the one number nobody may print.
func spendOf(run RunningRun) string {
	if run.UnknownCost != "" {
		return "cost unknown (" + run.UnknownCost + ")"
	}
	return fmt.Sprintf("$%.2f so far", run.CostUSD)
}

// count says a number with the noun it counts, in the three forms that read
// differently: none, one, and several. "0 developer runs" is arithmetic; "no
// developer runs" is an answer.
func count(number int, noun string) string {
	switch number {
	case 0:
		return "no " + noun + "s"
	case 1:
		return "1 " + noun
	default:
		return fmt.Sprintf("%d %ss", number, noun)
	}
}

// bound is how many entries a line names and how many it only counts.
func bound(total int) (int, int) {
	if total <= maxListed {
		return total, 0
	}
	return maxListed, total - maxListed
}

// remainder says what a bounded line did not name. A line that silently stopped
// at ten would read as ten being all there was, which is the truncation that
// makes a status worse than no status.
func remainder(further int, noun string) string {
	if further == 0 {
		return ""
	}
	return fmt.Sprintf("  and %s not named here\n", count(further, noun))
}

// age is an elapsed time as somebody says one. It is coarse on purpose: what a
// status answers is roughly how long this has been going, and a duration printed
// to the nanosecond is a number a reader has to parse before they can read it.
func age(elapsed time.Duration) string {
	switch {
	case elapsed < 0:
		// A record stamped ahead of this reading. It is said as itself rather than
		// as a negative duration, which reads as a bug in the arithmetic.
		return "no time at all; its record is stamped ahead of this reading"
	case elapsed < time.Minute:
		return fmt.Sprintf("%ds", int(elapsed.Seconds()))
	case elapsed < time.Hour:
		return fmt.Sprintf("%dm", int(elapsed.Minutes()))
	case elapsed < 24*time.Hour:
		return fmt.Sprintf("%dh%02dm", int(elapsed.Hours()), int(elapsed.Minutes())%60)
	default:
		return fmt.Sprintf("%dd%02dh", int(elapsed.Hours())/24, int(elapsed.Hours())%24)
	}
}

// RenderProgramManagers is one line per program manager instance, printed under
// the four lines rather than as a fifth: nothing on it waits on a person, and
// the operator reads an instance when he chooses. Each line names the instance,
// its lane, and its status word, and says why where the word is not working.
// It is empty where no instance is configured and none has asked for anything,
// and says so where the instances were listed but something behind them could
// not be read.
func (s Standing) RenderProgramManagers() string {
	if len(s.ProgramManagers) == 0 && s.ProgramManagersProblem == "" {
		return ""
	}
	var rendered strings.Builder
	fmt.Fprintf(&rendered, "Program managers (%d):\n", len(s.ProgramManagers))
	for _, instance := range s.ProgramManagers {
		lane := instance.Lane
		if lane == "" {
			lane = "no lane configured"
		} else {
			lane = "lane " + lane
		}
		fmt.Fprintf(&rendered, "  %s — %s — %s%s\n", instance.Agent, lane, instance.Status, instance.why())
	}
	if s.ProgramManagersProblem != "" {
		rendered.WriteString(partialRead + s.ProgramManagersProblem + "\n")
	}
	return rendered.String()
}

// why is what follows an instance's status word: the reason it is stale, how
// many open asks of its own block it, and how many of its report's blockers the
// record does not bear out. Both halves are said where both hold, because stale
// outranks blocked in the word and the reader should still see the second.
func (p ProgramManager) why() string {
	var parts []string
	if p.Stale {
		parts = append(parts, p.StaleSays)
	}
	if p.Blocked {
		cited := make([]string, 0, len(p.Blockers))
		for _, blocker := range p.Blockers {
			cited = append(cited, blocker.Cites)
		}
		parts = append(parts, fmt.Sprintf("blocked on %s (%s)", count(len(p.Blockers), "open ask"), strings.Join(cited, ", ")))
	}
	if len(p.Claims) > 0 {
		parts = append(parts, fmt.Sprintf("%s its report names that the record does not bear out", count(len(p.Claims), "blocker")))
	}
	if len(parts) == 0 {
		return ""
	}
	return ": " + strings.Join(parts, "; ")
}

// StaleProgramManagersLine is the count the channel's hourly line carries of
// stale instances, and empty where none is stale. It is said only inside a
// message that line already posts for another reason: a stale instance is
// something the operator reviews when he chooses, and a message sent for it
// alone would be the push he asked not to be sent.
func (s Standing) StaleProgramManagersLine() string {
	stale := s.StaleProgramManagers()
	if len(stale) == 0 {
		return ""
	}
	return fmt.Sprintf("Program managers stale: %d of %d (%s)\n", len(stale), len(s.ProgramManagers), strings.Join(stale, ", "))
}
