package notify

// How far one message is carried.
//
// Every kind in the vocabulary is worth recording, and that is not the same
// question as whether it is worth putting in front of somebody who has opened
// nothing. The operator's rule is that the channel level carries what is
// important — something that matters is broken or has materially changed — or
// what needs his action or decision, and that a message which is neither does
// not post at all: it lands in the durable record, and the regular summaries are
// built from there.
//
// The measurement that produced the rule is what makes it concrete. Of 2,250
// posts, roughly three quarters were per-event progress narration — a report
// pushed on its own, a poll that started nothing, a session starting, checks
// passing, a review approving — against under forty of the kinds somebody
// actually has to act on. The important ones were not hidden; they were drowned,
// which costs exactly as much.
//
// So each kind says how far it goes, once, here. It is beside the vocabulary
// rather than in the surface that posts for the reason the status symbols and
// the voice table are: a surface that decided this for itself would be a second
// posting policy nobody ratified, and two of them would disagree about one
// event in front of the one person who would have to adjudicate it.

import (
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/report"
)

// Reach is how far one event is carried.
type Reach string

const (
	// ReachChannel is the channel level: seen by somebody who has opened no
	// threads. It is what is important or what needs the operator, and nothing
	// else.
	ReachChannel Reach = "channel"
	// ReachThread is the topic's own thread, where the narrative is. A reader
	// following one item sees it; a reader scanning the channel does not.
	ReachThread Reach = "thread"
	// ReachRecord is no further than the durable record the event was read from.
	// It is not posted anywhere, because there is nowhere it would be worth
	// putting: the record holds it, `yoyo status` reads it, and the summaries are
	// built from it.
	ReachRecord Reach = "record"
)

// Valid reports whether a name is one of the three.
func (r Reach) Valid() bool {
	switch r {
	case ReachChannel, ReachThread, ReachRecord:
		return true
	default:
		return false
	}
}

// reaches is how far each kind goes. Every kind in the vocabulary has an entry,
// and a kind added without one reaches nothing rather than defaulting into the
// channel: the failure that asked for this table was noise, so the way to be
// wrong here is to be quiet and be corrected, not to be loud and be muted.
var reaches = map[Kind]Reach{
	// The backlog moving is the harness's steering wheel turning, and it is
	// narration: the item it concerns has a thread, the queue's own order is what
	// the tracker holds, and nobody acts on an admission as it happens.
	KindItemAdmitted:      ReachThread,
	KindItemDecomposed:    ReachThread,
	KindItemAttributed:    ReachThread,
	KindItemReprioritized: ReachThread,
	// A block of tracker actions the harness would not read is the opposite:
	// several admissions or closes did not happen and the role that asked believes
	// they did, so the queue is not what anybody thinks it is until somebody looks.
	KindTrackerBlockRefused: ReachChannel,
	// What the operator decided about proposed work. He decided it, so it is not
	// news to him; the item's thread is where the decision belongs.
	KindWorkApproved: ReachThread,
	KindWorkDeclined: ReachThread,
	// Work a conversation carries. It waits on a role rather than on the operator,
	// and the wait is legible in the item's thread where the handoff was said.
	KindWorkHandedOff:  ReachThread,
	KindWorkPickedUp:   ReachThread,
	KindWorkCarriedOut: ReachThread,
	// One run's own arc, which is the largest block of narration there is. A run
	// starting, its checks passing or failing, a verdict, a promotion, a
	// publication and its merge are the thread's whole story and none of them is a
	// move of the operator's: a failing check is the developer's next attempt, and
	// repairs asked for are the developer's too.
	KindRunStarted:     ReachThread,
	KindRunContinued:   ReachThread,
	KindChecksPassed:   ReachThread,
	KindChecksFailed:   ReachThread,
	KindReviewApproved: ReachThread,
	KindReviewRepairs:  ReachThread,
	KindPromoted:       ReachThread,
	KindPublished:      ReachThread,
	KindMergeQueued:    ReachThread,
	KindMergeCompleted: ReachThread,
	// A merge that is not going to happen is the one publication fact nobody finds
	// out about on their own: the change is promoted, the item reads as landed, and
	// the request waits on a person who does not know it is theirs.
	KindMergeDropped: ReachChannel,
	// Work that stopped. A park waits on something outside the run, a blocker is
	// the development manager's decision and moves nothing until it is made, and a
	// run that ended without succeeding has materially changed what exists.
	KindRunParked:       ReachChannel,
	KindBlockerRecorded: ReachChannel,
	KindRunEnded:        ReachChannel,
	// Capacity that ran out somewhere that is not a run: hours in which nothing
	// will happen, which look exactly like a healthy quiet queue.
	KindUsageLimitExhausted: ReachChannel,
	// What an agent said in its own words. A report is the single largest source of
	// individual pushes and asks for nothing by design, so it lives in its item's
	// thread and is carried to the channel by the summaries built from the report
	// store — except a critical one, which reaches the channel through the
	// promotion below because something already wrong is exactly what the operator
	// wants told. A proposal and a closed exchange are both waiting on a decision;
	// a turn of an exchange in flight is not yet.
	KindReportFiled:    ReachThread,
	KindProposalRaised: ReachChannel,
	KindExchangeTurn:   ReachThread,
	KindExchangeClosed: ReachChannel,
	// What a reply in a thread did. All four are addressed to the person who typed
	// the words being read back to them, and all four reach that person by name
	// wherever they are posted — so the channel level buys them nothing, and the
	// three that settle something belong in the thread that asked. The one
	// exception is a directive that left something unresolved, which pauses the
	// work it affects until somebody settles it: that one is promoted below, from
	// what the record left unsettled rather than from the kind.
	KindDirectiveRecorded:   ReachThread,
	KindDirectiveResolved:   ReachThread,
	KindDirectiveCarriedOut: ReachThread,
	KindDirectiveRefused:    ReachThread,
	// The operator's two switches, which are about the whole line and are his own
	// to lift.
	KindIntakeHeld:     ReachChannel,
	KindIntakeReleased: ReachChannel,
	KindHoldPlaced:     ReachChannel,
	KindHoldLifted:     ReachChannel,
	// What a watch session is doing. These are the poll-by-poll narration of a
	// process that spends most of its life saying nothing, and they were 473 of the
	// measured posts on their own. The watch log holds every one of them and
	// `yoyo status` reads it back. What the channel needs from that log is only
	// what somebody has to act on, and three messages below say exactly that from
	// the same log: the braked session, the waiting line — which names a session
	// that has stopped, once there is work it would have started — and the stall,
	// which is the case where the session died without recording anything at all.
	KindWatchStarted:     ReachRecord,
	KindWatchIdle:        ReachRecord,
	KindWatchResumed:     ReachRecord,
	KindWatchStopped:     ReachRecord,
	KindWatchRedeploying: ReachRecord,
	// A braked line has stopped and stays stopped until intake is released.
	KindWatchBraked: ReachChannel,
	// The three readings of a line that is choosing nothing: the state said again
	// while it stands, the silence nothing accounts for, and the silence the
	// provider accounts for. Every one of them is what somebody scanning a quiet
	// channel is actually checking for, and the last is a note that still belongs
	// at the top because the operator asked that a pause name its cause where he
	// will read it.
	KindLineWaiting:    ReachChannel,
	KindStallNoticed:   ReachChannel,
	KindProviderWindow: ReachChannel,
	// A session dispatching work on a binary the harness has moved past. Nothing in
	// the record says it at all, and what it costs is rounds spent against bugs
	// that were fixed hours earlier.
	KindResidentStale: ReachChannel,
	// One value the project's template has improved. It asks for a decision and it
	// is said exactly once per improvement, ever.
	KindBundleImprovement: ReachChannel,
	// What a thread gathered while nothing was posting it. It stands for that
	// thread's narrative, so it belongs where the narrative is.
	KindCatchUpDigest: ReachThread,
}

// Reach is how far one kind goes on its own, before anything about a particular
// event is taken into account. A kind with no entry reaches the record and no
// further, which is the quiet way to be wrong about a kind nobody has classified.
func (k Kind) Reach() Reach {
	if reach, found := reaches[k]; found {
		return reach
	}
	return ReachRecord
}

// reachOf is how far one event addressed to one topic goes. It is the whole of
// the posting policy, and it is here rather than in a surface so that the sink
// that posts and the notifier that renders cannot come to disagree about one
// message.
//
// Two things move a kind off its own answer, and each is a rule the operator
// stated rather than a refinement of it.
//
// A critical event always reaches the channel, whatever its kind says. Severity
// is importance rather than actionability — his correction, in his words — so
// something already wrong that will cost somebody is told at the level he reads,
// and the kind's own reach decides only where the quieter ones go.
//
// A thread's message with no thread to go in reaches the record instead. What is
// addressed to the product is posted unthreaded, which is the channel level: a
// report filed against no work item would otherwise be pushed to the top of the
// channel precisely because there was less to say about it.
//
// One kind answers from the record rather than from the table, for the reason
// the whose-move clause does: a recorded directive that left something unsettled
// pauses the work it affects until somebody settles it, and one that settled
// nothing is in force already and stops nothing. They are opposite news, and the
// pausing one is what the operator's own survey counts among the kinds that must
// not be drowned.
func reachOf(topic Topic, event Event) Reach {
	if event.Severity == report.SeverityCritical {
		return ReachChannel
	}
	reach := event.Kind.Reach()
	if event.Kind == KindDirectiveRecorded && strings.TrimSpace(event.Detail.Unresolved) != "" {
		reach = ReachChannel
	}
	if reach == ReachThread && topic.Kind == TopicProduct {
		return ReachRecord
	}
	return reach
}

// Reach is how far this notification goes, read before anything is rendered so
// the delivery pass can decline to post one at all.
func (n Notification) Reach() Reach { return reachOf(n.Topic, n.Event) }

// Posts reports a notification worth putting somewhere a person reads. It is
// false for the two cases that both end in a cursor advancing and nothing being
// said: selection that found no milestone at all, and an event whose reach is the
// durable record it was already read from.
func (n Notification) Posts() bool {
	return !n.Silent() && n.Reach() != ReachRecord
}
