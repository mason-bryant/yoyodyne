package chat

// An operator decides proposals in batches. Batching changes what one answer
// may say and nothing else: nothing is created without an approval that names
// the item, an answer nobody can be sure of declines, and every decision is
// recorded on its own exactly as a serial answer records it. A batch approval
// is several approvals rather than a new kind of thing.

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"yoyodyne/internal/console"
)

// The words one answer is written with. An approval is spelled out or answered
// the way a single question has always been answered; a decline has the same
// short forms. Everything is matched case-insensitively, and nothing else is a
// verb at all.
var (
	approveWords = []string{"approve", "y", "yes"}
	declineWords = []string{"decline", "n", "no"}
)

// allSelector names every proposal still on the table. It is accepted on a
// decline and refused on an approval, and the asymmetry is the fail-closed rule
// itself: turning work down wholesale is a decision an operator can make in one
// word, while creating work is not, because an approval has to name what it
// approves.
const allSelector = "all"

// connectorWords join two clauses of one answer, so "approve 1,3 and decline 2"
// reads the way an operator would write it.
var connectorWords = []string{"and", "then", "also"}

// card is one proposal as the operator is asked about it: the number they
// answer with and the proposal it names. The number is the proposal's place in
// the turn that proposed it, so it never changes as decisions are made — a
// number that meant a different item on the second round would be a way to
// approve something nobody read.
type card struct {
	number   int
	proposal PendingProposal
}

// Render describes one proposal as a card the operator decides on. The number
// leads it because that is what they answer with, and the proposal's own
// identifier is beside it because that is what the record calls it.
func (c card) Render(theme console.Theme) string {
	heading := fmt.Sprintf("%d · proposal %s · %s", c.number, c.proposal.ID, strings.TrimSpace(c.proposal.Proposal.Title))
	return theme.Card(heading, strings.Join(c.proposal.body(), "\n"))
}

// decision is one proposal and what the operator said about it. It is the unit
// the session acts on, so a batch answer and a single one reach exactly the
// same recording path.
type decision struct {
	proposalID string
	approve    bool
	// reason is why a declined proposal was turned down. It is empty on an
	// approval, which needs no reason: the item itself records where it came
	// from.
	reason string
}

// errNotADecision reports an answer that is not a decision at all: it names no
// proposal and begins with nothing the harness recognizes. It is separate from
// every other failure here because it is the one the contract already decides —
// an answer nobody can be sure of declines, and is kept as the reason.
var errNotADecision = errors.New("that answer does not decide anything")

// readDecisions turns one answer into the decisions it asks for, resolved
// against the proposals the operator was shown.
//
// It is deliberately strict in one direction only. An answer it cannot read
// whole decides nothing rather than partly something: an approval that names an
// item that is not there, or a clause that trails off into words, leaves every
// proposal awaiting a decision and is put to the operator again. Nothing here
// can create anything, so being asked twice is the entire cost of a typo, while
// acting on half of a misread answer would create work nobody asked for.
func readDecisions(answer string, cards []card) ([]decision, error) {
	rest := strings.TrimSpace(answer)
	if rest == "" {
		return nil, errNotADecision
	}
	var decisions []decision
	named := make(map[string]bool, len(cards))
	for rest != "" {
		verb, after := nextWord(rest)
		approve := matches(verb, approveWords)
		if !approve && !matches(verb, declineWords) {
			if len(decisions) == 0 {
				return nil, errNotADecision
			}
			return nil, fmt.Errorf("%q is not part of a decision; say approve or decline before the proposals you mean", verb)
		}
		selectors, remainder := takeSelectors(after)
		if approve {
			chosen, err := resolveApproved(selectors, cards, named)
			if err != nil {
				return nil, err
			}
			for _, id := range chosen {
				decisions = append(decisions, decision{proposalID: id, approve: true})
			}
			rest = skipConnector(remainder)
			continue
		}
		// A declined proposal keeps the operator's own words, so the reason runs
		// to the end of the line rather than stopping at the next thing that looks
		// like a verb. That makes a decline the last clause of an answer, which is
		// stated in the prompt: an approval written after one is never carried out,
		// and the proposal it named is put again rather than quietly created.
		chosen, err := resolveDeclined(selectors, cards, named)
		if err != nil {
			return nil, err
		}
		reason := strings.TrimSpace(remainder)
		for _, id := range chosen {
			decisions = append(decisions, decision{proposalID: id, reason: reason})
		}
		rest = ""
	}
	if len(decisions) == 0 {
		return nil, errNotADecision
	}
	return inCardOrder(decisions, cards), nil
}

// resolveApproved names the proposals an approval clause approves. An approval
// naming nothing is accepted only where there is exactly one proposal on the
// table, because there it does name it — that is the single question an
// operator has always answered with a bare yes. Where there is more than one it
// is refused, since the harness would otherwise be choosing which item to
// create.
func resolveApproved(selectors []string, cards []card, named map[string]bool) ([]string, error) {
	if len(selectors) == 0 {
		if len(cards) != 1 {
			return nil, fmt.Errorf("say which of the %d proposals to approve, as approve 1,3; an approval has to name what it creates", len(cards))
		}
		selectors = []string{"1"}
	}
	var chosen []string
	for _, selector := range selectors {
		if strings.EqualFold(selector, allSelector) {
			return nil, errors.New("an approval names the proposals it creates rather than all of them; say approve 1,3")
		}
		found, err := resolve(selector, cards, named)
		if err != nil {
			return nil, err
		}
		chosen = append(chosen, found)
	}
	return chosen, nil
}

// resolveDeclined names the proposals a decline clause turns down. Unlike an
// approval it may name none of them, which declines everything still on the
// table: nothing is created either way, so the operator saying "no" to the lot
// is a decision the harness can act on as it stands.
func resolveDeclined(selectors []string, cards []card, named map[string]bool) ([]string, error) {
	if len(selectors) == 0 || (len(selectors) == 1 && strings.EqualFold(selectors[0], allSelector)) {
		var chosen []string
		for _, entry := range cards {
			if named[entry.proposal.ID] {
				continue
			}
			named[entry.proposal.ID] = true
			chosen = append(chosen, entry.proposal.ID)
		}
		if len(chosen) == 0 {
			return nil, errors.New("every proposal in that answer was already decided by it")
		}
		return chosen, nil
	}
	var chosen []string
	for _, selector := range selectors {
		if strings.EqualFold(selector, allSelector) {
			return nil, errors.New("say either all or the proposals you mean, not both")
		}
		found, err := resolve(selector, cards, named)
		if err != nil {
			return nil, err
		}
		chosen = append(chosen, found)
	}
	return chosen, nil
}

// resolve finds the one proposal a selector names, by the number on its card or
// by its own identifier. A selector naming nothing on the table, and one naming
// a proposal the same answer already decided, are both refused: an answer that
// contradicts itself is exactly the kind nobody can be sure of.
func resolve(selector string, cards []card, named map[string]bool) (string, error) {
	number, numeric := cardNumber(selector)
	for _, entry := range cards {
		if entry.proposal.ID != selector && !(numeric && entry.number == number) {
			continue
		}
		if named[entry.proposal.ID] {
			return "", fmt.Errorf("proposal %s is decided twice in that answer", entry.proposal.ID)
		}
		named[entry.proposal.ID] = true
		return entry.proposal.ID, nil
	}
	return "", fmt.Errorf("%s is not one of the proposals you are being asked about", selector)
}

// cardNumber reads a selector as the number on a card. A proposal identifier is
// turn.position, so it is never mistaken for one of these.
func cardNumber(selector string) (int, bool) {
	number, err := strconv.Atoi(selector)
	if err != nil || number <= 0 {
		return 0, false
	}
	return number, true
}

// inCardOrder puts the decisions in the order the operator was shown them, so
// what is recorded and what is printed follow the cards rather than the order
// the clauses happened to be written in.
func inCardOrder(decisions []decision, cards []card) []decision {
	ordered := make([]decision, 0, len(decisions))
	for _, entry := range cards {
		for _, made := range decisions {
			if made.proposalID == entry.proposal.ID {
				ordered = append(ordered, made)
			}
		}
	}
	return ordered
}

// declineAll is what an answer nobody can be sure of comes to: every proposal
// still on the table is declined, and the answer itself is kept as the reason,
// exactly as it is when a single proposal is answered with something that is
// not a yes.
func declineAll(cards []card, reason string) []decision {
	decisions := make([]decision, 0, len(cards))
	for _, entry := range cards {
		decisions = append(decisions, decision{proposalID: entry.proposal.ID, reason: reason})
	}
	return decisions
}

// takeSelectors consumes the proposals a clause names, and returns what is left
// of the answer. Commas, spaces, and a joining word between two selectors all
// separate them, because an operator writes "1,3", "1, 3", and "1 and 3" and
// means the same thing by all three.
func takeSelectors(text string) ([]string, string) {
	var selectors []string
	rest := text
	for rest != "" {
		word, after := nextWord(rest)
		if matches(word, connectorWords) && len(selectors) > 0 {
			// A joining word only separates selectors when a selector follows it.
			// Otherwise it belongs to whatever comes next, which is the second
			// clause of the answer.
			if next, _ := nextWord(after); !isSelectorList(next) {
				break
			}
			rest = after
			continue
		}
		if !isSelectorList(word) {
			break
		}
		selectors = append(selectors, splitSelectors(word)...)
		rest = after
	}
	return selectors, rest
}

// isSelectorList reports a word that is nothing but proposals, so a clause
// stops consuming at the first word that is prose.
func isSelectorList(word string) bool {
	parts := splitSelectors(word)
	if len(parts) == 0 {
		return false
	}
	for _, part := range parts {
		if strings.EqualFold(part, allSelector) {
			continue
		}
		if _, numeric := cardNumber(part); numeric {
			continue
		}
		if !isProposalID(part) {
			return false
		}
	}
	return true
}

// isProposalID reports the turn.position shape a recorded proposal is named by.
func isProposalID(value string) bool {
	turn, position, found := strings.Cut(value, ".")
	if !found {
		return false
	}
	if _, numeric := cardNumber(position); !numeric {
		return false
	}
	number, err := strconv.Atoi(turn)
	return err == nil && number >= 0
}

// splitSelectors breaks one word into the proposals it names, dropping the
// commas and semicolons an operator separates them with.
func splitSelectors(word string) []string {
	var parts []string
	for _, part := range strings.FieldsFunc(word, func(r rune) bool { return r == ',' || r == ';' }) {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			parts = append(parts, trimmed)
		}
	}
	return parts
}

// skipConnector steps over whatever joins two clauses, so the next word an
// answer is read from is the verb that begins the clause after it.
func skipConnector(text string) string {
	rest := strings.TrimLeft(text, " \t,;")
	if word, after := nextWord(rest); matches(word, connectorWords) {
		return strings.TrimLeft(after, " \t,;")
	}
	return rest
}

// nextWord splits the first word off some text and returns what follows it.
func nextWord(text string) (string, string) {
	trimmed := strings.TrimLeft(text, " \t")
	at := strings.IndexAny(trimmed, " \t")
	if at < 0 {
		return trimmed, ""
	}
	return trimmed[:at], strings.TrimLeft(trimmed[at:], " \t")
}

// matches reports one of a small set of words, whatever case it was typed in.
func matches(word string, words []string) bool {
	for _, candidate := range words {
		if strings.EqualFold(word, candidate) {
			return true
		}
	}
	return false
}
