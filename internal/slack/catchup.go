package slack

// Surviving a long gap: how fast the sink posts, and what it does with a backlog
// too deep to post one message at a time.
//
// Both halves exist because of the same discovery. Slack does not delay an
// application that posts faster than it tolerates — it suppresses, and a
// suppressed message is hidden permanently rather than late. An unpaced catch-up
// therefore turns this design's arrive-late-never-dropped promise into
// dropped-from-view for whatever overflowed, which is the one outcome the
// durable record cannot repair from the reader's side: they never see there was
// anything to look for.
//
// So posting is paced to what Slack sustains, and a backlog deeper than a reader
// would scroll is digested per thread rather than replayed. The digest is not a
// concession to the rate limit — four hundred replayed messages hide the signal
// as thoroughly as suppression does, and nobody scrolls them. One line per
// thread saying how much accumulated, with the durable record named as the full
// account, is what both the workspace and the reader actually want.

import (
	"context"
	"sync"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/notify"
	"github.com/mason-bryant/yoyodyne/internal/report"
)

const (
	// DefaultPostInterval is the sink's own pace: roughly what Slack accepts from
	// one application in one channel indefinitely. It is deliberately the
	// sustained rate rather than the burst allowance, because a burst spent on
	// catch-up is a burst spent exactly when there is most to lose — and nothing
	// here is a live feed, so a message that goes a second later goes a second
	// later.
	DefaultPostInterval = time.Second
	// deepBacklog is how many messages one pass has to hold before any of them are
	// digested. Below it the whole backlog posts in full: a minute of paced
	// catch-up is not a flood, and a digest that stood in for a handful of
	// messages would be less than the messages it replaced.
	deepBacklog = 60
	// recentWindow is what is posted in full whatever the depth of the backlog.
	// It is what a reader coming back to a channel is actually reading for —
	// what is happening now — and it is short enough that twelve hours of
	// catch-up is mostly digest rather than mostly replay.
	recentWindow = 30 * time.Minute
)

// pacer holds posting down to a rate the workspace keeps accepting. It is per
// sink, and a sink is per channel, which is the unit Slack's own tolerance is
// measured in.
type pacer struct {
	every time.Duration
	// now and sleep are the clock and the wait, injected so a test can watch the
	// pace without spending it.
	now   func() time.Time
	sleep func(ctx context.Context, wait time.Duration) error
	// mu holds one message in here at a time. Two goroutines post through a sink
	// — the delivery loop, and the connection acknowledging a reply — and a pace
	// two callers each advanced from their own reading of it is not a pace.
	mu sync.Mutex
	// next is the earliest moment the next message may go.
	next time.Time
}

// wait blocks until the next message is due, and records when the one after it
// is. A context that ends while it is waiting stops the wait rather than the
// pace: the cursors are on disk, so what did not go now goes on the next pass.
func (p *pacer) wait(ctx context.Context) error {
	if p == nil || p.every <= 0 {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	at := p.now()
	if due := p.next.Sub(at); due > 0 {
		if err := p.sleep(ctx, due); err != nil {
			return err
		}
		at = p.now()
	}
	p.next = at.Add(p.every)
	return nil
}

// catchUp is what one pass decided about a backlog: which deliveries are
// digested away, and the one message that stands for each thread's accumulation.
//
// It is decided for the whole batch before anything is posted, because a digest
// has to say how many events it stands for and nothing walking the deliveries
// one at a time knows that yet. A zero catchUp digests nothing, which is every
// ordinary pass.
type catchUp struct {
	// digest is the message to post in place of the delivery at this index.
	digest map[int]notify.Notification
	// collapsed is the deliveries the digest already stands for. Their cursors
	// still advance — the events are reported, by the digest, and re-reading them
	// on the next pass would digest them a second time.
	collapsed map[int]struct{}
}

// say reports what to post for one delivery, and whether to post anything at
// all.
func (c catchUp) say(index int, delivery Delivery) (notify.Notification, bool) {
	if digest, found := c.digest[index]; found {
		return digest, true
	}
	if _, found := c.collapsed[index]; found {
		return notify.Notification{}, false
	}
	return delivery.Notification, !delivery.Silent()
}

// planCatchUp decides what a batch is too deep to say in full, and digests the
// older half of it per thread.
//
// Three things are never digested, and each of them is the reason a reader is
// looking at the channel at all: anything in a backlog shallow enough to post,
// anything inside the recent window, and anything critical. A critical event is
// the one thing a digest must not swallow — important findings standing out is
// what the surface is for, and a count is not a finding.
func planCatchUp(deliveries []Delivery, now time.Time) catchUp {
	pending := 0
	for _, delivery := range deliveries {
		if !delivery.Silent() {
			pending++
		}
	}
	if pending < deepBacklog {
		return catchUp{}
	}

	horizon := now.Add(-recentWindow)
	// The threads are gathered in the order the batch reaches them, so what is
	// planned does not depend on the iteration order of a map.
	var order []string
	gathered := map[string]*accumulation{}
	for index, delivery := range deliveries {
		if !digestible(delivery, horizon) {
			continue
		}
		topic := delivery.Notification.Topic
		key := topic.Key()
		thread, found := gathered[key]
		if !found {
			thread = &accumulation{at: index}
			thread.Accumulation.Topic = topic
			thread.Accumulation.Since = delivery.Notification.Event.At
			gathered[key] = thread
			order = append(order, key)
		}
		thread.add(index, delivery.Notification)
	}

	plan := catchUp{digest: map[int]notify.Notification{}, collapsed: map[int]struct{}{}}
	for _, key := range order {
		thread := gathered[key]
		// One event on its own is not an accumulation. Standing a digest in front
		// of a single message would say strictly less than the message did.
		if thread.Events < 2 {
			continue
		}
		plan.digest[thread.at] = notify.FromAccumulation(thread.Accumulation)
		for _, index := range thread.indices[1:] {
			plan.collapsed[index] = struct{}{}
		}
	}
	return plan
}

// digestible reports one delivery that may be collapsed into its thread's
// digest: something that would have been posted, old enough to be accumulation
// rather than news, and not the one severity a count must never stand in for. An
// event with no moment on it is never old, for the reason a record with no date
// is never history: absence of a date is not evidence of age.
func digestible(delivery Delivery, horizon time.Time) bool {
	if delivery.Silent() {
		return false
	}
	event := delivery.Notification.Event
	if event.Severity == report.SeverityCritical {
		return false
	}
	return !event.At.IsZero() && event.At.Before(horizon)
}

// accumulation is one thread's collapsed events while the plan is being built:
// what the digest will say, which delivery it is posted in place of, and which
// deliveries it stands for.
type accumulation struct {
	notify.Accumulation
	at      int
	indices []int
}

func (a *accumulation) add(index int, notification notify.Notification) {
	event := notification.Event
	a.indices = append(a.indices, index)
	a.Events++
	if event.At.Before(a.Since) {
		a.Since = event.At
	}
	if event.At.After(a.At) {
		a.At = event.At
	}
	a.Severity = louder(a.Severity, event.Severity)
	// A title arrives with whichever record carried one, so a thread opened by a
	// digest is headed exactly as one opened by any other message would be.
	if a.Topic.Title == "" {
		a.Topic = a.Topic.WithTitle(notification.Topic.Title)
	}
}

// louder is the more attention-asking of two severities, so a digest is marked
// by the loudest thing it collapsed rather than by the last one.
func louder(first, second report.Severity) report.Severity {
	if attention(second) > attention(first) {
		return second
	}
	return first
}

func attention(severity report.Severity) int {
	switch severity {
	case report.SeverityCritical:
		return 3
	case report.SeverityWarning:
		return 2
	case report.SeverityNote:
		return 1
	default:
		return 0
	}
}
