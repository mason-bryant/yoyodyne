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
)

// maxListed bounds how many entries one line names before it counts the rest.
// The counts stay exact either way: what a status owes a reader is what is
// happening, not an export of the queue.
const maxListed = 10

// Render is the four lines. It is deterministic, so the same standing always
// prints the same text, and it always returns exactly four labelled lines with
// whatever they carry indented under them.
func (s Standing) Render() string {
	var rendered strings.Builder
	rendered.WriteString(s.renderRunning())
	rendered.WriteString(s.renderWorking())
	rendered.WriteString(s.renderNotStartable())
	rendered.WriteString(s.renderNeedsHuman())
	return rendered.String()
}

func (s Standing) renderRunning() string {
	if s.RunningProblem != "" {
		return unreadable("Running", s.RunningProblem)
	}
	if len(s.Running) == 0 {
		return "Running: nothing\n"
	}
	var rendered strings.Builder
	fmt.Fprintf(&rendered, "Running (%s):\n", count(len(s.Running), "developer run"))
	listed, further := bound(len(s.Running))
	for _, run := range s.Running[:listed] {
		fmt.Fprintf(&rendered, "  %s — %s, %s elapsed, %s\n",
			run.WorkItemID, phaseOf(run), age(run.Elapsed), spendOf(run))
	}
	rendered.WriteString(remainder(further, "developer run"))
	return rendered.String()
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
		fmt.Fprintf(&rendered, "  not fully read: %s\n", s.WorkingProblem)
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
		fmt.Fprintf(&rendered, "Not startable (%d of %s):\n", len(s.NotStartable), count(s.Admitted, "admitted item"))
		listed, further := bound(len(s.NotStartable))
		for _, refused := range s.NotStartable[:listed] {
			fmt.Fprintf(&rendered, "  %s — %s\n", refused.WorkItemID, refused.Reason)
		}
		rendered.WriteString(remainder(further, "refused item"))
	}
	if s.NotStartableProblem != "" {
		fmt.Fprintf(&rendered, "  not fully read: %s\n", s.NotStartableProblem)
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
			fmt.Fprintf(&rendered, "  %s — %s\n", waiting.What, waiting.Whose)
		}
		rendered.WriteString(remainder(further, "thing waiting on somebody"))
	}
	if s.NeedsHumanProblem != "" {
		fmt.Fprintf(&rendered, "  not fully read: %s\n", s.NeedsHumanProblem)
	}
	return rendered.String()
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
func phaseOf(run RunningRun) string {
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
