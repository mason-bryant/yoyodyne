package notify

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/report"
)

// SchemaVersion versions the envelope rather than any one producer. A new
// producer adds kinds without the envelope, the addressing, or anything reading
// it changing, so this moves only when what an envelope means moves.
const SchemaVersion = 1

// Kind is what happened. The set is the milestones an operator would otherwise
// have to be at a terminal to see, and each transition is said once: a thread is
// a narrative rather than an event log scrolling sideways.
type Kind string

const (
	// The backlog moving, which is the harness's steering wheel turning. An item
	// admitted says what it is for, because work in the queue that does not say
	// what it serves is exactly the work nobody can later decide to stop doing; a
	// decomposition is separate from an admission because they are different acts
	// by different roles, and recording one as the other would say a role admitted
	// work it has no authority to admit.
	KindItemAdmitted      Kind = "backlog.admitted"
	KindItemDecomposed    Kind = "backlog.decomposed"
	KindItemAttributed    Kind = "backlog.attributed"
	KindItemReprioritized Kind = "backlog.reprioritized"
	// What the operator decided about work an agent proposed. It is two kinds
	// rather than one with a field, for the reason the reviewer's verdict is:
	// work entering the backlog and work turned down are different news, and
	// every persona says them differently.
	KindWorkApproved Kind = "proposed-work.approved"
	KindWorkDeclined Kind = "proposed-work.declined"
	// Work leaving the run queue for a role's conversation, that role starting
	// it, and that role finishing it. They are the transitions of the one class
	// of work no run ever touches, and before they existed a thread said nothing
	// at all about it: an item rerouted to the architect and worked to completion
	// there showed a run that failed, and then silence for the rest of its life.
	//
	// Three kinds rather than one with a field, for the reason the verdict is
	// two: handed to somebody, taken up by them, and done by them are different
	// news, and the middle one is the only thing that says the routing was acted
	// on rather than merely recorded. The completion is here and nowhere else
	// because closing an item is otherwise said by the run that finished it, and
	// this is the work that has no run to say it.
	KindWorkHandedOff  Kind = "work.handed-off"
	KindWorkPickedUp   Kind = "work.picked-up"
	KindWorkCarriedOut Kind = "work.carried-out"
	// KindRunStarted carries the recorded selection reason with it, so the fact
	// the selected-work-passes-intake-and-records-why invariant makes durable is
	// the fact an operator actually reads.
	KindRunStarted Kind = "run.started"
	// The deterministic gate, said in both directions. Passing is progress and
	// failing is a repair attempt, and neither is a verdict on the change.
	KindChecksPassed Kind = "checks.passed"
	KindChecksFailed Kind = "checks.failed"
	// The reviewer's verdict is two kinds rather than one with a field, because
	// an approval and a request for repairs are different news and are said
	// differently by every persona that says them.
	KindReviewApproved Kind = "review.approved"
	KindReviewRepairs  Kind = "review.repairs"
	// The three separate facts about getting a change out: promoted locally,
	// published to a forge, and merged there. A queued merge is its own kind
	// because it is a run that finished with its publication still owed.
	KindPromoted       Kind = "promotion.made"
	KindPublished      Kind = "publication.opened"
	KindMergeQueued    Kind = "merge.queued"
	KindMergeCompleted Kind = "merge.completed"
	// A run that stopped and one that carried on. Both are said because a queue
	// that goes quiet at night is indistinguishable from a broken one until
	// something says which it is.
	KindRunParked    Kind = "run.parked"
	KindRunContinued Kind = "run.continued"
	// A blocker is work that stopped and stayed stopped, which is the one thing
	// nobody finds out about on their own.
	KindBlockerRecorded Kind = "blocker.recorded"
	// A provider refusing the harness for want of capacity, somewhere that is not
	// a run: a conversation turn, an independent review. A run says it by parking,
	// and this is the same news from every other process — hours in which nothing
	// will happen, with a cause and, where the provider named one, an end.
	KindUsageLimitExhausted Kind = "usage-limit.exhausted"
	// What an agent said in its own words: a report at its severity, a proposed
	// change to a document it does not own, a turn of an ask exchange, and the
	// exchange closing — including closing unresolved at its round cap, which
	// escalates to the operator and is exactly what this channel exists for.
	KindReportFiled    Kind = "report.filed"
	KindProposalRaised Kind = "proposal.raised"
	KindExchangeTurn   Kind = "exchange.turn"
	KindExchangeClosed Kind = "exchange.closed"
	// What a reply in a topic's thread did. They are the acknowledgment the
	// inbound half owes every message it reads: the directive as recorded with
	// its identifier, the resolution that lifted one, or the refusal with its
	// reason. Three kinds rather than one, because they are the three different
	// things an operator has to know — the work is now steered, the work is
	// moving again, or nothing was recorded at all — and a reader who could not
	// tell them apart at a glance would have to open the record to find out
	// whether they had been heard.
	KindDirectiveRecorded Kind = "directive.recorded"
	KindDirectiveResolved Kind = "directive.resolved"
	KindDirectiveRefused  Kind = "directive.refused"
	// The operator's two switches. They are about the whole line rather than any
	// one item, which is why they are addressed to the product rather than
	// buried in a thread that would misfile them.
	KindIntakeHeld     Kind = "intake.held"
	KindIntakeReleased Kind = "intake.released"
	KindHoldPlaced     Kind = "hold.placed"
	KindHoldLifted     Kind = "hold.lifted"
	// What a watch session is doing. A session that stays open until it is told
	// to stop spends most of its life saying nothing, and an idle one and a dead
	// one are the same silence: these are what tell them apart. Idle and braked
	// are separate because they need different things — idle needs work admitted,
	// braked needs somebody to look at what stopped the line — and stopping is
	// said because a session that ended quietly is the case the rest of this
	// exists to rule out.
	KindWatchStarted Kind = "watch.started"
	KindWatchIdle    Kind = "watch.idle"
	KindWatchBraked  Kind = "watch.braked"
	KindWatchResumed Kind = "watch.resumed"
	KindWatchStopped Kind = "watch.stopped"
	// A line that is choosing nothing while work is ready to be chosen. Every
	// kind above is a transition said once; this one is a state said again while
	// it stands, because the fact somebody needs is not that it began but that it
	// is still true hours later. Silence has to mean nothing to do, so a state
	// that means waiting-on-you says so periodically until it clears.
	KindLineWaiting Kind = "line.waiting"
	// What one topic gathered while nothing was posting it. Every kind above is
	// something the record says happened; this one is what a surface does with a
	// backlog it cannot say one message at a time — a long gap replayed in full
	// is hundreds of messages nobody scrolls, and the surface that carries them
	// starts dropping them. So the accumulation is said once per thread, naming
	// how much of it there is and the record that holds all of it.
	KindCatchUpDigest Kind = "catch-up.digest"
)

// Kinds is the whole reportable set, in the order work reaches them: the queue
// changing, then what a run does to one item of it, then what an agent says and
// what an operator does. A caller that has to cover every kind reads it from
// here rather than repeating the list.
func Kinds() []Kind {
	return []Kind{
		KindItemAdmitted,
		KindItemDecomposed,
		KindItemAttributed,
		KindItemReprioritized,
		KindWorkApproved,
		KindWorkDeclined,
		KindWorkHandedOff,
		KindWorkPickedUp,
		KindWorkCarriedOut,
		KindRunStarted,
		KindChecksPassed,
		KindChecksFailed,
		KindReviewApproved,
		KindReviewRepairs,
		KindPromoted,
		KindPublished,
		KindMergeQueued,
		KindMergeCompleted,
		KindRunParked,
		KindRunContinued,
		KindBlockerRecorded,
		KindUsageLimitExhausted,
		KindReportFiled,
		KindProposalRaised,
		KindExchangeTurn,
		KindExchangeClosed,
		KindDirectiveRecorded,
		KindDirectiveResolved,
		KindDirectiveRefused,
		KindIntakeHeld,
		KindIntakeReleased,
		KindHoldPlaced,
		KindHoldLifted,
		KindWatchStarted,
		KindWatchIdle,
		KindWatchBraked,
		KindWatchResumed,
		KindWatchStopped,
		KindLineWaiting,
		KindCatchUpDigest,
	}
}

// Valid reports whether a name is one of the reportable kinds. An unrecognized
// kind has no voice line in any persona, so it is refused rather than posted as
// something nobody wrote words for.
func (k Kind) Valid() bool {
	switch k {
	case KindItemAdmitted, KindItemDecomposed, KindItemAttributed, KindItemReprioritized,
		KindWorkApproved, KindWorkDeclined,
		KindWorkHandedOff, KindWorkPickedUp, KindWorkCarriedOut,
		KindRunStarted, KindChecksPassed, KindChecksFailed,
		KindReviewApproved, KindReviewRepairs,
		KindPromoted, KindPublished, KindMergeQueued, KindMergeCompleted,
		KindRunParked, KindRunContinued, KindBlockerRecorded, KindUsageLimitExhausted,
		KindReportFiled, KindProposalRaised, KindExchangeTurn, KindExchangeClosed,
		KindDirectiveRecorded, KindDirectiveResolved, KindDirectiveRefused,
		KindIntakeHeld, KindIntakeReleased, KindHoldPlaced, KindHoldLifted,
		KindWatchStarted, KindWatchIdle, KindWatchBraked, KindWatchResumed, KindWatchStopped,
		KindLineWaiting, KindCatchUpDigest:
		return true
	default:
		return false
	}
}

// TopicKind is what a thread is about. The three are the whole set: an item of
// work, an ask exchange that concerns no item, and the product line itself.
type TopicKind string

const (
	TopicWorkItem TopicKind = "work-item"
	TopicExchange TopicKind = "exchange"
	TopicProduct  TopicKind = "product"
)

// MaxTopicIDBytes bounds the identifier half of a topic key. A key is a map key
// and a thread's identity, so it is a name rather than a payload.
const MaxTopicIDBytes = 128

// MaxTopicTitleBytes bounds the title carried beside a topic, and titleCut says
// where one was cut. A title is a line somebody scans rather than a record they
// read — the whole of what an item says is in the tracker — so a title long
// enough to push the identifier off a reader's screen is cut to the part that
// names it.
const (
	MaxTopicTitleBytes = 160
	titleCut           = "…"
)

// Topic is the thread key. The primary one is the work item, because the item is
// what a narrative is about: an exchange concerning an item is addressed to that
// item rather than to itself, and only an exchange with no item gets a thread of
// its own. What belongs to the whole line and to no item is addressed to the
// product, which is the top of the channel rather than any thread.
type Topic struct {
	Kind TopicKind `json:"kind"`
	// ID names the item or exchange, and is empty for the product, which is the
	// one topic there is only ever one of.
	ID string `json:"id,omitempty"`
	// Title is what the topic is called, in the words the durable record the
	// message was read from carried. It is what makes a thread header a subject a
	// reader recognizes rather than an identifier they have to go and resolve, and
	// it is deliberately not part of the key: the same topic is the same thread
	// whatever it was called when each message was said, so an item somebody
	// renamed never opens a second one. Empty is ordinary — a record that carried
	// no title leaves the identifier to name the topic on its own.
	Title string `json:"title,omitempty"`
}

// WorkItem addresses a topic to one item of work.
func WorkItem(id string) (Topic, error) {
	topic := Topic{Kind: TopicWorkItem, ID: strings.TrimSpace(id)}
	return topic, topic.Validate()
}

// Exchange addresses a topic to an ask exchange that concerns no work item. An
// exchange that does concern one belongs in that item's thread instead.
func Exchange(id string) (Topic, error) {
	topic := Topic{Kind: TopicExchange, ID: strings.TrimSpace(id)}
	return topic, topic.Validate()
}

// Product addresses a topic to the whole line: the operator's holds, and
// anything else that is about every item rather than one of them.
func Product() Topic {
	return Topic{Kind: TopicProduct}
}

// WithTitle names a topic in words, from what the record a message was read
// from calls it. It sanitizes rather than refuses, which is the whole reason it
// is separate from the constructors: the identifier is already exact, so a title
// is what a header adds to it, and refusing an awkward one would take a work
// item's entire narrative out of the channel over the way somebody phrased its
// name. So it is folded onto one line — a header is a line — cut to the bound,
// and left absent where the record carried nothing.
func (t Topic) WithTitle(title string) Topic {
	// The product is the whole line rather than a subject somebody named, and it
	// opens no thread to head, so it is left as the one topic that is only ever
	// itself.
	if t.Kind == TopicProduct {
		return t
	}
	t.Title = boundTitle(title)
	return t
}

// boundTitle is a title as a header can carry it: one line, and short enough
// that the identifier beside it is still the first thing read.
func boundTitle(title string) string {
	folded := strings.Join(strings.Fields(title), " ")
	if len(folded) <= MaxTopicTitleBytes {
		return folded
	}
	cut := MaxTopicTitleBytes - len(titleCut)
	for cut > 0 && !utf8.RuneStart(folded[cut]) {
		cut--
	}
	return strings.TrimRight(folded[:cut], " ") + titleCut
}

// Key is the topic as one string, which is what a thread map is keyed by and
// what an inbound reply is correlated back through. The colon separator is why
// an identifier may not contain one.
func (t Topic) Key() string {
	if t.Kind == TopicProduct {
		return string(TopicProduct)
	}
	return string(t.Kind) + ":" + t.ID
}

// ParseTopic reads a key back into the topic it names, so a durable thread map
// says what each thread is about rather than only that it exists.
func ParseTopic(key string) (Topic, error) {
	trimmed := strings.TrimSpace(key)
	if trimmed == string(TopicProduct) {
		return Product(), nil
	}
	kind, id, found := strings.Cut(trimmed, ":")
	if !found {
		return Topic{}, fmt.Errorf("topic key %q names no topic", key)
	}
	topic := Topic{Kind: TopicKind(kind), ID: id}
	if err := topic.Validate(); err != nil {
		return Topic{}, err
	}
	return topic, nil
}

// Validate rejects a topic that could not name a thread. An identifier is held
// to being a name — no whitespace, no separator, bounded — because it is half of
// a key that has to round-trip.
func (t Topic) Validate() error {
	var problems []error
	switch t.Kind {
	case TopicProduct:
		if t.ID != "" {
			problems = append(problems, errors.New("the product topic names no identifier"))
		}
	case TopicWorkItem, TopicExchange:
		switch {
		case strings.TrimSpace(t.ID) == "":
			problems = append(problems, fmt.Errorf("a %s topic requires an identifier", t.Kind))
		case len(t.ID) > MaxTopicIDBytes:
			problems = append(problems, fmt.Errorf("topic identifier is %d bytes, limit is %d", len(t.ID), MaxTopicIDBytes))
		case strings.ContainsAny(t.ID, ": \t\n"):
			problems = append(problems, fmt.Errorf("topic identifier %q must not contain a separator or whitespace", t.ID))
		}
	default:
		problems = append(problems, fmt.Errorf("topic kind %q must be %q, %q, or %q",
			t.Kind, TopicWorkItem, TopicExchange, TopicProduct))
	}
	return errors.Join(problems...)
}

// HarnessSpeaker is what the speaker key says for what no persona did. It is a
// word rather than an empty role because "the harness promoted this" is a real
// account of a real act, and the one role that must never be able to claim it is
// any of the agents.
const HarnessSpeaker = "harness"

// Speaker is whose account a message is. It is the role the work was done under
// and the configured agent that filled it, or the harness for what no persona
// did — a promotion, a merge, the operator's own switches.
type Speaker struct {
	// Role is empty for the harness. Both halves are kept because a project may
	// configure more than one agent for a role, and "which developer said this"
	// is a different question from "a developer said this".
	Role  domain.AgentRole `json:"role,omitempty"`
	Agent string           `json:"agent,omitempty"`
}

// Harness is the speaker for what no persona did.
func Harness() Speaker {
	return Speaker{}
}

// Persona is the speaker for what a role did, naming the configured agent that
// filled it where the project recorded one.
func Persona(role domain.AgentRole, agent string) Speaker {
	return Speaker{Role: role, Agent: strings.TrimSpace(agent)}
}

// IsHarness reports the speaker that is not a persona at all.
func (s Speaker) IsHarness() bool {
	return s.Role == ""
}

// Key names the speaker in the envelope: a role, or the harness.
func (s Speaker) Key() string {
	if s.IsHarness() {
		return HarnessSpeaker
	}
	return string(s.Role)
}

// Validate rejects a speaker nothing has a voice for. An unrecognized role is
// refused rather than posted in some default voice, because a message in a voice
// nobody wrote is a message attributed to a persona that did not speak it.
func (s Speaker) Validate() error {
	if s.IsHarness() {
		if s.Agent != "" {
			return errors.New("the harness is not a configured agent")
		}
		return nil
	}
	if !s.Role.Valid() {
		return fmt.Errorf("speaker role %q is not one of the harness's roles", s.Role)
	}
	return nil
}

// Refs are the correlation identifiers a message carries: enough to get from
// what was said back to the record that said it. They are what makes a message
// an index into the durable account rather than a replacement for it.
type Refs struct {
	RunID          string `json:"run_id,omitempty"`
	ConversationID string `json:"conversation_id,omitempty"`
	ExchangeID     string `json:"exchange_id,omitempty"`
	WorkItemID     string `json:"work_item_id,omitempty"`
	DirectiveID    string `json:"directive_id,omitempty"`
	PullRequest    string `json:"pull_request,omitempty"`
}

// Record names the durable record this message came out of, as a reader would
// have to name it to go and find it. It is what a truncated body points at, so
// nobody reads a cut message as the whole of what was recorded.
func (r Refs) Record() string {
	switch {
	case strings.TrimSpace(r.RunID) != "":
		return r.RunID
	case strings.TrimSpace(r.ConversationID) != "":
		return r.ConversationID
	case strings.TrimSpace(r.ExchangeID) != "":
		return r.ExchangeID
	case strings.TrimSpace(r.WorkItemID) != "":
		return r.WorkItemID
	default:
		return "the durable record"
	}
}

// Detail is what a voice line has to work with beyond the kind itself. It is one
// flat record with optional fields rather than a shape per kind, for the reason
// durable run state is: every field says which kinds read it, absence is a fact
// rather than an error, and a producer that fills nothing still produces a
// message that says something true.
type Detail struct {
	// SelectedBy and SelectionReason are the recorded account of why the harness
	// is running this item, read by KindRunStarted. Absence is reported as no
	// reason recorded rather than as a blank, because an unaccounted run is
	// exactly what carrying the reason exists to make visible.
	SelectedBy      string `json:"selected_by,omitempty"`
	SelectionReason string `json:"selection_reason,omitempty"`
	// Account and Configuration are which provider account a run is spending and
	// which configuration set it up, read by KindRunStarted. They are said where
	// the thread opens because they hold for the whole of a run, and they are said
	// at all because there is one account today and the message that names it is
	// the one an operator will read on the day there are two.
	Account       string `json:"account,omitempty"`
	Configuration string `json:"configuration,omitempty"`
	// Command and ExitCode are the failing deterministic check, read by
	// KindChecksFailed. They are the check's own words rather than a summary of
	// them.
	Command  string `json:"command,omitempty"`
	ExitCode int    `json:"exit_code,omitempty"`
	// Findings is how many the reviewer raised, read by KindReviewRepairs.
	Findings int `json:"findings,omitempty"`
	// TargetBranch and Commit are where a promotion put the change, read by
	// KindPromoted.
	TargetBranch string `json:"target_branch,omitempty"`
	Commit       string `json:"commit,omitempty"`
	// PullRequest is how the published request is named to a reader, read by the
	// three publication kinds.
	PullRequest string `json:"pull_request,omitempty"`
	// Cause is what a parked run is waiting on, read by KindRunParked, and what a
	// provider refused for, read by KindUsageLimitExhausted. It is a phrase
	// rather than a code because what an operator needs is the answer to "waiting
	// on what", and the causes do not share a shape: a deadline, an overloaded
	// server, the operator themselves, an unresolved directive.
	Cause string `json:"cause,omitempty"`
	// Waiting is what a provider's refusal stopped, read by
	// KindUsageLimitExhausted. A run names itself, so this is for everything that
	// is not one: which conversation, which review. It is what turns "the
	// provider is out of capacity" into news somebody can act on.
	Waiting string `json:"waiting,omitempty"`
	// Round and Rounds are where an ask exchange has got to against its cap, read
	// by KindExchangeTurn and KindExchangeClosed.
	Round  int `json:"round,omitempty"`
	Rounds int `json:"rounds,omitempty"`
	// Unresolved is what an exchange closed without settling, read by
	// KindExchangeClosed. Empty means it closed resolved, which is the ordinary
	// way for one to end.
	//
	// It is read a second time by KindDirectiveRecorded, where it is what the
	// recorded directive left for somebody to settle — and therefore the whole of
	// what says whether the work it affects is paused. Empty there is the
	// operational directive, which is in force already and stops nothing.
	Unresolved string `json:"unresolved,omitempty"`
	// Artifact is the document a proposal is about, read by KindProposalRaised,
	// and the governed document an artifact-changing directive rewrites, read by
	// KindDirectiveRecorded.
	Artifact string `json:"artifact,omitempty"`
	// ReceivedBy is the role a directive was addressed to, read by
	// KindDirectiveRecorded. It is attribution rather than routing — the record
	// reaches every role whichever one is named — and it is said because an
	// operator who addressed the reviewer wants to see that the reviewer is who
	// the record says they told.
	ReceivedBy string `json:"received_by,omitempty"`
	// Title is what an item is called, read by the kinds that report one arriving
	// in the backlog. An identifier says which item; the title is what makes a
	// thread readable by somebody who has not read the tracker.
	Title string `json:"title,omitempty"`
	// Goal is what the work says it serves, read by the admission kinds and by
	// KindItemAttributed. It is the goal in the words the goals document states
	// it in, which is what makes the claim checkable rather than decorative.
	Goal string `json:"goal,omitempty"`
	// Parent is the admitted item a decomposition was created under, read by
	// KindItemDecomposed. "Created under what" is the whole of what makes a
	// decomposition auditable.
	Parent string `json:"parent,omitempty"`
	// Priority is where in the queue a reprioritization put an item, read by
	// KindItemReprioritized. It is negative where the record did not say, because
	// zero is the highest priority rather than an unstated one.
	Priority int `json:"priority,omitempty"`
	// Executor is what carries an item where a developer run does not, read by
	// the three handoff kinds and by the admission kinds. It is also what decides
	// whose move follows an admission: work marked for a conversation is not
	// waiting for a run and never will be, so a thread that said it was waiting
	// for one would be telling the reader to expect something that cannot come.
	// Empty is the ordinary case, which is work a developer run carries.
	Executor string `json:"executor,omitempty"`
	// Stopped, Since, and Ready are read by KindLineWaiting: what has stopped the
	// harness choosing work, when it became that way, and how much admitted work
	// the tracker calls ready behind it. The age is rendered from Since against
	// the event's own moment rather than carried as a phrase, because a state
	// that is said again every hour has a different age every time it is said and
	// a timestamp a reader has to subtract from is not the fact they need.
	//
	// Since is read a second time by KindCatchUpDigest, where it is the first of
	// the events the digest stands for: the same subtraction against the event's
	// own moment says how much of a gap was digested away.
	Stopped string    `json:"stopped,omitempty"`
	Since   time.Time `json:"since,omitempty"`
	Ready   int       `json:"ready,omitempty"`
	// Accumulated is how many events one topic gathered while nothing was posting
	// them, read by KindCatchUpDigest. It is the whole of what the digest claims:
	// how much there is, and therefore how much of the thread's narrative is in
	// the durable record rather than in the channel.
	Accumulated int `json:"accumulated,omitempty"`
	// Reason is why: why the operator held something, read by KindIntakeHeld; why
	// a role changed the backlog, read by the tracker kinds; why proposed work
	// was turned down, read by KindWorkDeclined; and why a thread reply recorded
	// nothing, read by KindDirectiveRefused. An operator who holds in a hurry
	// owes nobody an explanation, so absence is ordinary.
	Reason string `json:"reason,omitempty"`
}

// Event is one reportable thing the record says happened. It carries no words of
// its own beyond what an agent actually wrote: the sentence is the persona's,
// rendered from the kind and the detail, so two events of the same kind from the
// same persona always read the same way.
type Event struct {
	Kind Kind `json:"kind"`
	// At is when the record says it happened, which is not when it was posted:
	// the sink catches up from its cursors, so a message can arrive long after
	// the moment it describes and must still date itself honestly.
	At time.Time `json:"at"`
	// Severity is how much attention this is asking for, in the reports
	// vocabulary. It is one vocabulary rather than two because an operator
	// reading a channel should not have to learn which words mean what depending
	// on which producer said them.
	Severity report.Severity `json:"severity"`
	Refs     Refs            `json:"refs"`
	Detail   Detail          `json:"detail,omitempty"`
	// Text is what an agent wrote, and it is carried into the message unchanged.
	// It is already redacted, because events are redacted before they are
	// persisted and nothing here reads anything else. A kind that carries no
	// authored text leaves it empty.
	Text string `json:"text,omitempty"`
}

// Validate rejects an event nothing could be said about. What it does not check
// is which fields a kind filled: absence is rendered as a stated absence rather
// than refused, because a record that is missing something is still a record
// somebody has to be told about.
func (e Event) Validate() error {
	var problems []error
	if !e.Kind.Valid() {
		problems = append(problems, fmt.Errorf("kind %q is not a reportable event", e.Kind))
	}
	if !e.Severity.Valid() {
		problems = append(problems, fmt.Errorf("severity %q must be %q, %q, or %q",
			e.Severity, report.SeverityCritical, report.SeverityWarning, report.SeverityNote))
	}
	if e.At.IsZero() {
		problems = append(problems, errors.New("at is required"))
	}
	if err := errors.Join(problems...); err != nil {
		return fmt.Errorf("invalid event: %w", err)
	}
	return nil
}

// Message is the envelope, and it is the whole of what a poster is given: what
// happened, which thread it belongs in, whose account it is and how that persona
// appears, how much attention it is asking for, the words, and the way back to
// the record.
type Message struct {
	SchemaVersion int    `json:"schema_version"`
	Kind          Kind   `json:"kind"`
	Topic         string `json:"topic"`
	// TopicTitle is what the topic is called, carried beside the key rather than
	// inside it so a surface opening a thread can name the subject in words while
	// still addressing it by the key alone. Absent where the record carried no
	// title, which reads as the identifier naming the topic by itself.
	TopicTitle string          `json:"topic_title,omitempty"`
	Speaker    string          `json:"speaker"`
	Identity   Identity        `json:"identity"`
	Severity   report.Severity `json:"severity"`
	Body       string          `json:"body"`
	Refs       Refs            `json:"refs"`
	At         time.Time       `json:"at"`
}

// Validate rejects a message that could not be posted as an account of anything.
func (m Message) Validate() error {
	var problems []error
	if m.SchemaVersion != SchemaVersion {
		problems = append(problems, fmt.Errorf("schema_version must be %d", SchemaVersion))
	}
	if !m.Kind.Valid() {
		problems = append(problems, fmt.Errorf("kind %q is not a reportable event", m.Kind))
	}
	if _, err := ParseTopic(m.Topic); err != nil {
		problems = append(problems, err)
	}
	if strings.TrimSpace(m.Speaker) == "" {
		problems = append(problems, errors.New("speaker is required"))
	}
	if err := m.Identity.Validate(); err != nil {
		problems = append(problems, err)
	}
	if !m.Severity.Valid() {
		problems = append(problems, fmt.Errorf("severity %q is invalid", m.Severity))
	}
	if strings.TrimSpace(m.Body) == "" {
		problems = append(problems, errors.New("body is required"))
	}
	if m.At.IsZero() {
		problems = append(problems, errors.New("at is required"))
	}
	if err := errors.Join(problems...); err != nil {
		return fmt.Errorf("invalid message: %w", err)
	}
	return nil
}
