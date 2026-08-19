package slack

// The sink itself: read the durable records, post what has not been posted, and
// remember how far it got.
//
// The ordering here is the whole of the delivery guarantee. A message is posted
// and then its cursor is advanced, so a process killed between the two repeats
// one message when it comes back. The other order would lose one instead, and a
// lost message is a lie about the work while a repeated one is only a repetition
// — the durable record is authoritative, and this is a view of it.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/notify"
	"github.com/mason-bryant/yoyodyne/internal/report"
)

const (
	// DefaultPollInterval is how often the sink reads the durable records. It is
	// seconds rather than milliseconds because nothing here is a live feed: the
	// records are files other processes write, and a channel that is a few
	// seconds behind a terminal is a channel nobody is waiting on.
	DefaultPollInterval = 15 * time.Second
	// maxTextBytes bounds one posted message. It is far inside what Slack
	// accepts, and deliberately so: the bound that matters is the one past which
	// a message stops being readable in a channel, not the one past which the
	// API refuses it. A body over it is truncated with a marker naming the
	// record that holds the whole, and never split into a flood of messages.
	maxTextBytes = 8 << 10
	// passBackoff is how long the sink waits after a pass that could not finish.
	// Nothing is lost by waiting: the cursors are on disk, so the next pass
	// resumes exactly where this one stopped.
	passBackoff = 30 * time.Second
	// refusalBackoff is how long the sink waits after a refusal that will still
	// be a refusal next time — an app nobody invited to the channel, a token
	// somebody revoked. It is minutes rather than seconds because nothing about
	// waiting less would help: what clears it is a person, and asking a
	// workspace to refuse the same call every fifteen seconds until they get to
	// it is noise in their audit log and ours. The wait is still finite, so an
	// operator who fixes it gets reporting back without restarting anything.
	refusalBackoff = 5 * time.Minute
)

// Options is everything the sink needs. Every field is required except the
// optional ones named as such, because a sink assembled with a missing piece
// would discover it while a run was in flight.
type Options struct {
	// Channel is where threads are opened.
	Channel string
	// Store holds the thread map and the cursors.
	Store *Store
	// API posts.
	API *API
	// Feed says what there is to post.
	Feed Feed
	// Poll is the interval between passes; zero takes the default.
	Poll time.Duration
	// Log is where the sink says what it is doing. It is the operator's only
	// window onto a process that otherwise runs silently, and it is never given
	// a token to print.
	Log func(format string, args ...any)
	// Inbound is what to do with a message that arrives on the connection.
	// Nothing today: the inbound half maps a reply onto the existing directive
	// record, and until it exists a reply is acknowledged and no more.
	Inbound InboundHandler
	// Dial opens the websocket transport. It is optional and exists so a test
	// can exercise the connection without a network.
	Dial dialFunc
}

// Sink is the long-running reporting process for one product.
type Sink struct {
	channel string
	store   *Store
	api     *API
	feed    Feed
	poll    time.Duration
	// refusal is how long to wait after a refusal only a person can clear. It is
	// a field only so a test can drive that path without spending the wait;
	// every sink the harness builds gets refusalBackoff.
	refusal    time.Duration
	log        func(format string, args ...any)
	connection *connection
}

func New(options Options) (*Sink, error) {
	var problems []error
	if strings.TrimSpace(options.Channel) == "" {
		problems = append(problems, errors.New("a channel is required"))
	}
	if options.Store == nil {
		problems = append(problems, errors.New("the sink's own state store is required"))
	}
	if options.API == nil {
		problems = append(problems, errors.New("a Slack client is required"))
	}
	if options.Feed == nil {
		problems = append(problems, errors.New("a feed is required; a sink with nothing to read reports nothing"))
	}
	if err := errors.Join(problems...); err != nil {
		return nil, fmt.Errorf("the Slack sink cannot start: %w", err)
	}
	log := options.Log
	if log == nil {
		log = func(string, ...any) {}
	}
	poll := options.Poll
	if poll <= 0 {
		poll = DefaultPollInterval
	}
	return &Sink{
		channel: strings.TrimSpace(options.Channel),
		store:   options.Store,
		api:     options.API,
		feed:    options.Feed,
		poll:    poll,
		refusal: refusalBackoff,
		log:     log,
		connection: &connection{
			api:    options.API,
			dial:   options.Dial,
			handle: options.Inbound,
			log:    log,
		},
	}, nil
}

// Run holds the connection open and posts until the context ends.
//
// It takes the product's sink lease first and refuses to start without it. Two
// sinks are not redundancy: each holds its own thread map, so the second opens
// its own threads and posts everything a second time, and an operator who
// started one twice would see a channel that looks broken rather than one that
// is doubled.
func (s *Sink) Run(ctx context.Context) error {
	release, err := s.hold()
	if err != nil {
		return err
	}
	defer release()

	if identity, err := s.api.Identify(ctx); err != nil {
		// A workspace that will not say who this is may still accept posts, and
		// reporting is never a gate, so this is said and not obeyed.
		s.log("Slack did not confirm this app's identity: %v", err)
	} else {
		s.log("reporting to %s as %s in workspace %s", s.channel, identity.User, identity.Team)
	}

	connected := make(chan struct{})
	go func() {
		defer close(connected)
		s.connection.run(ctx)
	}()

	err = s.deliver(ctx)
	<-connected
	return err
}

// Once makes a single pass and returns. It is what a setup document can tell
// somebody to run: it posts whatever is already due and leaves nothing behind
// it. It holds the same lease a running sink does, because a pass posts, and
// two things posting from two thread maps is exactly what the lease is for.
func (s *Sink) Once(ctx context.Context) error {
	release, err := s.hold()
	if err != nil {
		return err
	}
	defer release()
	return s.pass(ctx)
}

// hold makes this process the product's only sink and reports how to stop
// being it.
func (s *Sink) hold() (func(), error) {
	lease, held, err := s.store.Lease()
	if err != nil {
		return nil, err
	}
	if !held {
		return nil, errors.New("another Slack sink is already running for this product; one sink per product is what keeps one thread per work item")
	}
	return func() {
		if err := lease.Release(); err != nil {
			s.log("the Slack sink lease could not be released: %v", err)
		}
	}, nil
}

// deliver runs passes until the context ends.
//
// A refusal only a person can clear — an app nobody invited to the channel, a
// revoked token, a missing scope — is said once and then not said again while it
// stands. The alternative is the same line every pass for as long as the process
// runs, which is how a log stops being read: the operator has to fix it, and
// repeating the instruction every fifteen seconds does not make them fix it
// sooner. It is still retried, because what clears it is somebody doing
// something in Slack rather than anything here, and reporting has to come back
// on its own when they do — which is the other half of why it is said again when
// it does.
func (s *Sink) deliver(ctx context.Context) error {
	// standing is the refusal currently being waited out, empty when posting
	// works. It is what makes the first refusal news and the tenth silence.
	standing := ""
	for {
		wait := s.poll
		err := s.pass(ctx)
		switch {
		case ctx.Err() != nil:
			return nil
		case err == nil:
			if standing != "" {
				s.log("Slack is accepting messages again; reporting has caught up")
				standing = ""
			}
		case PermanentError(err):
			wait = s.refusal
			if refusal := err.Error(); refusal != standing {
				standing = refusal
				s.log("Slack will keep refusing this until somebody changes something in the workspace; it is retried quietly every %s: %v", wait, err)
			}
		default:
			standing = ""
			s.log("this pass over the records could not finish; the cursors are unchanged and it will be retried: %v", err)
			wait = passBackoff
		}
		if !sleepUntil(ctx, wait) {
			return nil
		}
	}
}

// pass reads the records once and posts what is due. It is the whole of the
// sink's behavior: staying open is this, repeated.
func (s *Sink) pass(ctx context.Context) error {
	cursors, err := s.store.LoadCursors()
	if err != nil {
		return err
	}
	threads, err := s.store.LoadThreads()
	if err != nil {
		return err
	}
	batch, err := s.feed.Poll(ctx, cursors)
	if err != nil {
		return err
	}

	for _, delivery := range batch.Deliveries {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := delivery.Envelope.Validate(); err != nil {
			// An envelope that cannot be posted is not a reason to stop reading:
			// it would hold up every later message on the same stream forever. It
			// is said once here and its cursor advances past it.
			s.log("a notification could not be posted and was skipped: %v", err)
			if err := s.advance(&cursors, delivery); err != nil {
				return err
			}
			continue
		}
		if err := s.post(ctx, &threads, delivery.Envelope); err != nil {
			return err
		}
		if err := s.advance(&cursors, delivery); err != nil {
			return err
		}
	}

	// Cursors for streams that no longer exist are dropped only after a pass
	// that read them all, so a feed that failed halfway never looks like a
	// product whose runs have gone away.
	if len(batch.Streams) > 0 && cursors.Keep(batch.Streams) {
		if err := s.store.SaveCursors(cursors); err != nil {
			return err
		}
	}
	return nil
}

// advance records that one delivery has been posted, durably, before anything
// else is posted.
func (s *Sink) advance(cursors *Cursors, delivery Delivery) error {
	if cursors.Streams == nil {
		cursors.Streams = map[string]Cursor{}
	}
	cursor := cursors.Streams[delivery.Stream]
	switch {
	case delivery.Mark != "":
		cursor = cursor.With(delivery.Mark)
	case delivery.Position > cursor.Position:
		cursor.Position = delivery.Position
	}
	cursors.Streams[delivery.Stream] = cursor
	return s.store.SaveCursors(*cursors)
}

// post puts one envelope in its topic's thread, opening the thread if this is
// the first thing anybody has said about that topic.
func (s *Sink) post(ctx context.Context, threads *ThreadMap, envelope notify.Envelope) error {
	threadTS := ""
	if envelope.Topic.Threaded() {
		thread, found := threads.Lookup(s.channel, envelope.Topic)
		if !found {
			opened, err := s.openThread(ctx, envelope.Topic)
			if err != nil {
				return err
			}
			thread = opened
			threads.Record(envelope.Topic, thread)
			// The map is written before the message that goes in the thread, so
			// a sink killed between them replies into the thread it opened
			// rather than opening a second one for the same topic.
			if err := s.store.SaveThreads(*threads); err != nil {
				return err
			}
		}
		threadTS = thread.ThreadTS
	}

	if _, err := s.api.Post(ctx, Message{
		Channel:   s.channel,
		Text:      renderText(envelope),
		ThreadTS:  threadTS,
		Username:  envelope.Speaker.Name(),
		IconEmoji: speakerIcon(envelope.Speaker),
	}); err != nil {
		return err
	}
	// What was posted is said in the sink's own log, by kind and topic and never
	// by body: a process that runs for days with no output cannot be told from
	// one that has quietly stopped working, and somebody following the setup
	// document needs to see that the first message actually went.
	s.log("posted %s about %s", envelope.Kind, envelope.Topic.Label())
	return nil
}

// openThread posts the message a topic's thread hangs from. It names the topic
// and nothing else: the thread's first reply is the first thing that actually
// happened, and a header that summarized it would be a summary written before
// the events it summarizes.
func (s *Sink) openThread(ctx context.Context, topic notify.Topic) (Thread, error) {
	ts, err := s.api.Post(ctx, Message{
		Channel:  s.channel,
		Text:     fmt.Sprintf("*%s*", topic.Label()),
		Username: notify.Harness.Name(),
	})
	if err != nil {
		return Thread{}, fmt.Errorf("open the thread for %s: %w", topic, err)
	}
	return Thread{Channel: s.channel, ThreadTS: ts, OpenedAt: time.Now().UTC()}, nil
}

// renderText is the message as it appears in the channel: the severity where it
// carries news, the body, and the record it came from.
//
// The word is what carries the severity and the icon only adds to it, so a
// client that renders no emoji still shows a critical as critical. A note takes
// no marker at all, because a channel that labelled every ordinary fact would
// teach a reader to skip the label on the one that was not ordinary.
func renderText(envelope notify.Envelope) string {
	var builder strings.Builder
	switch envelope.Severity {
	case report.SeverityCritical:
		builder.WriteString(":rotating_light: *critical* — ")
	case report.SeverityWarning:
		builder.WriteString(":warning: *warning* — ")
	}
	builder.WriteString(strings.TrimSpace(envelope.Body))
	if reference := renderRefs(envelope.Refs); reference != "" {
		builder.WriteString("\n" + reference)
	}
	return truncate(builder.String(), envelope.Refs)
}

// renderRefs names the durable records this message was read from, so a message
// leads back to the evidence rather than standing in for it.
func renderRefs(refs notify.Refs) string {
	parts := make([]string, 0, 4)
	for _, reference := range []struct{ label, value string }{
		{label: "run", value: refs.Run},
		{label: "conversation", value: refs.Conversation},
		{label: "exchange", value: refs.Exchange},
		{label: "directive", value: refs.Directive},
	} {
		if strings.TrimSpace(reference.value) != "" {
			parts = append(parts, reference.label+" "+reference.value)
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return "_" + strings.Join(parts, " · ") + "_"
}

// truncate keeps a message inside Slack's limit, and says where the rest is. A
// body too long to post is a body whose record holds the whole of it, so the
// marker names that record rather than apologizing.
func truncate(text string, refs notify.Refs) string {
	if len(text) <= maxTextBytes {
		return text
	}
	marker := "\n… truncated; the whole of it is in the durable record"
	if run := strings.TrimSpace(refs.Run); run != "" {
		marker = "\n… truncated; the whole of it is in the record of run " + run
	}
	return text[:maxTextBytes-len(marker)] + marker
}

// speakerIcon gives each role a face of its own, so a reader tells the speakers
// apart at a glance and not only by the name on the message. The harness gets
// one too, because "what no persona did" is itself a speaker.
func speakerIcon(speaker notify.Speaker) string {
	switch speaker.Role {
	case domain.RoleProductManager:
		return ":compass:"
	case domain.RoleArchitect:
		return ":triangular_ruler:"
	case domain.RoleDevelopmentManager:
		return ":clipboard:"
	case domain.RoleDeveloper:
		return ":hammer_and_wrench:"
	case domain.RoleReviewer:
		return ":mag:"
	default:
		return ":yo-yo:"
	}
}
