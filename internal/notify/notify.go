// Package notify turns what the durable record says happened into one message
// per reportable event, said in the voice of the persona whose account it is
// and addressed to the topic it belongs to.
//
// Work the harness runs on its own is acceptable only while it is visible, and
// today visibility means a terminal somebody is sitting at. This is the same
// account of the work, shaped so it can be carried somewhere an operator
// already reads. What carries it is a separate long-running sink
// (yoyodyne-ifd.68.3); what is here is everything that decides what gets said,
// who says it, and where it is addressed, so the transport can be replaced
// without touching any of that.
//
// Three things follow from the design and shape the whole package:
//
// The source is the durable record, never the running process. Selection reads
// recorded state and recorded reports rather than being called from the
// pipeline as it goes, so what is said is what somebody reading the record
// afterwards would say. Nothing here can fail a run, because nothing here is
// on the path of one.
//
// The voice is deterministic and the agent's own words are verbatim. A harness
// fact — a run starting, checks passing, a verdict, a promotion — is rendered
// from the event by that persona's voice template, with no provider invocation
// anywhere in it: a model paraphrasing "checks failed" is a model that can
// misstate it. Text an agent actually wrote is carried through unchanged, in
// the message its persona speaks, which is where the genuine voice already is.
//
// One message per event, one topic per thread. The topic is the thread key, so
// boundaries between topics are visible; the voice is the speaker, so
// boundaries between speakers are visible without anybody reading a header.
// Severity is said in words before it is said in decoration, so the distinction
// survives anywhere the decoration does not.
package notify

import "context"

// Notifier is what a producer of events talks to. It takes the topic the event
// belongs to, the persona whose account it is, and the event itself, and it
// assumes nothing whatever about the producer: runs were the first one and
// conversations the second, branch reviews are the same shape, and ask exchanges
// arrive later with exchange topics. A new producer adds nothing here.
type Notifier interface {
	Notify(ctx context.Context, topic Topic, speaker Speaker, event Event) error
}

// Poster carries a rendered message to wherever it is read. It is the seam the
// sink implements: everything above it decides what is said, and everything
// below it knows about a workspace, a connection, and a thread map.
type Poster interface {
	Post(ctx context.Context, message Message) error
}

// New returns the notifier that renders each event into its persona's voice and
// hands the message to the poster. Rendering happens here rather than in the
// poster so that what a message says is decided once, by this package, whatever
// ends up carrying it.
//
// The avatars are the project's overrides of the picture beside each name, and
// are nil for a project that configured none. They are applied here rather than
// inside Render because what the voice table says is what the harness ships: a
// default a configuration happens to sit in front of is still the default.
func New(poster Poster, avatars Avatars) Notifier {
	return notifier{poster: poster, avatars: avatars}
}

type notifier struct {
	poster  Poster
	avatars Avatars
}

func (n notifier) Notify(ctx context.Context, topic Topic, speaker Speaker, event Event) error {
	message, err := Render(topic, speaker, event)
	if err != nil {
		return err
	}
	message.Identity = n.avatars.Identity(speaker)
	if n.poster == nil {
		return nil
	}
	return n.poster.Post(ctx, message)
}

// Discard is the notifier a product with no reporting configured gets. It is
// not an error to have nowhere to report to: reporting is observation, so a
// harness nobody has pointed at a workspace behaves exactly as it did before
// there was one.
type Discard struct{}

func (Discard) Notify(context.Context, Topic, Speaker, Event) error { return nil }

// Notification is one reportable event with everything needed to say it: what
// happened, the topic it is addressed to, and the persona whose account it is.
// It is what selection produces, because reading a record answers all three
// questions at once and separating them would make a caller re-derive two of
// them.
type Notification struct {
	Topic   Topic
	Speaker Speaker
	Event   Event
}

// Notify hands one selected notification to a notifier, so a caller looping
// over what a record crossed does not unpack the same three fields every time.
func (n Notification) Notify(ctx context.Context, to Notifier) error {
	return to.Notify(ctx, n.Topic, n.Speaker, n.Event)
}

// Silent reports a notification with nothing to say. Selection over a log
// produces one for every record that is not a milestone, which is most of a
// conversation's: the reading still has to move past them, and nobody is told
// about them.
func (n Notification) Silent() bool { return n.Event.Kind == "" }
