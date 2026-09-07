// Package backlogrepair is the backlog state the records have made stale, and
// what may be corrected about it.
//
// Three things in a tracker stop describing the world without anybody having
// changed them. A status is written when work stops and is never rewritten when
// what stopped it clears, so an item whose blocking run ended reads as blocked
// forever. A dependency records that one item waits for another and goes on
// recording it after that other item closes. And an attribution stops resolving:
// an item that recorded its goal in the document's words rather than by the
// goal's identity names words nobody states once the document is reworded, an
// item that named a goal since retired or removed names a goal that is not in
// force whichever way it named it, and an item whose notes were replaced carries
// nothing at all where the tracker witnesses that it once did. Re-wording a goal
// an item named by identity is deliberately not among them — that is what the
// identity is for — so what this acts on is the attribution the goals cannot
// resolve rather than every item an amendment touched. None of the three is a
// decision anybody owes: the records already say what is true, and the tracker
// is simply still saying something else.
//
// # Nothing here decides, and nothing here acts
//
// This says what is stale and what makes it stale, from the records themselves.
// Whether to correct one is the product manager's, and correcting it is the
// harness's under the capability the product manager holds for it. What that
// separation buys is that an act can be judged twice against the same rule: once
// when a pass reports what it found, and again as the act is carried out against
// the records as they stand then rather than as the pass saw them.
//
// # A governance hold is reported and never repaired
//
// Some of this work is held for a person: an escalation waiting on a decision,
// a change that exists only on a preserved branch, a publication that never
// finished, a directive in force over the item. The first is the sharpest of
// them, because the harness's own triage blocks an item in order to escalate it
// and leaves no dependency behind — so an escalated item reads as a status with
// nothing at all standing behind it, and what stops it being cleared is the hold
// and nothing else. Such an item can look exactly as stale as any other — a blocked status
// with nothing unfinished behind it is what a preserved stoppage leaves — and
// correcting it would release work that somebody still has to decide about. So a
// held item is reported with the reason restated and is never among the repairs,
// and the reading itself decides that: holds that could not be read hold
// everything, because a reader that cannot tell a hold from a stale status must
// not clear either.
package backlogrepair

import (
	"fmt"
	"sort"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/backlog"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/directive"
	"github.com/mason-bryant/yoyodyne/internal/goal"
)

// Class is which kind of stale backlog state one repair is about. The three are
// kept apart because they are found in different records and corrected by
// different writes, and because what makes each one stale is a different
// sentence: a status is stale against the dependency graph, a link against the
// item it names, an attribution against the goals document.
type Class string

const (
	// ClassStatus is a status of blocked with nothing unfinished behind it.
	ClassStatus Class = "status"
	// ClassDependency is a recorded dependency on work that closed.
	ClassDependency Class = "dependency"
	// ClassAttribution is an attribution the goals cannot resolve: a goal an item
	// names that they do not state, and a goal the tracker witnessed on an item
	// whose notes no longer carry it.
	ClassAttribution Class = "attribution"
)

// classes lists the classes in the order a report states them, so a listing and
// a refusal name them the same way.
var classes = []Class{ClassStatus, ClassDependency, ClassAttribution}

// Classes is every class of stale state this recognizes, in report order.
func Classes() []Class { return append([]Class(nil), classes...) }

// Valid reports a class this recognizes. A word outside the set names no rule
// here, so an act carrying one is refused rather than run as whichever class it
// resembles.
func (c Class) Valid() bool {
	for _, known := range classes {
		if c == known {
			return true
		}
	}
	return false
}

func (c Class) String() string { return string(c) }

// Records is what a judgement is made against: the admitted work as the tracker
// holds it, what the harness is holding for a person, the directives in force,
// and the goals the repository records.
//
// Finished is what the caller has read about work outside the admitted listing,
// and it is how a dependency is known to be dead where the listing's own edge
// does not say. It is deliberately not filled in by absence: an identifier the
// admitted work does not carry may be closed, claimed, or a name nothing
// answers to, and removing a link on the strength of not having found something
// would retire the dependency of an item somebody is working on right now.
//
// The status class rests on the same evidence, which is what stops the two
// classes in this file disagreeing about what absence means. Admitted is the
// open and blocked work, so a blocker somebody is running right now is in
// neither — and a status cleared because its blocker was not in a listing that
// never carried it is a durable write made on nothing. So a status is corrected
// only where every link it records is one the tracker's own listing calls
// closed or the caller went and read.
type Records struct {
	Admitted   []beads.WorkItem
	Held       backlog.Holds
	Directives []directive.Directive
	Goals      goal.Set
	Finished   map[string]struct{}
}

// Repair is one piece of stale backlog state, and what the records say made it
// stale.
type Repair struct {
	WorkItemID string `json:"work_item_id"`
	Title      string `json:"title,omitempty"`
	Class      Class  `json:"class"`
	// Stale is what makes the old state stale, in the records' own terms. It is
	// carried rather than left to be re-derived because it is what the act
	// records about itself: an item whose state was corrected says what it was
	// corrected against, so a correction made wrongly is visible rather than
	// silent.
	Stale string `json:"stale"`
	// DependsOn names the closed item a dependency repair unlinks, and is empty
	// on the other two classes.
	DependsOn string `json:"depends_on,omitempty"`
}

// Held is one item whose state looks stale and which somebody still has to
// release. It carries the same account of the staleness a repair would, so a
// reader can see what is not being corrected as well as why.
type Held struct {
	WorkItemID string `json:"work_item_id"`
	Title      string `json:"title,omitempty"`
	Class      Class  `json:"class"`
	Stale      string `json:"stale"`
	// Reason is what somebody has to release, restated from the record that
	// holds it rather than summarized: what a reader does about it depends on
	// which of the holds it is.
	Reason string `json:"reason"`
}

// Report is what one reading found.
type Report struct {
	Repairs []Repair `json:"repairs,omitempty"`
	Holds   []Held   `json:"holds,omitempty"`
}

// Anything reports a reading that found something worth saying.
func (r Report) Anything() bool { return len(r.Repairs) > 0 || len(r.Holds) > 0 }

// HoldError reports a repair refused because the item is held for a person. It
// is a type rather than a sentence so a caller can say the thing this boundary
// exists for — that the hold was reported and the item was left exactly as it
// was — instead of reporting it as a repair that failed.
type HoldError struct {
	WorkItemID string
	Reason     string
}

func (e *HoldError) Error() string {
	return fmt.Sprintf("%s is held for a person and nothing here touches it: %s", e.WorkItemID, e.Reason)
}

// Survey reads the admitted work and reports what has gone stale about it, and
// what is held. Every item is judged for every class, so an item with a stale
// status and a dead dependency is two entries rather than whichever was found
// first: they are corrected by different writes and either may be refused on its
// own.
//
// Nothing here fails. A reading whose holds could not be read reports every item
// it would otherwise have offered as held, which is the whole of what a failed
// reading changes about the answer.
//
// A survey reads no work outside the listings it was given, so a blocked item
// with a link nothing in those listings settles is not offered here. That is the
// safe direction and not a gap: the act reads that link itself, so what a survey
// under-reports an act can still establish, and what a survey over-reported
// would be a correction proposed on nothing.
func Survey(records Records) Report {
	var report Report
	for _, item := range records.Admitted {
		for _, class := range classes {
			for _, candidate := range judgements(item, class, records) {
				if reason, held := heldFor(item.ID, records); held {
					report.Holds = append(report.Holds, Held{
						WorkItemID: candidate.WorkItemID,
						Title:      candidate.Title,
						Class:      candidate.Class,
						Stale:      candidate.Stale,
						Reason:     reason,
					})
					continue
				}
				report.Repairs = append(report.Repairs, candidate)
			}
		}
	}
	return report
}

// Judge answers whether one repair is still the right act, over the records as
// they stand now. It is the same rule the survey reports from, asked again at
// the moment of the act: a pass that found a stale status minutes ago is not
// evidence that the status is stale now, and this is what makes the act rest on
// the records rather than on the reading that proposed it.
func Judge(item beads.WorkItem, class Class, dependsOn string, records Records) (Repair, error) {
	if !class.Valid() {
		return Repair{}, fmt.Errorf("%q is not a kind of stale state this corrects; they are %s", class, NamedClasses())
	}
	if reason, held := heldFor(item.ID, records); held {
		return Repair{}, &HoldError{WorkItemID: item.ID, Reason: reason}
	}
	found := judgements(item, class, records)
	if len(found) == 0 {
		return Repair{}, fmt.Errorf("nothing about %s's %s is stale: %s", item.ID, class, why(item, class, records))
	}
	if class != ClassDependency {
		return found[0], nil
	}
	wanted := strings.TrimSpace(dependsOn)
	for _, candidate := range found {
		if candidate.DependsOn == wanted {
			return candidate, nil
		}
	}
	return Repair{}, fmt.Errorf("%s does not record a dependency on %s that the tracker says is finished; the dead links it records are %s",
		item.ID, wanted, namedLinks(found))
}

// NamedClasses names the classes the way a refusal has to read.
func NamedClasses() string {
	named := make([]string, 0, len(classes))
	for _, class := range classes {
		named = append(named, string(class))
	}
	return strings.Join(named, ", ")
}

// judgements is every repair of one class this item offers, which is one for
// the two classes about the item itself and one per dead link for the third.
func judgements(item beads.WorkItem, class Class, records Records) []Repair {
	switch class {
	case ClassStatus:
		return staleStatus(item, records)
	case ClassDependency:
		return deadDependencies(item, records)
	case ClassAttribution:
		return orphanedAttribution(item, records)
	default:
		return nil
	}
}

// staleStatus is a status of blocked that every record says nothing stands
// behind. It asks two questions rather than one, and both have to answer.
//
// The first is the reading the backlog orders the queue by and the claim
// corrects a refusal on: does this item wait on work that is still unfinished.
// The second is what makes this a durable write rather than a selection: is
// every link it records one something actually says is finished. They differ
// exactly where a listing carries a link and says nothing about what became of
// it, and the item it names is in no listing the caller read — which is what a
// blocker somebody is running right now looks like from here, since the admitted
// work is the open and the blocked and a claimed item is neither.
func staleStatus(item beads.WorkItem, records Records) []Repair {
	if !blocked(item) {
		return nil
	}
	if len(item.WaitingOn(unfinished(records))) > 0 || len(unsettledLinks(item, records)) > 0 {
		return nil
	}
	return []Repair{{
		WorkItemID: item.ID,
		Title:      item.Title,
		Class:      ClassStatus,
		Stale:      "its status says blocked and nothing unfinished blocks it; a status is written when work stops and is never rewritten when what stopped it clears",
	}}
}

// unsettledLinks is every blocking link this item records that nothing the
// caller read calls finished. A link the listing itself calls closed is settled,
// and so is one whose item the caller went and read; anything else is a link
// whose fate this does not know, which is not the same thing as a link to work
// that is done.
func unsettledLinks(item beads.WorkItem, records Records) []string {
	var unsettled []string
	for _, dependency := range item.Dependencies {
		if dependency.Type != beads.BlocksDependency {
			continue
		}
		id := strings.TrimSpace(dependency.ID)
		if id == "" || dependency.Status == closedStatus {
			continue
		}
		if _, read := records.Finished[id]; read {
			continue
		}
		unsettled = append(unsettled, id)
	}
	sort.Strings(unsettled)
	return unsettled
}

// deadDependencies is every blocking link this item records on work the tracker
// says is finished. The evidence is the tracker's, in one of the two places it
// gives one: the listing's own edge, or an item the caller read.
func deadDependencies(item beads.WorkItem, records Records) []Repair {
	var dead []Repair
	for _, dependency := range item.Dependencies {
		if dependency.Type != beads.BlocksDependency {
			continue
		}
		id := strings.TrimSpace(dependency.ID)
		if id == "" {
			continue
		}
		var evidence string
		switch _, read := records.Finished[id]; {
		case dependency.Status == closedStatus:
			evidence = "the tracker's own listing of the dependency says so"
		case read:
			evidence = "the tracker reports that item as closed"
		default:
			continue
		}
		dead = append(dead, Repair{
			WorkItemID: item.ID,
			Title:      item.Title,
			Class:      ClassDependency,
			DependsOn:  id,
			Stale:      fmt.Sprintf("it records a dependency on %s, which is closed: %s", id, evidence),
		})
	}
	sort.Slice(dead, func(first, second int) bool { return dead[first].DependsOn < dead[second].DependsOn })
	return dead
}

// orphanedAttribution is an item whose recorded goal no longer resolves: one
// naming something the goals do not state, and one the tracker witnessed a goal
// on whose notes no longer carry it.
//
// Whether it resolves is the goals' answer and not this one, which is what keeps
// this correct as that answer gets better. An attribution recorded by identity
// survives a rewording and an attribution recorded in words does not; both stop
// resolving when the goal itself leaves. Asking here would be a second reading of
// the same question, and the two would come apart the first time either changed.
//
// Work that never named a goal is deliberately not here. Nothing about it has
// gone stale — it was admitted before goals were checked — and attributing it is
// a judgement about what the work is for rather than a correction, which is what
// the attribute action already is.
func orphanedAttribution(item beads.WorkItem, records Records) []Repair {
	attribution := records.Goals.AttributionOf(item.Notes, item.GoalWitness)
	if !attribution.Divergent() {
		return nil
	}
	stale := fmt.Sprintf("it names the goal %q and the goals no longer state it", singleLine(attribution.Named))
	if attribution.State == goal.StateLost {
		stale = fmt.Sprintf("the tracker witnessed the goal %q on it and its notes no longer carry one", singleLine(attribution.Recorded))
	}
	return []Repair{{
		WorkItemID: item.ID,
		Title:      item.Title,
		Class:      ClassAttribution,
		Stale:      stale,
	}}
}

// why says what the records actually say about an item whose state a repair
// claimed was stale. It is the other half of a refusal: an act refused for
// nothing to correct has to say what the state is instead, because that is what
// the caller reasons from next.
func why(item beads.WorkItem, class Class, records Records) string {
	switch class {
	case ClassStatus:
		if !blocked(item) {
			return fmt.Sprintf("the tracker holds it at %q rather than blocked, so there is no blocked status to clear", item.Status)
		}
		if waiting := item.WaitingOn(unfinished(records)); len(waiting) > 0 {
			return fmt.Sprintf("it is blocked and waits on unfinished work: %s", strings.Join(waiting, ", "))
		}
		return fmt.Sprintf("nothing read here says what became of the work it waits for (%s), and an identifier the admitted work does not carry may be closed, claimed, or a name nothing answers to",
			strings.Join(unsettledLinks(item, records), ", "))
	case ClassDependency:
		return "it records no dependency the tracker says is finished"
	case ClassAttribution:
		attribution := records.Goals.AttributionOf(item.Notes, item.GoalWitness)
		if attribution.State == goal.StateUnattributed {
			return "it names no goal at all, which is work admitted before goals were checked rather than an attribution that stopped resolving; \"attribute\" is what records one"
		}
		if reason, uncheckable := records.Goals.Uncheckable(); uncheckable {
			return "nothing could check what it names: " + reason
		}
		return "the goal it names is one the goals state"
	default:
		return "there is no such state"
	}
}

// heldFor is what somebody has to release before this item is touched at all,
// and whether anybody does. It answers from the harness's own record of stopped
// work and from the directives, which are the two places a hold is kept.
//
// A reading that never happened holds everything. The zero Holds is exactly that
// reading, and it is the one case where saying "nothing is held" would be a
// statement about the reader rather than about the work.
func heldFor(workItemID string, records Records) (string, bool) {
	if !records.Held.Read() {
		return "what the harness is holding for a person could not be read, so nothing here can tell a stale status from a stoppage somebody has to decide about", true
	}
	if reason, held := records.Held.Reason(workItemID); held {
		return reason, true
	}
	for _, recorded := range records.Directives {
		if recorded.Pauses() && recorded.Affects(workItemID) {
			return fmt.Sprintf("directive %s pauses the work it affects until somebody settles what it is waiting for: %s",
				recorded.ID, singleLine(recorded.Unresolved)), true
		}
	}
	return "", false
}

// unfinished is the admitted work that is not finished, which is what says
// whether a dependency an item records is still somebody's wait. It is the same
// set the backlog and the claim judge a blocked status against.
func unfinished(records Records) map[string]struct{} {
	admitted := make(map[string]struct{}, len(records.Admitted))
	for _, item := range records.Admitted {
		admitted[item.ID] = struct{}{}
	}
	return admitted
}

// The tracker statuses this reasons about: the one a stale status is written in,
// and the one that says a dependency is nobody's wait.
const (
	blockedStatus = "blocked"
	closedStatus  = "closed"
)

func blocked(item beads.WorkItem) bool {
	return strings.TrimSpace(item.Status) == blockedStatus
}

// namedLinks names the dead dependencies an item does record, so a refusal over
// one it does not says what was there instead.
func namedLinks(found []Repair) string {
	if len(found) == 0 {
		return "none"
	}
	named := make([]string, 0, len(found))
	for _, candidate := range found {
		named = append(named, candidate.DependsOn)
	}
	return strings.Join(named, ", ")
}

// singleLine folds tracker or operator prose into one line, so a sentence this
// writes onto an item stays one sentence.
func singleLine(text string) string {
	return strings.Join(strings.Fields(text), " ")
}
