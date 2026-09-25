package chat

// A report handled as covered by work says, request by request, which item
// covers what.
//
// A report is two sentences, and two sentences can ask for two things. On
// 2026-09-02 the development manager reported that the docket's entry lifecycle
// should consume recorded decisions and closed status rather than re-presenting
// them; on 09-05 it was handled as covered by yoyodyne-ifd.269, which covered the
// decisions. The closed-status half was covered by nothing, lapsed without a
// word, and was found three weeks later as 125 dead docket entries. A handling
// that names one covering item in its reason loses every other request with
// nothing anybody can audit, so the covering items are structured instead, and a
// request nothing answers is refused rather than recorded.

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/report"
)

// coveredByInProse is a reason that says in words that an item covers the
// report: "covered by", followed by something shaped like an item identifier.
// It is what the one-sentence handling looked like, and a reason that still says
// it with no mapping beside it is refused, because the words are exactly the
// claim the mapping exists to make checkable.
var coveredByInProse = regexp.MustCompile(`(?i)\bcovered\s+by\s+[a-z0-9][a-z0-9-]*[.-][a-z0-9.-]*[a-z0-9]`)

// requestProblems checks a handling's mapping as far as one action can be
// checked. Whether a covering item exists needs the tracker, and whether an
// admission in the block happened needs the block to have run, so those are
// asked as the handling is carried out.
func (a TrackerAction) requestProblems() []error {
	var problems []error
	if len(a.Requests) == 0 {
		if match := coveredByInProse.FindString(a.Reason); match != "" {
			problems = append(problems, fmt.Errorf("the reason says %q but the handling maps none of the report's requests; list each request in \"requests\" with the item that covers it in \"covered_by\", and admit or decline any request no item covers, so a request the covering item does not answer is not lost with the report", match))
		}
		return problems
	}
	if len(a.Requests) > report.MaxRequestsPerHandling {
		problems = append(problems, fmt.Errorf("handle maps %d requests, limit is %d", len(a.Requests), report.MaxRequestsPerHandling))
	}
	seen := make(map[string]bool, len(a.Requests))
	for _, request := range a.Requests {
		quoted := strings.TrimSpace(request.Request)
		switch {
		case quoted == "":
			problems = append(problems, errors.New("every mapped request needs \"request\", the words of what the report asked for"))
			continue
		case len(quoted) > report.MaxRequestBytes:
			problems = append(problems, fmt.Errorf("request %q is %d bytes, limit is %d", singleLine(quoted, maxTrackerFailureBytes), len(quoted), report.MaxRequestBytes))
		case strings.ContainsAny(quoted, "\r\n"):
			problems = append(problems, fmt.Errorf("request %q cannot span lines", singleLine(quoted, maxTrackerFailureBytes)))
		}
		if seen[quoted] {
			problems = append(problems, fmt.Errorf("request %q is mapped twice", quoted))
		}
		seen[quoted] = true
		covered, admitted, declined := strings.TrimSpace(request.CoveredBy), strings.TrimSpace(request.Admitted), strings.TrimSpace(request.Declined)
		answers := 0
		for _, answer := range []string{covered, admitted, declined} {
			if answer != "" {
				answers++
			}
		}
		switch {
		case answers == 0:
			// The refusal this whole mapping exists for, and it quotes the request,
			// because the request is the thing that would otherwise have been lost.
			problems = append(problems, fmt.Errorf("request %q is covered by no item: name the item that covers it in \"covered_by\", admit it with a \"create\" earlier in this block and give that creation's title in \"admitted\", or say in \"declined\" why nothing is being done about it", quoted))
		case answers > 1:
			problems = append(problems, fmt.Errorf("request %q takes exactly one of \"covered_by\", \"admitted\", and \"declined\"", quoted))
		}
		if covered != "" {
			if err := beads.ValidateIssueID(covered); err != nil {
				problems = append(problems, fmt.Errorf("request %q covered_by: %w", quoted, err))
			}
		}
		if err := boundTrackerText("declined", declined, report.MaxHandlingReasonBytes, false); err != nil {
			problems = append(problems, fmt.Errorf("request %q %w", quoted, err))
		}
	}
	return problems
}

// admissionsInBlockProblems refuses a handling that says a request is admitted by
// a creation the block does not carry before it. The admission has to come first
// because the handling records the identifier it was assigned, and it has to be
// in the same block because "admitted" is a claim about this reply rather than a
// promise about a later one — a promise is how a request lapses.
func admissionsInBlockProblems(actions []TrackerAction) []error {
	var problems []error
	created := map[string]bool{}
	for i, action := range actions {
		switch action.Action {
		case actionCreate:
			created[strings.TrimSpace(action.Title)] = true
		case actionHandle:
			for _, request := range action.Requests {
				title := strings.TrimSpace(request.Admitted)
				if title == "" || created[title] {
					continue
				}
				problems = append(problems, fmt.Errorf("actions[%d]: request %q is admitted by a creation titled %q, and no \"create\" before this handle in the same block carries that title", i, strings.TrimSpace(request.Request), title))
			}
		}
	}
	return problems
}

// resolveRequests turns the mapping a handling was asked for into the one it
// records: each admission's title replaced by the identifier the creation was
// assigned, and each covering item read from the tracker, so the record names
// items that exist. Nothing is written here; a request that cannot be resolved
// fails the handling before anything lands.
func (s *Session) resolveRequests(ctx context.Context, outcome *TrackerOutcome) ([]report.Request, error) {
	requests := make([]report.Request, 0, len(outcome.Action.Requests))
	read := map[string]string{}
	for _, asked := range outcome.Action.Requests {
		resolved := report.Request{
			Request:  strings.TrimSpace(asked.Request),
			Declined: strings.TrimSpace(asked.Declined),
		}
		if title := strings.TrimSpace(asked.Admitted); title != "" {
			id, admitted := outcome.admittedInBlock[title]
			if !admitted {
				return nil, fmt.Errorf("request %q was to be admitted by the creation titled %q, which did not happen, so the report stays in the pile", resolved.Request, title)
			}
			resolved.Admitted = id
		}
		if covering := strings.TrimSpace(asked.CoveredBy); covering != "" {
			id, known := read[covering]
			if !known {
				item, err := s.options.Tracker.Show(ctx, covering)
				if err != nil {
					return nil, fmt.Errorf("request %q is said to be covered by %s, which the tracker would not show: %w", resolved.Request, covering, err)
				}
				id = strings.TrimSpace(item.ID)
				if id == "" {
					id = covering
				}
				read[covering] = id
			}
			resolved.CoveredBy = id
		}
		requests = append(requests, resolved)
	}
	return requests, nil
}

// noteCoveringItems writes onto each item that answers one of the report's
// requests which of them it answers. The handling record says the same thing
// from the report's side; this is the item's side, and it is the one somebody
// closing or narrowing the item reads — so an item that was said to cover a
// request says so on itself, and whoever finishes it can see what it owes.
//
// Each note is noted as landed as it lands, because the handling is written after
// them and can fail: a note saying an item covers a request stays true whatever
// became of the record beside it.
func (s *Session) noteCoveringItems(ctx context.Context, outcome *TrackerOutcome, subject report.Report, requests []report.Request) error {
	items, covers := report.Covering(requests)
	for _, item := range items {
		var note strings.Builder
		note.WriteString(s.trackerProvenance("Named as covering report "+subject.ID, outcome.Action.Reason))
		fmt.Fprintf(&note, "\n\nThis item covers these requests of report %s, filed at %q by the %s%s:",
			subject.ID, subject.Severity, RoleTitle(subject.Role), reportedOn(subject))
		for _, request := range covers[item] {
			fmt.Fprintf(&note, "\n- %q", request)
		}
		if _, err := s.options.Tracker.Update(ctx, item, beads.WorkItemChange{AppendNotes: note.String()}); err != nil {
			return fmt.Errorf("note on %s which of the report's requests it covers: %w", item, err)
		}
		outcome.noteLanded("noted on %s which of %s's requests it covers", item, subject.ID)
	}
	return nil
}

// mappingClause is what the line about a handling says about its mapping, and is
// nothing for a handling that mapped nothing.
func mappingClause(requests []report.Request) string {
	if len(requests) == 0 {
		return ""
	}
	parts := make([]string, 0, len(requests))
	for _, request := range requests {
		switch {
		case request.CoveredBy != "":
			parts = append(parts, fmt.Sprintf("%q covered by %s", request.Request, request.CoveredBy))
		case request.Admitted != "":
			parts = append(parts, fmt.Sprintf("%q admitted as %s", request.Request, request.Admitted))
		default:
			parts = append(parts, fmt.Sprintf("%q declined", request.Request))
		}
	}
	return "; " + strings.Join(parts, ", ")
}
