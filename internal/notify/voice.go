package notify

// How a persona sounds, and how it appears.
//
// One thread per topic draws the boundary between subjects; this draws the
// boundary between speakers. A channel where five roles and the harness all
// report in the same flat sentence is one where the only thing separating them
// is a header, and a header is the first thing a reader stops reading. So each
// persona has its own line for each reportable kind: the developer speaks in the
// first person about the change and the evidence, the reviewer judges what it
// did not write, the development manager talks about the queue, the product
// manager about intent, the architect about the shape of the thing, and the
// harness says only what happened, with no first person and no opinion, because
// what it reports is what no persona did.
//
// The lines are data and nothing renders them but substitution. There is no
// provider call anywhere on this path: voicing an event through a model would
// price visibility at a call per event, make the channel nondeterministic, and
// put a model between a fact and its reporting. What an agent actually wrote is
// substituted in whole, so the genuine voice is carried rather than described.

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/report"
)

// Identity is how a persona appears on its own messages: the name a reader sees
// and the avatar beside it. It is per message rather than per connection because
// one sink posts for every persona, and a workspace where they all appear as one
// app is a workspace where the voice is the only thing telling them apart.
type Identity struct {
	Name string `json:"name"`
	// Avatar is an emoji shortcode, or the URL of an image where a project
	// configured one. It is decoration in the strict sense: everything it
	// distinguishes is already distinguished by the name beside it and the voice
	// below it, so a reader whose client will not render it loses nothing but the
	// picture. Which of the two it is, and how a surface carries each, is that
	// surface's business rather than this table's.
	Avatar string `json:"avatar"`
}

func (i Identity) Validate() error {
	var problems []error
	if strings.TrimSpace(i.Name) == "" {
		problems = append(problems, errors.New("identity name is required"))
	}
	if strings.TrimSpace(i.Avatar) == "" {
		problems = append(problems, errors.New("identity avatar is required"))
	}
	return errors.Join(problems...)
}

// Identity is how this speaker appears. The configured agent is named only where
// it says something the role does not, which is a project that configured more
// than one agent for a role — the same rule a collected report is rendered under,
// so the same speaker is named the same way wherever it is read.
func (s Speaker) Identity() Identity {
	spoken, ok := voices[s.Key()]
	if !ok {
		return Identity{}
	}
	name := spoken.title
	if agent := strings.TrimSpace(s.Agent); agent != "" && agent != string(s.Role) {
		name += " (" + agent + ")"
	}
	return Identity{Name: name, Avatar: spoken.avatar}
}

// Avatars is a project's override of the picture beside a speaker's name, keyed
// exactly as an envelope names a speaker: a role, or the harness.
//
// It is the only part of an identity configuration may move, and that boundary
// is the point. The name a message appears under, and whose account it is, stay
// the voice table's: who speaks is a claim about who did the work, and a project
// that could rewrite it could attribute a promotion to a developer. The avatar
// is what the design calls strict decoration, so it is the part that can be a
// preference without any of that riding on it.
type Avatars map[string]string

// Identity is how a speaker appears once this project's overrides are applied.
// An entry that is absent or blank leaves the shipped default, so configuring
// one persona's avatar does not quietly un-decorate the rest.
func (a Avatars) Identity(speaker Speaker) Identity {
	identity := speaker.Identity()
	if override := strings.TrimSpace(a[speaker.Key()]); override != "" {
		identity.Avatar = override
	}
	return identity
}

// voice is one persona's own way of saying what the record holds: how it
// appears, and its line for every reportable kind. Every persona has a line for
// every kind, with no shared fallback anywhere, because a persona falling back
// to the harness's flat sentence would be a message attributed to somebody who
// does not sound like that.
type voice struct {
	title  string
	avatar string
	lines  map[Kind]string
}

// The harness: what no persona did. Flat, no first person, no judgment. It is
// the voice a promotion is reported in, and a promotion is the harness's own act
// by invariant — no agent performs one.
var harnessVoice = voice{
	title:  "Yoyodyne",
	avatar: ":gear:",
	lines: map[Kind]string{
		KindItemAdmitted:        "{item} was admitted to the backlog: {title}. It serves: {goal}",
		KindItemDecomposed:      "{item} was created under {parent}, decomposing it: {title}",
		KindItemAttributed:      "{item} was attributed to a goal: {goal}",
		KindItemReprioritized:   "{item} was set to {priority}.",
		KindWorkApproved:        "The operator approved proposed work, and it was admitted as {item}: {title}. It serves: {goal}",
		KindWorkDeclined:        "The operator declined proposed work — {title} — because: {why}",
		KindRunStarted:          "{item} claimed and started as {run}. Selected by {by}: {reason}",
		KindChecksPassed:        "Checks passed on {item}.",
		KindChecksFailed:        "Checks failed on {item}: {command} exited {exit}.",
		KindReviewApproved:      "Review of {item} approved.",
		KindReviewRepairs:       "Review of {item} asked for repairs: {findings}.",
		KindPromoted:            "{item} promoted onto {branch} at {commit}.",
		KindPublished:           "{item} published as {pr}.",
		KindMergeQueued:         "The merge of {pr} is queued on the forge.",
		KindMergeCompleted:      "{pr} merged.",
		KindRunParked:           "{run} parked on {item}, waiting on {cause}.",
		KindRunContinued:        "{run} continued on {item}.",
		KindBlockerRecorded:     "{item} is blocked: {text}",
		KindUsageLimitExhausted: "The provider refused {waiting}: {cause}.",
		KindReportFiled:         "A report was filed on {item}: {text}",
		KindProposalRaised:      "A change to {artifact} is proposed: {text}",
		KindExchangeTurn:        "{exchange}, {rounds}: {text}",
		KindExchangeClosed:      "{exchange} closed {outcome}.",
		KindIntakeHeld:          "Intake is held for this product: {why}",
		KindIntakeReleased:      "Intake is released for this product.",
		KindHoldPlaced:          "All harness activity is held.",
		KindHoldLifted:          "The hold on harness activity is lifted.",
		KindWatchStarted:        "A watch session is open on this product: {why}",
		KindWatchIdle:           "The watch session found nothing to start and is polling: {why}",
		KindWatchBraked:         "The watch session is choosing nothing while intake is held: {why}",
		KindWatchResumed:        "The watch session is choosing work again: {why}",
		KindWatchStopped:        "The watch session ended: {why}",
	},
}

// The developer: one bounded item, first person, evidence somebody else can
// verify.
var developerVoice = voice{
	title:  "Developer",
	avatar: ":hammer_and_wrench:",
	lines: map[Kind]string{
		KindItemAdmitted:        "{item} is in the backlog and could reach me one day: {title}, admitted for {goal}.",
		KindItemDecomposed:      "{parent} was broken down, and {item} is one of the pieces I could be handed: {title}.",
		KindItemAttributed:      "{item} now says what it is for: {goal}. That is the intent I'd be building against.",
		KindItemReprioritized:   "{item} sits at {priority} now. What I build doesn't change with the order it is queued in.",
		KindWorkApproved:        "The operator approved that one, so {item} is work somebody will be given: {title}, for {goal}.",
		KindWorkDeclined:        "Proposed work was turned down before it reached anybody — {title} — because: {why}",
		KindRunStarted:          "I've picked up {item} as {run}. It came to me from {by}: {reason}",
		KindChecksPassed:        "Checks are green on {item}. I ran them before calling anything done.",
		KindChecksFailed:        "Checks are red on {item}: {command} exited {exit}. That is my next attempt, not somebody else's problem.",
		KindReviewApproved:      "The reviewer approved my change on {item}.",
		KindReviewRepairs:       "{item} came back to me with {findings}. I'll take them as written.",
		KindPromoted:            "My change on {item} is on {branch}, at {commit}.",
		KindPublished:           "I've published the change on {item} as {pr}.",
		KindMergeQueued:         "{pr} is queued to merge; there is nothing more from me on {item}.",
		KindMergeCompleted:      "{pr} is merged, so {item} is out of my hands.",
		KindRunParked:           "I've stopped mid-change on {item}, waiting on {cause}. The worktree keeps everything.",
		KindRunContinued:        "Back on {item}, picking the change up where I left it.",
		KindBlockerRecorded:     "I could not finish {item}: {text}",
		KindUsageLimitExhausted: "The provider has nothing left for {waiting}: {cause}. None of my work moves until it lifts.",
		KindReportFiled:         "Something I noticed on {item} while the work itself went fine: {text}",
		KindProposalRaised:      "{artifact} isn't mine to edit, so I'm proposing the change instead: {text}",
		KindExchangeTurn:        "On {exchange}, {rounds}: {text}",
		KindExchangeClosed:      "{exchange} closed {outcome}. Back to the change.",
		KindIntakeHeld:          "Intake is held, so nothing new reaches me: {why}",
		KindIntakeReleased:      "Intake is open again; I'll take what I'm given.",
		KindHoldPlaced:          "Held before my next provider call. Nothing of the change is lost.",
		KindHoldLifted:          "The hold is lifted; I'm carrying on.",
		KindWatchStarted:        "Work can reach me without anybody typing an identifier now: {why}",
		KindWatchIdle:           "There's nothing queued for me to pick up, so I'm waiting rather than working: {why}",
		KindWatchBraked:         "Nothing is being handed to me while intake is held: {why}",
		KindWatchResumed:        "Work is being handed out again, and I'll take what I'm given: {why}",
		KindWatchStopped:        "Nothing more will be handed to me until somebody starts it again: {why}",
	},
}

// The reviewer: judges one change against the item that asked for it, did not
// write it, does not fix it.
var reviewerVoice = voice{
	title:  "Reviewer",
	avatar: ":mag:",
	lines: map[Kind]string{
		KindItemAdmitted:        "{item} was admitted: {title}. What it serves — {goal} — is what I will judge a change against.",
		KindItemDecomposed:      "{item} was cut out of {parent}: {title}. Narrower work is work I can judge as finished or not.",
		KindItemAttributed:      "{item} records the goal it serves: {goal}. Intent I can read beats intent I have to infer.",
		KindItemReprioritized:   "{item} moved to {priority}. The order work arrives in changes nothing about the standard it meets.",
		KindWorkApproved:        "Approved and admitted as {item}: {title}, serving {goal}. I'll see it when a change comes back from it.",
		KindWorkDeclined:        "{title} was declined, so there is no change coming and nothing for me to judge: {why}",
		KindRunStarted:          "{item} is under way as {run}, chosen by {by}: {reason}. I'll judge what comes back rather than how it got here.",
		KindChecksPassed:        "The checks behind {item} pass. Passing checks are evidence, not a verdict.",
		KindChecksFailed:        "The checks behind {item} fail: {command} exited {exit}. There is nothing for me to judge yet.",
		KindReviewApproved:      "I approve {item}: correct and complete against the criteria it was given.",
		KindReviewRepairs:       "I'm asking for repairs on {item}: {findings}, each one specific enough to act on.",
		KindPromoted:            "The change I approved on {item} is on {branch} at {commit}.",
		KindPublished:           "{item} is published as {pr}. My verdict was on the change, not on the request.",
		KindMergeQueued:         "{pr} is queued to merge with my approval behind it.",
		KindMergeCompleted:      "{pr} is merged, so what I approved is what landed.",
		KindRunParked:           "{item} stopped before there was a verdict, waiting on {cause}.",
		KindRunContinued:        "{item} is moving again; I'll see the change when it is ready.",
		KindBlockerRecorded:     "{item} is blocked and there is no change to judge: {text}",
		KindUsageLimitExhausted: "{waiting} was refused for want of provider capacity: {cause}. Nothing arrives for a verdict until it lifts.",
		KindReportFiled:         "Worth knowing about {item} — what I saw rather than what I judged: {text}",
		KindProposalRaised:      "{artifact} is not mine to change, so this is an argument about it: {text}",
		KindExchangeTurn:        "In {exchange}, at {rounds}: {text}",
		KindExchangeClosed:      "{exchange} closed {outcome}; I judge the change against what that leaves.",
		KindIntakeHeld:          "Intake is held, so nothing new will arrive for review: {why}",
		KindIntakeReleased:      "Intake is open; work will reach me again.",
		KindHoldPlaced:          "Held before my next review. Nothing already judged changes.",
		KindHoldLifted:          "The hold is lifted; reviews resume.",
		KindWatchStarted:        "Changes will keep arriving for a verdict without anybody starting them: {why}",
		KindWatchIdle:           "Nothing is being worked on, so there is no change coming to judge: {why}",
		KindWatchBraked:         "Nothing new is being started while intake is held, so nothing is coming for a verdict: {why}",
		KindWatchResumed:        "Work is starting again, so changes will come back to me: {why}",
		KindWatchStopped:        "No more changes will arrive from this session; what I judged already stands: {why}",
	},
}

// The development manager: the queue, its order, and what is honestly done.
var developmentManagerVoice = voice{
	title:  "Development Manager",
	avatar: ":clipboard:",
	lines: map[Kind]string{
		KindItemAdmitted:        "{item} is in the queue: {title}, admitted for {goal}.",
		KindItemDecomposed:      "I've broken {parent} down, and {item} is a bounded piece of it: {title}.",
		KindItemAttributed:      "{item} now carries the goal it serves: {goal}. Work I cannot say the purpose of is work I cannot order honestly.",
		KindItemReprioritized:   "{item} is at {priority} now, so that is where it gets pulled from.",
		KindWorkApproved:        "{item} was approved and is in my queue: {title}, for {goal}.",
		KindWorkDeclined:        "{title} was declined, so nothing about it ever reaches my queue: {why}",
		KindRunStarted:          "I've pulled {item} off the queue, and it is claimed and started as {run}: {reason}",
		KindChecksPassed:        "{item} cleared its checks and is on to review.",
		KindChecksFailed:        "{item} came back from its checks: {command} exited {exit}. It routes to repair with that intact.",
		KindReviewApproved:      "{item} is approved and clear to integrate.",
		KindReviewRepairs:       "{item} routes back to the developer with {findings} intact.",
		KindPromoted:            "{item} is integrated into {branch}. That is one item actually done.",
		KindPublished:           "{item} is published as {pr} and still counts as in flight.",
		KindMergeQueued:         "{pr} is queued to merge, so {item} stays in flight until the forge says otherwise.",
		KindMergeCompleted:      "{pr} merged; {item} is done.",
		KindRunParked:           "{item} is parked, waiting on {cause}. It keeps its claim.",
		KindRunContinued:        "{item} is moving again, from where it stopped.",
		KindBlockerRecorded:     "{item} is blocked, and recorded as blocked rather than left implicit: {text}",
		KindUsageLimitExhausted: "{waiting} is stopped by the provider rather than by the queue: {cause}. Nothing moves until it lifts.",
		KindReportFiled:         "Filed beside {item} rather than folded into it: {text}",
		KindProposalRaised:      "{artifact} isn't mine to change, so here is the case for changing it: {text}",
		KindExchangeTurn:        "{exchange} is at {rounds} and the item waits on it: {text}",
		KindExchangeClosed:      "{exchange} closed {outcome}, and the item it held moves accordingly.",
		KindIntakeHeld:          "Intake is held, so I pull nothing new until it lifts: {why}",
		KindIntakeReleased:      "Intake is released; I'm pulling from the top of the backlog again.",
		KindHoldPlaced:          "Everything is held. Nothing new starts, and nothing in flight is lost.",
		KindHoldLifted:          "The hold is lifted; the work in flight carries on.",
		KindWatchStarted:        "The queue is being pulled from until somebody stops it, rather than once: {why}",
		KindWatchIdle:           "The queue has nothing pullable in it, so the line is waiting on admissions rather than on work: {why}",
		KindWatchBraked:         "I'm pulling nothing while intake is held, and what was already running finishes: {why}",
		KindWatchResumed:        "I'm pulling from the top of the queue again: {why}",
		KindWatchStopped:        "The queue stops being pulled from here; what is in it stays in it: {why}",
	},
}

// The product manager: what the product is for, and what the operator needs to
// be able to disagree with.
var productManagerVoice = voice{
	title:  "Product Manager",
	avatar: ":compass:",
	lines: map[Kind]string{
		KindItemAdmitted:        "I've admitted {item} to the backlog: {title}. It serves {goal}, and that claim is the operator's to disagree with.",
		KindItemDecomposed:      "{parent} was decomposed into {item}: {title}. What the work is for stays what the parent was admitted for.",
		KindItemAttributed:      "I've recorded what {item} is for: {goal}. Work that says nothing about intent is work nobody can decide to stop doing.",
		KindItemReprioritized:   "I've put {item} at {priority}: {why}",
		KindWorkApproved:        "The operator approved the work I proposed, and it is admitted as {item}: {title}, serving {goal}.",
		KindWorkDeclined:        "The operator turned down work I proposed — {title}, which would have served {goal} — because: {why}. Nothing was created.",
		KindRunStarted:          "Work started on {item} as {run}, chosen by {by}: {reason}. That reason is the operator's to disagree with.",
		KindChecksPassed:        "{item} passed its checks — progress on what it was admitted for.",
		KindChecksFailed:        "{item} failed its checks: {command} exited {exit}. Nothing about what it is for has changed.",
		KindReviewApproved:      "{item} was approved: what was admitted is what was built.",
		KindReviewRepairs:       "{item} needs repairs: {findings}. Still the same item, not a new one.",
		KindPromoted:            "{item} is integrated into {branch} and serves the goal it was admitted for.",
		KindPublished:           "{item} is published as {pr}.",
		KindMergeQueued:         "{pr} is queued to merge; {item} is not delivered until it lands.",
		KindMergeCompleted:      "{pr} merged, so {item} is delivered.",
		KindRunParked:           "{item} is waiting on {cause}. Nothing about its priority changed while it waits.",
		KindRunContinued:        "{item} is moving again.",
		KindBlockerRecorded:     "{item} is blocked and stays in the backlog until somebody decides otherwise: {text}",
		KindUsageLimitExhausted: "Nothing is being spent, because the provider refused {waiting}: {cause}. What is admitted keeps its place.",
		KindReportFiled:         "Noticed beside {item} and worth the operator's attention: {text}",
		KindProposalRaised:      "A change to {artifact} is proposed, and I decide it only where the document is mine: {text}",
		KindExchangeTurn:        "The operator is being asked something, in {exchange} at {rounds}: {text}",
		KindExchangeClosed:      "{exchange} closed {outcome}. What it settles is product intent.",
		KindIntakeHeld:          "The operator holds intake, so nothing new is chosen until they lift it: {why}",
		KindIntakeReleased:      "The operator released intake; the backlog is being pulled from again.",
		KindHoldPlaced:          "The operator holds all harness activity.",
		KindHoldLifted:          "The operator lifted the hold.",
		KindWatchStarted:        "What is admitted is now what is spent on, since the queue is pulled from until somebody stops it: {why}",
		KindWatchIdle:           "Nothing is being spent, because nothing admitted is ready to be worked on: {why}",
		KindWatchBraked:         "Spending has stopped while intake is held, and what is in the backlog keeps its place: {why}",
		KindWatchResumed:        "Work is being chosen again, and what I admit is what gets spent on: {why}",
		KindWatchStopped:        "Nothing further is being chosen or spent, and the backlog is untouched by that: {why}",
	},
}

// The architect: the shape of the system, the constraints on it, and whether
// what happened is what the design said would.
var architectVoice = voice{
	title:  "Architect",
	avatar: ":triangular_ruler:",
	lines: map[Kind]string{
		KindItemAdmitted:        "{item} entered the backlog under {goal}: {title}. Work traceable to intent is what keeps the shape of the thing deliberate.",
		KindItemDecomposed:      "{item} was carved out of {parent}: {title}. Decomposition is structure, and structure is where a design holds or does not.",
		KindItemAttributed:      "{item} was traced back to {goal}. A queue nobody can trace is a system nobody can reason about.",
		KindItemReprioritized:   "{item} moved to {priority}, which changes the order and nothing about the design it derives from.",
		KindWorkApproved:        "Approved and admitted as {item}: {title}, under {goal}. What it may become is bounded by the design it derives from.",
		KindWorkDeclined:        "{title} was declined, and the shape of the system is unchanged by work nobody started: {why}",
		KindRunStarted:          "{item} is under way as {run}, chosen by {by}: {reason}. The design it derives from is unchanged.",
		KindChecksPassed:        "{item} passed its checks. The gate held.",
		KindChecksFailed:        "{item} failed its checks: {command} exited {exit}. A gate that catches this is a gate doing its job.",
		KindReviewApproved:      "{item} was approved against the design it derives from.",
		KindReviewRepairs:       "{item} was sent back with {findings}, which is the loop working rather than failing.",
		KindPromoted:            "{item} is on {branch} at {commit}, promoted by the harness itself as the invariant requires.",
		KindPublished:           "{item} is published as {pr}. The local branch remains the authoritative one.",
		KindMergeQueued:         "{pr} is queued to merge; the forge settles it, not this run.",
		KindMergeCompleted:      "{pr} merged, so the forge's history and the local target agree again.",
		KindRunParked:           "{item} is parked, waiting on {cause}. A design that cannot survive an interruption is the wrong design.",
		KindRunContinued:        "{item} resumed from exactly where it stopped.",
		KindBlockerRecorded:     "{item} is blocked, which is a fact about the system rather than about the attempt: {text}",
		KindUsageLimitExhausted: "{waiting} met the account's own ceiling rather than a defect: {cause}. Capacity is a boundary condition, and a design that treats it as a failure is the wrong design.",
		KindReportFiled:         "Recorded beside {item} rather than left in prose nobody surfaces: {text}",
		KindProposalRaised:      "{artifact} should change, and this is the case: {text}",
		KindExchangeTurn:        "{exchange}, {rounds}, on what the design left open: {text}",
		KindExchangeClosed:      "{exchange} closed {outcome}, which is where the design stops being ambiguous.",
		KindIntakeHeld:          "Intake is held, which stops selection and nothing already running: {why}",
		KindIntakeReleased:      "Intake is released; selection resumes.",
		KindHoldPlaced:          "All harness activity is held, at the provider-call boundary rather than mid-generation.",
		KindHoldLifted:          "The hold is lifted, and every parked run resumes from its own record.",
		KindWatchStarted:        "Selection is now a loop rather than a pass, and nothing between its readings is cached: {why}",
		KindWatchIdle:           "Selection is polling an empty queue, which costs a tracker read and no provider call: {why}",
		KindWatchBraked:         "Selection is stopped by the intake hold, which is the brake working rather than failing: {why}",
		KindWatchResumed:        "Selection resumes where it left off, from a queue read fresh rather than remembered: {why}",
		KindWatchStopped:        "The selection loop is closed; every run it started was waited out rather than abandoned: {why}",
	},
}

// voices is every speaker there is, keyed exactly as an envelope names one.
var voices = map[string]voice{
	HarnessSpeaker:                        harnessVoice,
	string(domain.RoleDeveloper):          developerVoice,
	string(domain.RoleReviewer):           reviewerVoice,
	string(domain.RoleDevelopmentManager): developmentManagerVoice,
	string(domain.RoleProductManager):     productManagerVoice,
	string(domain.RoleArchitect):          architectVoice,
}

// The words each severity is said in, and the decoration that is added to them.
// The words come first and carry the whole meaning: a reader whose client will
// not render the emoji, or who is reading this piped into a file, still cannot
// mistake a critical report for a note. A note is the baseline and is marked
// with nothing, because marking everything marks nothing.
const (
	criticalMark = ":rotating_light: Critical — "
	warningMark  = ":warning: Warning — "
)

func severityMark(severity report.Severity) string {
	switch severity {
	case report.SeverityCritical:
		return criticalMark
	case report.SeverityWarning:
		return warningMark
	default:
		return ""
	}
}

// MaxBodyBytes bounds one message body. A body over it is cut with a marker
// naming the durable record that holds the whole, rather than split into a flood
// of messages to fit: a thread is a narrative, and the record is authoritative
// anyway.
const MaxBodyBytes = 2900

const bodyCutNote = "… [cut; the whole is in %s]"

// Render says one event in one persona's voice, addressed to one topic. It is
// the whole of what this package decides, and it is deterministic: the same
// topic, speaker, and event always produce the same message.
func Render(topic Topic, speaker Speaker, event Event) (Message, error) {
	var problems []error
	if err := topic.Validate(); err != nil {
		problems = append(problems, err)
	}
	if err := speaker.Validate(); err != nil {
		problems = append(problems, err)
	}
	if err := event.Validate(); err != nil {
		problems = append(problems, err)
	}
	if err := errors.Join(problems...); err != nil {
		return Message{}, fmt.Errorf("render %s: %w", event.Kind, err)
	}
	spoken, ok := voices[speaker.Key()]
	if !ok {
		return Message{}, fmt.Errorf("render %s: no voice for speaker %q", event.Kind, speaker.Key())
	}
	line, ok := spoken.lines[event.Kind]
	if !ok {
		return Message{}, fmt.Errorf("render %s: the %s has no line for it", event.Kind, speaker.Key())
	}
	said, err := substitute(line, event.fields(topic))
	if err != nil {
		return Message{}, fmt.Errorf("render %s as the %s: %w", event.Kind, speaker.Key(), err)
	}
	message := Message{
		SchemaVersion: SchemaVersion,
		Kind:          event.Kind,
		Topic:         topic.Key(),
		// The title is bounded here as well as where a producer sets it, so what
		// the envelope carries is a header line whichever way the topic was built.
		TopicTitle: boundTitle(topic.Title),
		Speaker:    speaker.Key(),
		Identity:   speaker.Identity(),
		Severity:   event.Severity,
		Body:       bound(severityMark(event.Severity)+said, event.Refs),
		Refs:       event.Refs,
		At:         event.At.UTC(),
	}
	if err := message.Validate(); err != nil {
		return Message{}, err
	}
	return message, nil
}

// fields is every placeholder a voice line may use, filled from the event and
// the topic it is addressed to. Every one of them always has a value: where the
// record holds nothing, what is substituted states the absence rather than
// leaving a blank, because a sentence with a hole in it reads as a bug and a
// record that is missing something is still worth being told about.
//
// The two reasons are separate placeholders because they are separate facts and
// their absences mean opposite things: an unaccounted run is what recording a
// selection exists to expose, while an operator who holds intake in a hurry owes
// nobody an explanation.
func (e Event) fields(topic Topic) map[string]string {
	detail := e.Detail
	return map[string]string{
		"item":     stated(itemOf(e.Refs, topic), "an unnamed work item"),
		"run":      stated(e.Refs.RunID, "an unrecorded run"),
		"by":       stated(detail.SelectedBy, "nobody the record names"),
		"reason":   stated(detail.SelectionReason, "no reason recorded"),
		"command":  stated(detail.Command, "a check the record does not name"),
		"exit":     strconv.Itoa(detail.ExitCode),
		"findings": countOf(detail.Findings, "finding", "findings", "findings the record does not count"),
		"branch":   stated(detail.TargetBranch, "an unnamed branch"),
		"commit":   stated(shortCommit(detail.Commit), "an unrecorded commit"),
		"pr":       stated(detail.PullRequest, "an unrecorded pull request"),
		"cause":    stated(detail.Cause, "something the record does not name"),
		"waiting":  stated(detail.Waiting, "something the record does not name"),
		"rounds":   roundsOf(detail),
		"why":      stated(detail.Reason, "no reason given"),
		"text":     stated(e.Text, "nothing the record could carry"),
		"artifact": stated(detail.Artifact, "an unnamed artifact"),
		"outcome":  outcomeOf(detail),
		"exchange": stated(exchangeOf(e.Refs, topic), "an unnamed exchange"),
		"title":    stated(detail.Title, "a title the record does not carry"),
		"goal":     stated(detail.Goal, "no goal the record names"),
		"parent":   stated(detail.Parent, "an item the record does not name"),
		"priority": priorityOf(detail),
	}
}

// substitute replaces each {placeholder} with what the record holds for it. A
// placeholder no field fills is a voice line naming something that does not
// exist, which is a mistake in the table rather than in any record, so it is an
// error rather than an empty string somebody would have to notice in a channel.
func substitute(line string, fields map[string]string) (string, error) {
	var built strings.Builder
	rest := line
	for {
		before, after, found := strings.Cut(rest, "{")
		built.WriteString(before)
		if !found {
			return built.String(), nil
		}
		name, remainder, closed := strings.Cut(after, "}")
		if !closed {
			return "", fmt.Errorf("unclosed placeholder in %q", line)
		}
		value, ok := fields[name]
		if !ok {
			return "", fmt.Errorf("no field named %q", name)
		}
		built.WriteString(value)
		rest = remainder
	}
}

func stated(value, absence string) string {
	if trimmed := strings.TrimSpace(value); trimmed != "" {
		return trimmed
	}
	return absence
}

// itemOf prefers the topic's own identifier over the reference, because the
// topic is what the thread is about and a message in an item's thread must name
// that item whatever else it correlates to.
func itemOf(refs Refs, topic Topic) string {
	if topic.Kind == TopicWorkItem {
		return topic.ID
	}
	return refs.WorkItemID
}

func exchangeOf(refs Refs, topic Topic) string {
	if topic.Kind == TopicExchange {
		return topic.ID
	}
	return refs.ExchangeID
}

// countOf says a count in words rather than as a bare number, so "1 findings"
// never reaches anybody and a count nothing recorded says so.
func countOf(count int, singular, plural, absence string) string {
	switch {
	case count < 0:
		return absence
	case count == 0:
		return "no " + plural
	case count == 1:
		return "one " + singular
	default:
		return strconv.Itoa(count) + " " + plural
	}
}

// roundsOf says where an exchange has got to against its cap. The cap is said
// alongside the round because the round on its own does not say how close the
// exchange is to closing unresolved, which is the part an operator acts on.
func roundsOf(detail Detail) string {
	switch {
	case detail.Round <= 0:
		return "an unrecorded round"
	case detail.Rounds <= 0:
		return "round " + strconv.Itoa(detail.Round)
	default:
		return "round " + strconv.Itoa(detail.Round) + " of " + strconv.Itoa(detail.Rounds)
	}
}

// priorityOf says where in the queue an item sits. The number is said as the
// tracker's own, because that is what somebody reordering the backlog types, and
// a priority the record did not carry says so rather than reading as the highest
// one there is — zero is the top of this queue rather than an unstated place in
// it, which is exactly the confusion an absent value would cause.
func priorityOf(detail Detail) string {
	if detail.Priority < 0 {
		return "a priority the record does not state"
	}
	return "priority " + strconv.Itoa(detail.Priority)
}

// outcomeOf says how an exchange ended. Closing unresolved at the round cap is
// named as such and carries what was left unsettled, because that is the ending
// somebody has to do something about.
func outcomeOf(detail Detail) string {
	if unresolved := strings.TrimSpace(detail.Unresolved); unresolved != "" {
		return "unresolved at its round cap, leaving: " + unresolved
	}
	return "resolved"
}

// shortCommit says a commit the way a reader would quote one. The whole
// identifier is in the record; what a sentence needs is enough of it to match.
func shortCommit(commit string) string {
	trimmed := strings.TrimSpace(commit)
	if len(trimmed) <= 12 {
		return trimmed
	}
	return trimmed[:12]
}

// bound cuts a body that would not survive the surface it is posted to, and says
// it was cut and where the whole is. Cutting rather than splitting is deliberate:
// a message broken into four to fit is four messages in a narrative that had one
// thing to say.
func bound(body string, refs Refs) string {
	if len(body) <= MaxBodyBytes {
		return body
	}
	note := fmt.Sprintf(bodyCutNote, refs.Record())
	cut := MaxBodyBytes - len(note)
	if cut < 0 {
		cut = 0
	}
	for cut > 0 && !utf8.RuneStart(body[cut]) {
		cut--
	}
	return strings.TrimRight(body[:cut], " \n\t") + note
}
