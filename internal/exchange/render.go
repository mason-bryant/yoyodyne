package exchange

// How an exchange reads: to the operator who is auditing it, and to the asking
// role that is waiting on it.
//
// Both renderings say the rounds and the cost together, always. That pairing is
// the point: rounds alone say how long a conversation went on and cost alone
// says what it came to, and the operator's question — was this worth it — is
// only answerable from the two side by side.

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// Sort orders exchanges the way they are read: the ones still being conducted
// first, and the most recently moved first within each group. An open exchange
// is the one that might still need somebody.
func Sort(exchanges []Exchange) {
	sort.SliceStable(exchanges, func(i, j int) bool {
		if exchanges[i].Open() != exchanges[j].Open() {
			return exchanges[i].Open()
		}
		return exchanges[i].UpdatedAt.After(exchanges[j].UpdatedAt)
	})
}

// State is what an exchange is, in one word an operator reads down a column.
func (e Exchange) State() string {
	if e.Open() {
		return "open"
	}
	return string(e.Outcome)
}

// Summary is one exchange as a line: who asked whom, where it got to, and what
// it cost.
func (e Exchange) Summary() string {
	return fmt.Sprintf("%s  %s asked %s, %s, %d/%d round(s), %s",
		e.ID, e.Asker.Role.Title(), e.Answerer.Role.Title(), e.State(),
		e.Spent(), e.MaxRounds, money(e.CostUSD()))
}

// Render is one exchange as an operator reads it in a listing: the line above,
// the question it opened with, and what closed it.
func (e Exchange) Render() string {
	var rendered strings.Builder
	rendered.WriteString(e.Summary() + "\n")
	rendered.WriteString(indent("asked: " + singleLine(e.Question)))
	switch {
	case e.Outcome == OutcomeResolved && strings.TrimSpace(e.Settled) != "":
		rendered.WriteString(indent("settled: " + singleLine(e.Settled)))
	case e.Outcome == OutcomeUnresolved:
		rendered.WriteString(indent(fmt.Sprintf(
			"nothing was settled: it reached the %d round(s) it was opened with, and was escalated to you", e.MaxRounds)))
	}
	return rendered.String()
}

// RenderThread is the whole exchange, which is what "durable and visible" comes
// to in the end: every question and every answer as the two roles wrote them,
// readable by somebody who was not there.
func (e Exchange) RenderThread() string {
	var rendered strings.Builder
	rendered.WriteString(e.Summary() + "\n")
	fmt.Fprintf(&rendered, "opened %s", e.OpenedAt.UTC().Format(time.RFC3339))
	if e.ClosedAt != nil {
		fmt.Fprintf(&rendered, ", closed %s", e.ClosedAt.UTC().Format(time.RFC3339))
	}
	rendered.WriteString("\n")
	if agent := strings.TrimSpace(e.Asker.Agent); agent != "" {
		fmt.Fprintf(&rendered, "asked by %s", agent)
		if conversation := strings.TrimSpace(e.Asker.Conversation); conversation != "" {
			fmt.Fprintf(&rendered, ", from %s", conversation)
		}
		rendered.WriteString("\n")
	}
	if agent := strings.TrimSpace(e.Answerer.Agent); agent != "" {
		fmt.Fprintf(&rendered, "answered by %s\n", agent)
	}
	for _, round := range e.Rounds {
		fmt.Fprintf(&rendered, "\nround %d of %d (%s)\n", round.Number, e.MaxRounds, money(round.CostUSD))
		rendered.WriteString(indent(e.Asker.Role.Title() + ": " + strings.TrimSpace(round.Question)))
		if context := strings.TrimSpace(round.Context); context != "" {
			rendered.WriteString(indent("context: " + context))
		}
		switch {
		case strings.TrimSpace(round.Answer) != "":
			rendered.WriteString(indent(e.Answerer.Role.Title() + ": " + strings.TrimSpace(round.Answer)))
		case strings.TrimSpace(round.Problem) != "":
			rendered.WriteString(indent("unanswered: " + round.Problem))
		default:
			rendered.WriteString(indent("unanswered: the round was recorded and nothing came back"))
		}
	}
	if strings.TrimSpace(e.Settled) != "" {
		fmt.Fprintf(&rendered, "\nsettled: %s\n", strings.TrimSpace(e.Settled))
	}
	if e.Outcome == OutcomeUnresolved {
		fmt.Fprintf(&rendered, "\nnothing was settled: it reached the %d round(s) it was opened with, and was escalated to you.\n", e.MaxRounds)
	}
	return rendered.String()
}

// Delivery is what the harness hands back to the asking role when a round comes
// in: the answer, where the exchange has got to against its cap, and what the
// asker may do next. The cap is stated as live state rather than as contract
// text, because what a role needs is not the setting but how much of it this
// thread has left.
func (e Exchange) Delivery() string {
	var rendered strings.Builder
	rendered.WriteString("# The " + e.Answerer.Role.Title() + "'s answer\n\n")
	fmt.Fprintf(&rendered, "Exchange %s, round %d of the %d it is allowed. This is the %s's judgement and nothing more: it had no tools and no authority, so treat it as an opinion you asked for rather than as evidence or as a decision. It is recorded and the operator can read it.\n\n",
		e.ID, e.Spent(), e.MaxRounds, e.Answerer.Role.Title())
	if len(e.Rounds) == 0 {
		// Nothing calls this for an exchange with no rounds, and an index into an
		// empty thread is not the way to find that out.
		return rendered.String() + "No round has been taken yet.\n"
	}
	last := e.Rounds[len(e.Rounds)-1]
	switch {
	case strings.TrimSpace(last.Answer) != "":
		rendered.WriteString(strings.TrimSpace(last.Answer) + "\n\n")
	default:
		problem := strings.TrimSpace(last.Problem)
		if problem == "" {
			problem = "nothing came back"
		}
		fmt.Fprintf(&rendered, "The question went unanswered: %s. The round is spent either way. Say so to the operator rather than describing an answer you did not get.\n\n", problem)
	}
	switch remaining := e.RoundsRemaining(); {
	case remaining > 0:
		fmt.Fprintf(&rendered, "Carry on answering the operator using this. Ask again in %s if you still need something, which leaves %d round(s); close it with \"settled\" as soon as you have what you needed.\n",
			e.ID, remaining)
	default:
		fmt.Fprintf(&rendered, "That was the last round %s is allowed. Close it with \"settled\", saying what you did and did not get; asking again closes it as unresolved and puts it to the operator.\n", e.ID)
	}
	return rendered.String()
}

// indent puts provider text under the harness's own line and never at the
// margin, which is what keeps a listing readable when what an agent wrote runs
// to several lines.
func indent(text string) string {
	trimmed := strings.TrimRight(text, "\n")
	if strings.TrimSpace(trimmed) == "" {
		return ""
	}
	var rendered strings.Builder
	for _, line := range strings.Split(trimmed, "\n") {
		rendered.WriteString("  " + line + "\n")
	}
	return rendered.String()
}
