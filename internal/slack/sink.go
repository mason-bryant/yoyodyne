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
	"unicode/utf8"

	"github.com/mason-bryant/yoyodyne/internal/notify"
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
	// API refuses it. The notifier bounds the body before this ever sees it, so
	// what this catches is the reference line pushing one over; either way a
	// message is cut with a marker naming the record that holds the whole, and
	// never split into a flood of messages.
	maxTextBytes = 3800
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
	// Avatars is the project's per-speaker override of the picture beside a name.
	// It is optional, and a sink given none posts every persona under the avatar
	// the harness ships.
	Avatars notify.Avatars
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
	// Now is read once, for the watermark this product's reporting begins at. It
	// is optional and exists so a test can say when a sink was first pointed at a
	// product without waiting for real time to pass.
	Now func() time.Time
}

// Sink is the long-running reporting process for one product.
type Sink struct {
	channel string
	avatars notify.Avatars
	store   *Store
	api     *API
	feed    Feed
	poll    time.Duration
	// refusal is how long to wait after a refusal only a person can clear. It is
	// a field only so a test can drive that path without spending the wait;
	// every sink the harness builds gets refusalBackoff.
	refusal time.Duration
	// now is the clock the watermark is taken from, injected for the same reason.
	now        func() time.Time
	log        func(format string, args ...any)
	connection *connection
}

// clock is when this sink says it is. It is a function rather than time.Now so a
// test can put a product's watermark where it needs it.
func (s *Sink) clock() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
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
		avatars: options.Avatars,
		store:   options.Store,
		api:     options.API,
		feed:    options.Feed,
		poll:    poll,
		refusal: refusalBackoff,
		now:     options.Now,
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
	// The watermark is taken once, on the first pass this product ever gets, and
	// written before anything is read. Taking it afresh on every start would move
	// it past every outage, and a report filed while the sink was down would then
	// be older than the restart and be read past as history — which is exactly
	// the record somebody coming back most needs to see.
	if cursors.Since.IsZero() {
		cursors.Since = s.clock().UTC()
		if err := s.store.SaveCursors(cursors); err != nil {
			return err
		}
		s.log("reporting on this product from %s; what it recorded before that is left in the durable records", cursors.Since.Format(time.RFC3339))
	}
	threads, err := s.store.LoadThreads()
	if err != nil {
		return err
	}
	batch, err := s.feed.Poll(ctx, cursors)
	if err != nil {
		return err
	}

	// Rendering is the notifier's, posting is the sink's, and this is the seam
	// between them: what a message says is decided once, by the package that
	// knows the personas, whatever ends up carrying it.
	into := &poster{sink: s, threads: &threads}
	notifier := notify.New(into, s.avatars)

	for _, delivery := range batch.Deliveries {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !delivery.Silent() {
			into.reached = false
			if err := delivery.Notification.Notify(ctx, notifier); err != nil {
				if into.reached {
					return err
				}
				// The notifier refused to say it at all, which is a record
				// nothing can be said about rather than a workspace that is
				// down. Stopping would hold up every later message on every
				// stream forever, so it is said once here and read past.
				s.log("a notification could not be said and was skipped: %v", err)
			}
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
// else is posted. What the cursor becomes is the feed's to decide, because it is
// the feed that knows what reading each stream was posted from.
func (s *Sink) advance(cursors *Cursors, delivery Delivery) error {
	if cursors.Streams == nil {
		cursors.Streams = map[string]Cursor{}
	}
	cursors.Streams[delivery.Stream] = delivery.Cursor
	return s.store.SaveCursors(*cursors)
}

// poster carries a rendered message into the workspace. It is the seam the
// notifier hands its work to: everything above it decided what is said and whose
// account it is, and everything below it knows about a channel, a connection,
// and a thread map.
type poster struct {
	sink    *Sink
	threads *ThreadMap
	// reached records that a message got as far as the workspace. It is what lets
	// the pass tell a record nothing could be said about — which it reads past —
	// from a workspace that refused it, which it retries. Without it the two are
	// one error and one of them would be treated as the other.
	reached bool
}

// Post puts one message in its topic's thread, opening the thread if this is the
// first thing anybody has said about that topic.
func (p *poster) Post(ctx context.Context, message notify.Message) error {
	sink := p.sink
	topic, err := notify.ParseTopic(message.Topic)
	if err != nil {
		return err
	}
	// The key says which thread; what the topic is called travels beside it,
	// because a header is read by somebody who has not read the tracker.
	topic = topic.WithTitle(message.TopicTitle)

	p.reached = true
	threadTS := ""
	// The product is the one topic that is not a thread: what is about the whole
	// line rather than any one item goes to the top of the channel, because
	// burying it in some item's thread would misfile it.
	if topic.Kind != notify.TopicProduct {
		thread, found := p.threads.Lookup(sink.channel, message.Topic)
		if !found {
			opened, err := sink.openThread(ctx, topic)
			if err != nil {
				return err
			}
			thread = opened
			p.threads.Record(message.Topic, thread)
			// The map is written before the message that goes in the thread, so
			// a sink killed between them replies into the thread it opened
			// rather than opening a second one for the same topic.
			if err := sink.store.SaveThreads(*p.threads); err != nil {
				return err
			}
		}
		threadTS = thread.ThreadTS
	}

	emoji, url := icon(message.Identity.Avatar)
	if _, err := sink.api.Post(ctx, Message{
		Channel:   sink.channel,
		Text:      renderText(message),
		ThreadTS:  threadTS,
		Username:  message.Identity.Name,
		IconEmoji: emoji,
		IconURL:   url,
	}); err != nil {
		return err
	}
	// What was posted is said in the sink's own log, by kind and topic and never
	// by body: a process that runs for days with no output cannot be told from
	// one that has quietly stopped working, and somebody following the setup
	// document needs to see that the first message actually went.
	sink.log("posted %s about %s", message.Kind, label(topic))
	return nil
}

// openThread posts the message a topic's thread hangs from. It names the topic
// and nothing else: the thread's first reply is the first thing that actually
// happened, and a header that summarized it would be a summary written before
// the events it summarizes. Naming it is the identifier and what the record
// calls the topic, because a channel is read by people and an identifier on its
// own is a name somebody has to go and resolve before they know what the thread
// is about. It is opened by the harness rather than by whichever persona
// happened to speak first, because opening a thread is not anybody's account of
// anything.
func (s *Sink) openThread(ctx context.Context, topic notify.Topic) (Thread, error) {
	identity := s.avatars.Identity(notify.Harness())
	emoji, url := icon(identity.Avatar)
	ts, err := s.api.Post(ctx, Message{
		Channel:   s.channel,
		Text:      fmt.Sprintf("*%s*", header(topic)),
		Username:  identity.Name,
		IconEmoji: emoji,
		IconURL:   url,
	})
	if err != nil {
		return Thread{}, fmt.Errorf("open the thread for %s: %w", topic.Key(), err)
	}
	return Thread{Channel: s.channel, ThreadTS: ts, OpenedAt: time.Now().UTC()}, nil
}

// icon splits one avatar into the two fields Slack takes for it: a shortcode
// into icon_emoji, an image into icon_url. The split is here rather than in the
// notifier because it is Slack's shape and not the persona's — nothing about
// which field carries the picture is a fact about who is speaking.
func icon(avatar string) (emoji, url string) {
	trimmed := strings.TrimSpace(avatar)
	if strings.HasPrefix(trimmed, "https://") || strings.HasPrefix(trimmed, "http://") {
		return "", trimmed
	}
	return trimmed, ""
}

// header names a topic the way the message a thread hangs from does: the
// identifier first, because that is what somebody scanning a channel matches
// against and what a reply has to quote, and then what the record calls the
// topic, which is what tells a reader what the thread is for without their
// resolving anything. A topic whose record carried no title is headed by the
// identifier alone, exactly as every thread was before titles were carried.
func header(topic notify.Topic) string {
	named := label(topic)
	if topic.Title == "" {
		return named
	}
	return named + " — " + topic.Title
}

// label names a topic in the sink's own log and inside a header: the item's own
// identifier, and an exchange said as one so a thread of questions is not
// mistaken for an item.
func label(topic notify.Topic) string {
	switch topic.Kind {
	case notify.TopicExchange:
		return "exchange " + topic.ID
	case notify.TopicProduct:
		return "this product"
	default:
		return topic.ID
	}
}

// renderText is the message as it appears in the channel: what the persona said,
// and the records it was read from.
//
// The words are the notifier's and nothing here changes them — the severity is
// already said in them, and already bounded, because how a message reads is
// decided once by the package that knows the personas. What is added is the way
// back to the evidence, so a message leads to the record rather than standing in
// for it.
func renderText(message notify.Message) string {
	text := strings.TrimSpace(message.Body)
	if reference := renderRefs(message.Refs); reference != "" {
		text += "\n" + reference
	}
	return truncate(text, message.Refs)
}

// renderRefs names the durable records this message was read from.
func renderRefs(refs notify.Refs) string {
	parts := make([]string, 0, 4)
	for _, reference := range []struct{ label, value string }{
		{label: "run", value: refs.RunID},
		{label: "conversation", value: refs.ConversationID},
		{label: "exchange", value: refs.ExchangeID},
		{label: "directive", value: refs.DirectiveID},
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

// truncate keeps a message inside Slack's limit, and says where the rest is. The
// notifier already bounds the body it renders, so nothing that arrives here in
// good order is near this; what it guards is a reference line long enough to
// push one over, which Slack would refuse forever rather than once. A message
// too long to post is one whose record holds the whole of it, so the marker
// names that record rather than apologizing.
func truncate(text string, refs notify.Refs) string {
	if len(text) <= maxTextBytes {
		return text
	}
	marker := "\n… truncated; the whole of it is in " + refs.Record()
	cut := maxTextBytes - len(marker)
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return text[:cut] + marker
}
