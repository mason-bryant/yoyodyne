package slack

// What the sink reads, and where the reading stops.
//
// Nothing here decides what an event means or how it is said. Which crossings of
// a durable record are worth reporting, which persona's account each one is, and
// the words it is said in all belong to the notifier, and this reads that
// package rather than repeating it: a second producer of reportable events adds
// a selection function there and nothing at all here.
//
// What is left is the part that is genuinely the sink's — where the reading got
// to. The notifier's selection is a pure comparison of two readings of a record,
// so somebody has to remember the earlier reading; the notifier's other
// producers are logs, so somebody has to remember the position. That is a
// cursor, it is durable, and it is written after each message rather than at the
// end of a pass, because it is the only thing standing between a crash and
// saying everything again.

import (
	"context"
	"fmt"
	"hash/fnv"
	"strconv"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/amendment"
	"github.com/mason-bryant/yoyodyne/internal/notify"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// The streams a product has. Runs and conversations take one each, named for
// the run or the conversation, because they are separate subjects rather than
// one log; the rest are one apiece.
const (
	reportStream     = "reports"
	proposalStream   = "proposals"
	productStream    = "product"
	watchStream      = "watch"
	usageLimitStream = "usage-limits"
	heartbeatStream  = "heartbeat"
	residentStream   = "resident"
	directiveStream  = "directives"
)

func runStream(runID string) string { return "run:" + runID }

func conversationStream(conversationID string) string { return "chat:" + conversationID }

// The prefixes the operator's two switches are marked under. A hold is marked
// with the moment it was placed, so a second hold a week later is a second thing
// to say rather than the same one.
const (
	intakeMark = "intake:"
	holdMark   = "hold:"
	// outcomeMark records having said what became of one directive somebody
	// asked for in a thread. It names the directive, because a directive is
	// settled once and what settled it is said once.
	outcomeMark = "outcome:"
	// buildMark names the build a running session is standing on, so a restart
	// onto a different binary re-arms rather than inheriting the last one's clock.
	buildMark = "build:"
	// escalatedMark records having told the operators directly about the build
	// this cursor is standing on. It carries no name of its own because the build
	// is already the cursor's standing state: a different build is a different
	// cursor, and the mark goes with it.
	escalatedMark = "escalated"
)

// Delivery is one step of one stream: what to say, and the cursor that records
// having said it. The cursor is the whole of the delivery guarantee — the sink
// posts and then writes it, so a process killed between the two repeats one
// message rather than losing it.
//
// A delivery with nothing to say is a cursor advance on its own. It is how a run
// that is over stops being carried and how reports older than this sink are read
// past, neither of which anybody should be told about.
type Delivery struct {
	Stream       string
	Cursor       Cursor
	Notification notify.Notification
	// Mention is the Slack member id this delivery is for, empty for the ordinary
	// message that is for whoever is reading the channel. It is set on the one
	// class of message that answers a person rather than reports to a channel:
	// what became of something they asked for in a thread. Being told is the
	// whole point of it, and a thread they are not looking at is silence.
	Mention string
	// Reply is the timestamp of the message that asked for this, empty for every
	// delivery that is not an answer to one. It is the message the receipt sits
	// on: it has worn the thinking face since it arrived, and saying what became
	// of the directive is the moment that stops being true.
	Reply string
	// Direct asks for this delivery to reach the operators where they will see it
	// at three in the morning, as well as in the channel. It is set on the one
	// class of message that is about the harness itself being degraded rather than
	// about any work, because a channel is a place somebody chooses to look and a
	// degraded harness is exactly what they will not think to look for.
	//
	// It names nobody. Who the operators are is the surface's — the same member
	// ids a reply is authorized against — and a feed that named them would be a
	// reading of the durable records holding an opinion about a workspace.
	Direct bool
}

// Silent reports a delivery that advances a cursor and posts nothing.
func (d Delivery) Silent() bool { return d.Notification.Silent() }

// Batch is one pass over the durable records: what is ready to post, which
// streams still exist so cursors for the rest can be dropped, and what each
// topic is doing right now.
type Batch struct {
	Deliveries []Delivery
	Streams    map[string]struct{}
	// Statuses is each topic's current status, keyed exactly as a thread is. It
	// is a reading rather than a history, and that is why it is beside the
	// deliveries instead of among them: a delivery is a transition said once, and
	// a status is what is true at the end of this pass however many transitions
	// got there — including none, which is the case a message could never carry.
	// A run that finishes with nothing left to say still has to stop reading as
	// working.
	Statuses map[string]notify.Status
}

// Feed is where the sink's messages come from. It is polled rather than
// subscribed to because the records it reads are files written by other
// processes, and because a sink that has been away has to catch up from its
// cursors either way.
type Feed interface {
	Poll(ctx context.Context, cursors Cursors) (Batch, error)
}

// HarnessFeed reads the product's own durable records: what became of its runs,
// what its conversations did to the backlog, what its agents reported and
// proposed while their work carried on, and the operator's two switches over the
// whole line.
type HarnessFeed struct {
	Runs *runstate.Store
	// Conversations is where the backlog moving is read from. It is optional
	// only in the sense that a feed assembled without it reports everything else:
	// a product whose queue changes invisibly is the thing this exists to
	// prevent, and every sink the harness builds is given one.
	Conversations *runstate.ConversationStore
	Reports       *runstate.ReportStore
	Proposals     *runstate.AmendmentStore
	Intake        *runstate.IntakeHoldStore
	Holds         *runstate.OperatorHoldStore
	// Watch is where a watch session says what it is doing. It is optional in the
	// same sense the conversations are: a feed assembled without one reports
	// everything else, and what is lost is the one thing nothing else in the
	// record says — that the session choosing work is alive and idle rather than
	// dead.
	Watch *runstate.WatchStore
	// UsageLimits is where a provider refusing the harness outside a run is read
	// from: a conversation turn, an independent review. It is optional in the
	// same sense the two above are, and what a feed assembled without one loses
	// is the only account there is of those refusals — a run says its own by
	// parking, and nothing else says anything at all.
	UsageLimits *runstate.UsageLimitStore
	// Backlog is how much admitted work the tracker calls ready, and it is read
	// for one purpose: telling a line that is waiting on somebody from one that is
	// honestly quiet. It is optional, and a feed assembled without one says
	// everything else and never says the line is waiting — which is silence over a
	// held queue, so every sink the harness builds is given one.
	Backlog Backlog
	// Directives is the product's durable directive record, read for one thing
	// only: what became of a directive somebody asked for in a thread. It is
	// optional, and a feed assembled without one says nothing about any
	// directive — which is silence exactly where somebody is waiting for an
	// answer, so every sink the harness builds is given one.
	//
	// Nothing here writes it. Recording a directive is the inbound half's and
	// resolving one is the operator's; this is a reading of what they did.
	Directives *runstate.DirectiveStore
	// Steers is the sink's own memory of which directives were said into a
	// thread, by whom, and in which message. It is what separates a directive to
	// answer somebody about from one typed at a terminal, which has no thread and
	// nobody to tag, and it is read here and written only by the connection.
	Steers *Store
	// Deployments is how far the product's repository has moved past the build a
	// live watch session is running, read for one thing: telling a session that is
	// executing what is deployed from one that is executing a binary from before
	// the fixes it is about to spend rounds rediscovering. It is optional, and a
	// feed assembled without one says everything else and never says a session is
	// stale — which is the silence this exists to end, so every sink the harness
	// builds is given one.
	Deployments Deployments
	// StaleBuildThreshold is how far behind a session has to be before the
	// operators are told directly rather than in the channel alone. Zero takes
	// DefaultStaleBuildThreshold.
	StaleBuildThreshold int
	// Heartbeat is how often a line that is choosing nothing over ready work says
	// so again. Zero takes DefaultHeartbeat. It is a cadence rather than a switch:
	// there is deliberately no way to turn it off, because what it would buy is
	// silence that means waiting-on-you, which is the state it exists to end.
	Heartbeat time.Duration
	// Now is read for the moment a hold was seen to have lifted, and for the age a
	// heartbeat says a state has stood. Both are things no record holds — what
	// lifts a hold is its absence, and what makes a state worth saying again is how
	// long ago it began — so it is injected and a test can say when they were.
	Now func() time.Time
	// Log is where a record that cannot be addressed at all is said out loud
	// before it is read past. It is the sink's own log, and it is never given a
	// token to print.
	Log func(format string, args ...any)
}

// Poll reads every stream and reports what the cursors say has not been posted.
//
// Everything the product recorded before the cursors' watermark is history and
// is read past: a channel turned on today does not want a month of finished work
// arriving at once. The watermark is one durable moment for the whole product
// rather than this process's start time, which is what makes an outage a delay:
// a record filed while the sink was down is after the watermark whatever the
// cursor for its stream happens to say, so it is posted when the sink returns
// rather than mistaken for history somebody has already read.
func (f *HarnessFeed) Poll(ctx context.Context, cursors Cursors) (Batch, error) {
	batch := Batch{Streams: map[string]struct{}{
		reportStream:   {},
		proposalStream: {},
		productStream:  {},
	}}
	since := cursors.Since

	states, err := f.Runs.Recorded()
	if err != nil {
		return Batch{}, fmt.Errorf("read the recorded runs: %w", err)
	}
	// What is still in flight is counted from the same reading the crossings are
	// selected from, rather than asked for a second time: a run is in flight or it
	// is not, and two readings of the same files a moment apart could disagree
	// about it.
	inFlight := 0
	for _, state := range states {
		if err := ctx.Err(); err != nil {
			return Batch{}, err
		}
		if !state.Status.Terminal() {
			inFlight++
		}
		stream := runStream(state.RunID)
		batch.Streams[stream] = struct{}{}
		deliveries, err := f.runDeliveries(state, cursors.Streams[stream], since)
		if err != nil {
			return Batch{}, err
		}
		batch.Deliveries = append(batch.Deliveries, deliveries...)
	}
	// What each item is doing is read from the same reading its crossings were
	// selected from, for the reason the in-flight count is: a status derived from
	// a second reading a moment later could contradict the messages posted beside
	// it in the same pass.
	batch.Statuses = itemStatuses(states, since)

	conversed, err := f.conversationDeliveries(ctx, cursors, batch.Streams)
	if err != nil {
		return Batch{}, err
	}
	batch.Deliveries = append(batch.Deliveries, conversed...)

	filed, err := f.Reports.List()
	if err != nil {
		return Batch{}, fmt.Errorf("read the collected reports: %w", err)
	}
	reported, err := f.logDeliveries(reportStream, cursors.Streams[reportStream], len(filed), since,
		func(index int) (time.Time, notify.Notification, error) {
			notification, err := notify.FromReport(filed[index])
			return filed[index].RecordedAt, notification, err
		})
	if err != nil {
		return Batch{}, err
	}
	batch.Deliveries = append(batch.Deliveries, reported...)

	records, err := f.Proposals.List()
	if err != nil {
		return Batch{}, fmt.Errorf("read the proposed changes: %w", err)
	}
	proposals := amendment.Proposals(records)
	raised, err := f.logDeliveries(proposalStream, cursors.Streams[proposalStream], len(proposals), since,
		func(index int) (time.Time, notify.Notification, error) {
			notification, err := notify.FromProposal(proposals[index])
			return proposals[index].RaisedAt, notification, err
		})
	if err != nil {
		return Batch{}, err
	}
	batch.Deliveries = append(batch.Deliveries, raised...)

	// The watch log is read once and used twice: for what each session said it was
	// doing, and for whether any session is still doing it. Reading it twice would
	// let one pass post a session stopping and then derive a line from a log that
	// had moved underneath it.
	sessions, err := f.sessions()
	if err != nil {
		return Batch{}, err
	}
	watched, err := f.watchDeliveries(cursors, batch.Streams, sessions)
	if err != nil {
		return Batch{}, err
	}
	batch.Deliveries = append(batch.Deliveries, watched...)

	refused, err := f.usageLimitDeliveries(cursors, batch.Streams)
	if err != nil {
		return Batch{}, err
	}
	batch.Deliveries = append(batch.Deliveries, refused...)

	outcomes, err := f.directiveDeliveries(cursors, batch.Streams)
	if err != nil {
		return Batch{}, err
	}
	batch.Deliveries = append(batch.Deliveries, outcomes...)

	// The operator's two switches are read once and used twice: for the messages
	// that say they were placed and lifted, and for what has stopped the line when
	// the heartbeat below asks. Reading them apart would let one pass post a hold
	// and derive a line nothing is holding.
	held, err := f.switches()
	if err != nil {
		return Batch{}, err
	}
	batch.Deliveries = append(batch.Deliveries, f.holdDeliveries(cursors.Streams[productStream], held)...)

	beat, err := f.heartbeatDeliveries(ctx, cursors.Streams[heartbeatStream], held, sessions, inFlight, batch.Streams)
	if err != nil {
		return Batch{}, err
	}
	batch.Deliveries = append(batch.Deliveries, beat...)

	// The session's own age, from the same reading of the watch log. It is a
	// separate stream from the heartbeat above because the two are true at
	// opposite times: the line is said when nothing is being chosen, and a session
	// running an old binary is at its most expensive while it is busy.
	resident, err := f.residentDeliveries(ctx, cursors.Streams[residentStream], sessions, batch.Streams)
	if err != nil {
		return Batch{}, err
	}
	batch.Deliveries = append(batch.Deliveries, resident...)
	return batch, nil
}

// itemStatuses reads what each work item is doing out of the runs recorded for
// it. An item is marked from its latest run, because a status answers what is
// happening to the item now: an earlier attempt that failed is not what somebody
// scanning the channel needs, and the thread below the mark still holds it.
//
// A run that was over before the watermark marks nothing. It is history the
// channel was never told about, and a thread opened today by something else —
// a report, the backlog moving — must not acquire a status from a run nobody
// here has said a word about.
func itemStatuses(states []runstate.State, since time.Time) map[string]notify.Status {
	latest := map[string]runstate.State{}
	for _, state := range states {
		if predates(since, completion(state)) {
			continue
		}
		if held, found := latest[state.WorkItemID]; found && !later(state, held) {
			continue
		}
		latest[state.WorkItemID] = state
	}
	statuses := make(map[string]notify.Status, len(latest))
	for item, state := range latest {
		topic, err := notify.WorkItem(item)
		if err != nil {
			// A run nothing can be addressed to is already said once and read past
			// where its crossings are selected; marking is not the place to say it
			// a second time on every pass.
			continue
		}
		statuses[topic.Key()] = notify.StatusOfRun(state)
	}
	return statuses
}

// later reports the more recent of two runs on one item. The start is what
// orders them — a second attempt begins after the first, whatever either goes on
// to do — and the last update settles the tie a repaired record could otherwise
// leave.
func later(state, held runstate.State) bool {
	if !state.StartedAt.Equal(held.StartedAt) {
		return state.StartedAt.After(held.StartedAt)
	}
	return state.UpdatedAt.After(held.UpdatedAt)
}

// sessions reads what the watch sessions did, in the order they did it. A
// product nobody has watched has no log rather than a broken one, and a feed
// assembled without a watch store reads none.
func (f *HarnessFeed) sessions() ([]runstate.WatchTransition, error) {
	if f.Watch == nil {
		return nil, nil
	}
	transitions, err := f.Watch.List()
	if err != nil {
		return nil, fmt.Errorf("read what the watch sessions did: %w", err)
	}
	return transitions, nil
}

// switches reads the operator's two holds. Neither absence is a failure: not
// holding anything is the ordinary state of both, and a record that cannot be
// read is an error rather than an absence, because a hold nobody can read must
// never be treated as one that was never placed.
func (f *HarnessFeed) switches() (switches, error) {
	intake, intakeHeld, err := f.Intake.Held()
	if err != nil {
		return switches{}, fmt.Errorf("read the intake hold: %w", err)
	}
	operator, operatorHeld, err := f.Holds.Held()
	if err != nil {
		return switches{}, fmt.Errorf("read the operator hold: %w", err)
	}
	return switches{
		intake:       intake,
		intakeHeld:   intakeHeld,
		operator:     operator,
		operatorHeld: operatorHeld,
	}, nil
}

// watchDeliveries says what the sessions that choose work have been doing. It is
// an append-only log like the reports pile and advances by position, and it is a
// stream of its own rather than part of the product's marks because it is a
// history rather than a switch that is on or off: a session that idled all night
// and one that stopped at midnight are both things somebody reads afterwards.
func (f *HarnessFeed) watchDeliveries(cursors Cursors, streams map[string]struct{}, transitions []runstate.WatchTransition) ([]Delivery, error) {
	if f.Watch == nil {
		return nil, nil
	}
	streams[watchStream] = struct{}{}
	return f.logDeliveries(watchStream, cursors.Streams[watchStream], len(transitions), cursors.Since,
		func(index int) (time.Time, notify.Notification, error) {
			notification, err := notify.FromWatch(transitions[index])
			return transitions[index].At, notification, err
		})
}

// usageLimitDeliveries says where a provider refused the harness outside a run.
// It is an append-only log like the watch transitions and advances by position,
// and it is a stream of its own rather than part of any run's: the processes
// that meet a refusal have no run between them, which is the whole reason the
// log exists.
func (f *HarnessFeed) usageLimitDeliveries(cursors Cursors, streams map[string]struct{}) ([]Delivery, error) {
	if f.UsageLimits == nil {
		return nil, nil
	}
	streams[usageLimitStream] = struct{}{}
	exhaustions, err := f.UsageLimits.List()
	if err != nil {
		return nil, fmt.Errorf("read what the provider refused: %w", err)
	}
	return f.logDeliveries(usageLimitStream, cursors.Streams[usageLimitStream], len(exhaustions), cursors.Since,
		func(index int) (time.Time, notify.Notification, error) {
			notification, err := notify.FromUsageLimit(exhaustions[index])
			return exhaustions[index].At, notification, err
		})
}

// directiveDeliveries says what became of the directives somebody asked for in
// a thread, in the thread they asked in and addressed to them by name.
//
// It is the other half of the acknowledgment the inbound half posts. That one
// says what was recorded, which is a receipt rather than an answer: the person
// who typed it is waiting to hear what came of it, and until now the only place
// that was said was a terminal they are not at. So the record is read for the
// one thing it holds about an outcome — the directive having been settled, and
// what settled it — and that is said where they asked. It is also the moment
// their own message stops wearing the thinking face, which is why the reply that
// asked travels with the delivery.
//
// Both settlements are read here, and the commonest one by far is a directive
// that paused nothing being carried out. Only pausing kinds could be settled at
// all until the harness could record what came of an operational one, so a plain
// reply — which is what most replies are — reached this loop, was recorded,
// and then stood open forever with its thread never told what became of it.
//
// Only directives said into a thread are read, because only those have a thread
// to answer in and somebody to answer. One recorded at a terminal is not in the
// steer map, and reporting on it here would be this feed announcing every
// directive the product has ever had.
//
// A settlement the connection made itself is left alone. It has already been
// said in the thread, by the reply that made it, and the two halves post from
// different goroutines and cannot share a memory of what they have said.
//
// What was settled before the watermark is history, exactly as it is on every
// stream that reads a record. The per-directive mark alone would not hold that
// line: the marks live in the cursors, the steer map does not, and the setup
// document tells an operator starting a channel over to delete one and keep the
// other — which without this would answer them, by name, for every directive
// they ever steered and settled. A flood of mentions about work that is long
// over is the same trust erosion as silence, from the other side.
func (f *HarnessFeed) directiveDeliveries(cursors Cursors, streams map[string]struct{}) ([]Delivery, error) {
	if f.Directives == nil || f.Steers == nil {
		return nil, nil
	}
	streams[directiveStream] = struct{}{}
	cursor := cursors.Streams[directiveStream]
	steers, err := f.Steers.LoadSteers()
	if err != nil {
		return nil, fmt.Errorf("read which directives were said in a thread: %w", err)
	}
	if len(steers.Steers) == 0 {
		// A product nobody has steered from a thread has nothing here to answer,
		// which is every product until somebody replies to one.
		return nil, nil
	}
	recorded, err := f.Directives.List()
	if err != nil {
		return nil, fmt.Errorf("read what became of the recorded directives: %w", err)
	}
	var deliveries []Delivery
	advanced := cursor
	for _, directed := range recorded {
		steer, found := steers.Lookup(directed.ID)
		if !found || steer.Said || !directed.Resolved() {
			continue
		}
		if predates(cursors.Since, *directed.ResolvedAt) {
			// Settled before this product's reporting began, or before it began
			// again. It is read past on age rather than marked, because a mark is
			// what a cursor reset just threw away and this has to hold without one.
			continue
		}
		mark := outcomeMark + directed.ID
		if advanced.Has(mark) {
			continue
		}
		topic, err := notify.ParseTopic(steer.Topic)
		if err != nil {
			// The thread it was said in cannot be addressed, so there is nowhere to
			// say this. It is said here once and read past rather than retried on
			// every pass for as long as the sink runs.
			f.say("directive %s is remembered against %q, which names no thread, so what became of it was not said there: %v", directed.ID, steer.Topic, err)
			advanced = advanced.With(mark)
			deliveries = append(deliveries, Delivery{Stream: directiveStream, Cursor: advanced})
			continue
		}
		advanced = advanced.With(mark)
		deliveries = append(deliveries, Delivery{
			Stream:  directiveStream,
			Cursor:  advanced,
			Mention: steer.Member,
			Reply:   steer.Message,
			// How it was settled decides how it is said. A directive that paused
			// work is reported as resolved and a directive that paused nothing as
			// carried out, because the reader of the second one was never waiting
			// for work to resume — they were waiting to hear what came of what they
			// asked for.
			Notification: acknowledged(topic, settledKind(directed), directed, *directed.ResolvedAt),
		})
	}
	return deliveries, nil
}

// runDeliveries says what one run's record crossed since the reading already
// reported, and advances that reading only once the whole crossing has been
// posted. A crash halfway therefore repeats what it had already said rather than
// losing what it had not, which is the trade the design takes deliberately: the
// durable record is authoritative and this is a view of it.
func (f *HarnessFeed) runDeliveries(state runstate.State, cursor Cursor, since time.Time) ([]Delivery, error) {
	if cursor.Closed {
		return nil, nil
	}
	stream := runStream(state.RunID)
	var before runstate.State
	if cursor.Reported != nil {
		before = *cursor.Reported
	} else if len(cursor.Delivered) == 0 && predates(since, completion(state)) {
		// A run that was over before the watermark is history nobody turned
		// reporting on to read, so its cursor closes without a word. A run that
		// both started and finished while the sink was down is not that: it
		// finished after the watermark, so it is caught up on in full.
		//
		// A run this sink has already said something about is never history
		// whatever its dates say. Nothing backdates a completion today, so the
		// two cannot disagree, but if one ever did, a thread that stopped
		// mid-narrative is a worse answer than a run reported to its end.
		return []Delivery{{Stream: stream, Cursor: Cursor{Closed: true}}}, nil
	}

	crossed, err := notify.FromRun(before, state)
	if err != nil {
		// A run nothing can be addressed to is one nothing about it will ever be
		// postable to, whatever it goes on to do. Failing the pass would hold up
		// every other stream forever over one record, so it is said once and its
		// cursor closes.
		f.say("run %s could not be addressed and nothing will be reported about it: %v", state.RunID, err)
		return []Delivery{{Stream: stream, Cursor: Cursor{Closed: true}}}, nil
	}
	pending := make([]Delivery, 0, len(crossed))
	advanced := cursor
	for _, notification := range crossed {
		mark := markOf(notification.Event)
		if advanced.Has(mark) {
			continue
		}
		advanced = advanced.With(mark)
		pending = append(pending, Delivery{Stream: stream, Cursor: advanced, Notification: notification})
	}

	if len(pending) == 0 {
		// A run that is over and owes nothing has nothing left to cross, so the
		// reading it was being compared against is dropped rather than carried
		// for as long as the product exists.
		if settled(state) {
			return []Delivery{{Stream: stream, Cursor: Cursor{Closed: true}}}, nil
		}
		return nil, nil
	}
	// The last message of a crossing is what makes the crossing said, so it is
	// the one that moves the reading on. The marks it made redundant go with it:
	// they only ever existed to stop a crash repeating what was said against the
	// reading they were recorded under.
	settledState := state
	pending[len(pending)-1].Cursor = Cursor{Reported: &settledState}
	return pending, nil
}

// conversationDeliveries says what each of the product's conversations did to
// the backlog since the reading already reported. A conversation's log is an
// append-only log like the reports pile, so it advances by position: what it
// holds is mostly the turn itself — provider messages, tools, the reply as it
// was written — and the milestones are the few records among them where the
// queue actually moved. Everything else advances the position and says nothing.
func (f *HarnessFeed) conversationDeliveries(ctx context.Context, cursors Cursors, streams map[string]struct{}) ([]Delivery, error) {
	if f.Conversations == nil {
		return nil, nil
	}
	conversations, err := f.Conversations.Recorded()
	if err != nil {
		return nil, fmt.Errorf("read the recorded conversations: %w", err)
	}
	var deliveries []Delivery
	for _, conversation := range conversations {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		stream := conversationStream(conversation.ConversationID)
		streams[stream] = struct{}{}
		events, err := f.Conversations.LoadEvents(conversation.ConversationID)
		if err != nil {
			return nil, fmt.Errorf("read the log of conversation %s: %w", conversation.ConversationID, err)
		}
		said, err := f.logDeliveries(stream, cursors.Streams[stream], len(events), cursors.Since,
			func(index int) (time.Time, notify.Notification, error) {
				notification, err := notify.FromConversation(conversation, events, index)
				return events[index].Timestamp, notification, err
			})
		if err != nil {
			return nil, err
		}
		deliveries = append(deliveries, said...)
	}
	return deliveries, nil
}

// logDeliveries reads an append-only log from where the cursor left it. A record
// selection had nothing to say about is read past exactly as one that predates
// the watermark is: the position still moves, so a log that is mostly not
// milestones is not re-read from its beginning on every pass. What was filed
// before the watermark is read past in one silent advance, because a
// channel turned on today does not want a year of history arriving at once.
// Nothing after the watermark is ever read past on age: the watermark is a fixed
// moment rather than this process's start, so a record filed while the sink was
// down is still news when it comes back, which is the difference between an
// outage that delays messages and one that loses them.
func (f *HarnessFeed) logDeliveries(stream string, cursor Cursor, count int, since time.Time, at func(int) (time.Time, notify.Notification, error)) ([]Delivery, error) {
	var deliveries []Delivery
	skipped := cursor.Position
	for index := int(cursor.Position); index < count; index++ {
		recordedAt, notification, err := at(index)
		position := uint64(index + 1)
		if err != nil {
			// One record nobody can address must not hold up every record behind
			// it for as long as the process runs, so it is said once and read
			// past rather than retried forever.
			f.say("a record on the %s log could not be addressed and was skipped: %v", stream, err)
			skipped = position
			continue
		}
		if notification.Silent() || predates(since, recordedAt) {
			skipped = position
			continue
		}
		deliveries = append(deliveries, Delivery{
			Stream:       stream,
			Cursor:       Cursor{Position: position},
			Notification: notification,
		})
	}
	// Anything read past after the last message has to move the cursor on its
	// own, or it is read past again on every pass for as long as the sink runs.
	reached := cursor.Position
	if len(deliveries) > 0 {
		reached = deliveries[len(deliveries)-1].Cursor.Position
	}
	if skipped > reached {
		deliveries = append(deliveries, Delivery{Stream: stream, Cursor: Cursor{Position: skipped}})
	}
	return deliveries, nil
}

// holdDeliveries says the operator's two switches. Each is said when it is
// placed and again when it is lifted, and the lift is the awkward half: nothing
// records a release, so what says a hold has lifted is the hold's absence
// against a mark saying it was once there. The pair is forgotten once both have
// been said, so the product's cursor does not grow a line for every afternoon
// somebody was away.
func (f *HarnessFeed) holdDeliveries(cursor Cursor, read switches) []Delivery {
	var deliveries []Delivery
	advanced := cursor

	intake, held := read.intake, read.intakeHeld
	if held {
		if mark := intakeMark + stamp(intake.HeldAt); !advanced.Has(mark) {
			advanced = advanced.With(mark)
			deliveries = append(deliveries, Delivery{
				Stream:       productStream,
				Cursor:       advanced,
				Notification: notify.FromIntakeHold(intake),
			})
		}
	} else if mark, said := advanced.Marked(intakeMark); said {
		advanced = advanced.Without(mark)
		deliveries = append(deliveries, Delivery{
			Stream:       productStream,
			Cursor:       advanced,
			Notification: notify.IntakeReleased(f.now()),
		})
	}

	operator, held := read.operator, read.operatorHeld
	if held {
		if mark := holdMark + stamp(operator.HeldAt); !advanced.Has(mark) {
			advanced = advanced.With(mark)
			deliveries = append(deliveries, Delivery{
				Stream:       productStream,
				Cursor:       advanced,
				Notification: notify.FromOperatorHold(operator),
			})
		}
	} else if mark, said := advanced.Marked(holdMark); said {
		advanced = advanced.Without(mark)
		deliveries = append(deliveries, Delivery{
			Stream:       productStream,
			Cursor:       advanced,
			Notification: notify.HoldLifted(f.now()),
		})
	}
	return deliveries
}

func (f *HarnessFeed) say(format string, args ...any) {
	if f.Log != nil {
		f.Log(format, args...)
	}
}

func (f *HarnessFeed) now() time.Time {
	if f.Now != nil {
		return f.Now()
	}
	return time.Now().UTC()
}

// predates reports what the product recorded before the watermark. A record with
// no moment on it is never history, because absence of a date is not evidence of
// age; nor is anything at all when there is no watermark, which is what a caller
// asking for the whole record leaves behind.
func predates(since, at time.Time) bool {
	return !since.IsZero() && !at.IsZero() && at.Before(since)
}

// markOf names one crossing so that having posted it survives a crash. The kind
// alone would not do it: a check that fails, is repaired, and fails differently
// has crossed the same kind twice with two different things to say, and a mark
// that could not tell those apart would swallow the second. The moment is left
// out because every crossing of one reading carries the same one, and a mark
// that moved with it would match nothing.
func markOf(event notify.Event) string {
	digest := fnv.New64a()
	fmt.Fprintf(digest, "%v\x00%v\x00%s", event.Severity, event.Detail, event.Text)
	return string(event.Kind) + ":" + strconv.FormatUint(digest.Sum64(), 36)
}

// settled reports a run that is over and owes nothing further: terminal, with no
// publication still to be watched and no integration left half done.
func settled(state runstate.State) bool {
	return state.Status.Terminal() && !state.Outstanding()
}

// completion is when a run stopped, for deciding whether it is history. A run
// that has not stopped has no completion and is never history.
func completion(state runstate.State) time.Time {
	if state.CompletedAt == nil {
		return time.Time{}
	}
	return *state.CompletedAt
}

func stamp(at time.Time) string { return at.UTC().Format(time.RFC3339Nano) }
