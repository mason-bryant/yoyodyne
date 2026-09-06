package chat

// What stops the same work being admitted twice.
//
// Both doors into the queue ask this, and they ask it of the same judgement for
// the reason the admission gate is one predicate: the product manager reaches
// the direct "create" and the proposal path, and a check one of them made and
// the other did not would be duplicates arriving through whichever asked less.
//
// What each door does with the answer is different, because what is available to
// each is different. A creation is carried out and reported, with nobody to ask,
// so a creation that looks like admitted work is refused and the match is named:
// the role that asked is told which item this already is, which is what it needs
// either to act on that item instead or to say why this is not it. A proposal is
// already on its way to somebody, so nothing is refused — the resemblance is
// written onto the proposal, which stops the harness admitting it on a goal's
// authority and puts it in front of the operator with the match named.
//
// Neither ever drops it silently, which is the whole point. A guard whose finding
// reaches nobody is a guard that turns a duplicate into a mystery.

import (
	"context"
	"fmt"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/admission"
	"github.com/mason-bryant/yoyodyne/internal/report"
)

// alreadyAdmitted reports the admitted work a candidate looks like, and says why
// the question went unanswered where it did.
//
// The two are separate returns because they are separate things to do. Matches
// are acted on; an unanswered question is not, and must not be: losing an
// admission to a tracker that was briefly unavailable is worse than admitting a
// duplicate, since the duplicate is caught by whoever reads the queue and the
// lost admission is caught by nobody. So the caller carries on and says the check
// did not run, which is the same shape every other unread thing here takes.
func (s *Session) alreadyAdmitted(ctx context.Context, candidate admission.Candidate) ([]admission.Match, string) {
	if s.options.Tracker == nil {
		return nil, "no work tracker is configured, so nothing checked whether this work is already admitted"
	}
	// Every item rather than the open queue. The duplicate that costs a run is a
	// duplicate of work that has already landed — a diff against the target branch
	// arithmetically cannot contain what the target branch carries — and closed
	// work is exactly what an open-queue listing leaves out.
	admitted, err := s.options.Tracker.List(ctx, "")
	if err != nil {
		return nil, "the tracker would not list the admitted work, so nothing checked whether this is already in it: " +
			singleLine(err.Error(), maxTrackerFailureBytes)
	}
	return admission.Resembling(candidate, admitted), ""
}

// admissionSources are the records a candidate says the work came from, as those
// records hold their identifiers rather than as anything typed them. A reference
// is resolved before it gets here, so a directive named by a prefix is matched
// against the items citing the directive rather than against the prefix.
func admissionSources(cited ...string) []string {
	var sources []string
	for _, source := range cited {
		if trimmed := strings.TrimSpace(source); trimmed != "" {
			sources = append(sources, trimmed)
		}
	}
	return sources
}

// duplicateRefusal is what a creation that looks like admitted work is told. It
// names the match, says nothing was created, and names what the role can actually
// do about it — because a refusal that only says no is one the role answers by
// asking again in different words.
//
// What it does not offer is a way to insist that this is not a duplicate. There
// is one already and it is the right one: work the role believes is genuinely
// different is proposed, and the operator decides with the same match in front of
// them.
func duplicateRefusal(verb creation, matches []admission.Match) string {
	return fmt.Sprintf("%s already looks like work the tracker holds, so nothing was created: %s. %s",
		verb.subject, admission.Describe(matches), duplicateRemedy(matches))
}

// duplicateRemedy is what to do instead. It depends on where the matched work
// got to — work still open is work to fold this into or to widen, and work that
// has closed is work already done, which is what makes admitting it again a run
// spent on a diff that cannot contain anything — and on how it was matched.
//
// A source match has a remedy the other does not, because one record genuinely
// can prompt more than one piece of work: an operator's directive routinely does,
// and the contract has always said to name it on the item that answers it. So the
// second piece of work is admitted without the citation rather than not admitted,
// and this says so. Leaving that out would turn a guard into a wall in front of
// something the role is told to do.
func duplicateRemedy(matches []admission.Match) string {
	remedy := "That work is closed, so it is already done and a run made for this one could not contain anything it does not already carry. " +
		"Say so rather than admitting it again, and where something is genuinely left over, propose it with what is left over named."
	for _, match := range matches {
		if match.Status != closedWorkItemStatus {
			remedy = "Act on that item — update it, or say why this is separate work and propose it instead so the operator decides — rather than admitting this beside it."
			break
		}
	}
	for _, match := range matches {
		if match.Source != "" {
			return remedy + fmt.Sprintf(" Where %s genuinely prompted a second, separate piece of work, admit that without citing %s: the citation belongs on the one item that answers the record, and it is what this check reads.",
				match.Source, match.Source)
		}
	}
	return remedy
}

// uncheckedClause is what an admission that happened says about a duplicate check
// that did not run. It is folded into the same line the admission is reported on,
// so the operator's account and the role's results say it once and in the same
// words.
func uncheckedClause(unchecked string) string {
	if unchecked == "" {
		return ""
	}
	return ", and " + unchecked
}

// citedReport is the collected report an admission says the work came from, and
// is the zero report where it names none, which is most admissions.
//
// It is looked up rather than taken as typed, for the reason a directive is: an
// item whose record names a report nobody filed says where the work came from and
// says something untrue, and the guard above decides from exactly these citations
// — so a citation nothing checked is a guard that silently stops working.
func (s *Session) citedReport(named string) (report.Report, error) {
	reported := strings.TrimSpace(named)
	if reported == "" {
		return report.Report{}, nil
	}
	if s.options.Reports == nil {
		return report.Report{}, errNoReports
	}
	reports, err := s.options.Reports.List()
	if err != nil {
		return report.Report{}, fmt.Errorf("read the collected reports: %w", err)
	}
	for _, collected := range reports {
		if collected.ID == reported {
			return collected, nil
		}
	}
	return report.Report{}, fmt.Errorf("no report in the pile is %s; name a report exactly as it was listed to you", reported)
}

// reportNote is what an item admitted from a report records about it, and is
// nothing at all on the ordinary admission that cites none. It carries the
// reporter's own words as well as the identifier, for the reason a directive note
// does: an item naming a record somebody has to go and open says less than one
// that says what was reported.
func reportNote(cited report.Report) string {
	if cited.ID == "" {
		return ""
	}
	return fmt.Sprintf("\n\nAdmitted from report %s, filed at %q by the %s: %s",
		cited.ID, cited.Severity, RoleTitle(cited.Role), singleLine(cited.Message, maxTrackerFailureBytes))
}

// citedClause is what one line about an admission says about the report it was
// admitted from. It is folded into the summary rather than rendered separately,
// exactly as the directive's clause is.
func citedClause(cited report.Report) string {
	if cited.ID == "" {
		return ""
	}
	return ", admitted from report " + cited.ID
}

// resemblingProposals judges each proposal against the work the tracker already
// holds, once for the whole turn. The listing is taken once rather than per
// proposal for the reason the placement check looks its references up together: a
// turn proposing three items asks the tracker one question, and every proposal in
// it is judged against the same answer.
//
// A proposal that cannot be judged is judged as resembling nothing rather than
// held back. The operator is being asked about it either way; what a failed check
// costs there is the sentence naming a match, and what refusing would cost is the
// proposal.
func (s *Session) resemblingProposals(ctx context.Context, proposals []Proposal) []string {
	resembling := make([]string, len(proposals))
	if len(proposals) == 0 || s.options.Tracker == nil {
		return resembling
	}
	admitted, err := s.options.Tracker.List(ctx, "")
	if err != nil {
		return resembling
	}
	for i, proposal := range proposals {
		matches := admission.Resembling(admission.Candidate{
			Title:  strings.TrimSpace(proposal.Title),
			Parent: strings.TrimSpace(proposal.Parent),
		}, admitted)
		if len(matches) == 0 {
			continue
		}
		resembling[i] = "it looks like work already admitted: " + admission.Describe(matches)
	}
	return resembling
}
