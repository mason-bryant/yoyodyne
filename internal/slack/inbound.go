package slack

// The inbound half: a reply in a work item's thread reaches the work as a
// durable directive.
//
// It creates no directive machinery, and that is the whole of why a thread is
// allowed to steer anything. A reply is a new *receiver* for the record `yoyo
// directive record` already writes — same kinds, same pause semantics, same
// resolution, same store the run pipeline consults before it starts a run,
// before it resumes one, and before it puts a change through its gate. A
// directive recorded from a thread is indistinguishable downstream from one
// typed at a terminal, which is what "enforceable regardless of which agent
// received it" costs and all it costs. Nothing here can weaken directive
// governance, because nothing here has governance of its own to weaken.
//
// Three rules hold the channel to that:
//
// The pausing kinds are stated, never inferred. A reply opening `ambiguous:` or
// `artifact:` records that kind and must say what is unresolved; everything else
// is operational. A classifier deciding which sentences stop work is exactly
// what the command line already refused, and it would be worse here, where the
// input is chat.
//
// Authority defaults closed. A reply acts only when its Slack member id belongs
// to a human the project granted direct-work, and the derivation of that list is
// the configuration's rather than this file's. An unlisted reply is answered in
// the thread saying it was not acted on, because a channel that silently ignores
// some people looks broken rather than closed.
//
// Every message this reads is answered in its own thread — the directive as
// recorded with its identifier, or the refusal with its reason. An operator who
// steers from a phone has nothing else to tell them whether they were heard.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/mason-bryant/yoyodyne/internal/directive"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/notify"
	"github.com/mason-bryant/yoyodyne/internal/report"
)

// Directives is the durable directive record as the inbound half writes to it:
// recording one, and settling one an operator names. It is an interface here for
// the reason the feed is — the sink is assembled from the parts it needs rather
// than reaching for the state root itself — and it is deliberately these two
// methods and no more. Reading directives is the run pipeline's, and a reporting
// process that could revise one would be a second authority over the record.
type Directives interface {
	Record(recorded directive.Directive) error
	Resolve(reference, resolution string, at time.Time) (directive.Directive, error)
}

const (
	// The stated openings. A reply beginning with one of them records that kind
	// and has to say what is unresolved; a reply beginning with neither is
	// operational, which is the ordinary case and the one that stops nothing.
	ambiguousOpening = "ambiguous:"
	artifactOpening  = "artifact:"
	// resolveVerb settles a directive somebody recorded, by any prefix of its
	// identifier that names exactly one — the same rule the command line follows,
	// because it is the same store answering.
	resolveVerb = "resolve"
	// socketEventsAPI is the envelope type an event arrives in. Everything else on
	// the connection — a hello, a disconnect, an interaction this app does not
	// enable — is not a message and is not read here.
	socketEventsAPI = "events_api"
	// maxRememberedReplies bounds what one process remembers having acted on. It
	// exists for one case: Slack redelivers an envelope whose acknowledgment did
	// not reach it, and a redelivered reply recorded twice is two directives an
	// operator has to resolve for one thing they said. A few hundred is far more
	// than a redelivery window holds, and the memory is per process rather than
	// durable because the redelivery is too.
	maxRememberedReplies = 256
)

// steering is what the sink does with what arrives on its connection.
type steering struct {
	sink       *Sink
	directives Directives
	// operators is the allow-list, by Slack member id. It is derived from the
	// project's operator mapping — the humans granted direct-work who bound a
	// member id — and an empty one is a product nobody may steer from Slack,
	// which is what every workspace gets until somebody names themselves.
	operators map[string]bool
	// mu guards what this process has already acted on, because replies arrive on
	// the connection's goroutine while the delivery loop runs on its own.
	mu      sync.Mutex
	acted   map[string]bool
	ordered []string
}

func newSteering(sink *Sink, directives Directives, operators []string) *steering {
	allowed := make(map[string]bool, len(operators))
	for _, member := range operators {
		if trimmed := strings.TrimSpace(member); trimmed != "" {
			allowed[trimmed] = true
		}
	}
	return &steering{
		sink:       sink,
		directives: directives,
		operators:  allowed,
		acted:      map[string]bool{},
	}
}

// handle reads one inbound envelope and, where it is a reply in a thread this
// sink opened, answers it in that thread.
//
// Two kinds of thread qualify, and they are the same rule rather than two. A
// work item's thread in the channel is where the harness reports; a direct
// message is where it interrupts somebody because the system has stopped on
// them. Both were opened by this sink, both are correlated back through what it
// wrote down when it opened them, and the answer in either is recorded in the
// same directive record with the same identity check in front of it. Anything in
// a thread this sink did not open is somebody's own conversation.
//
// It reports nothing to its caller and stops nothing when it fails. Reporting is
// an observation rather than a gate in both directions: a reply that could not be
// recorded is said in the thread it arrived in and in the sink's own log, and the
// connection carries on reading. What an operator must never get is silence, so
// every path out of here either answers in the thread or says why it could not.
func (s *steering) handle(ctx context.Context, envelope socketEnvelope) {
	message, ok := readInbound(envelope)
	if !ok {
		return
	}
	if !s.first(message.ts) {
		return
	}
	// A message somewhere else in the workspace is not this sink's business even
	// where one workspace runs two, and the only somewhere else it reads at all is
	// a conversation it opened itself.
	if message.channel != s.sink.channel {
		s.decide(ctx, message)
		return
	}

	// The thread map is the correlation: a reply carries the timestamp of the
	// message its thread hangs from, and that message is the one this sink posted
	// to open a topic. A thread this sink did not open is somebody's own
	// conversation in the channel and is left alone.
	threads, err := s.sink.store.LoadThreads()
	if err != nil {
		s.sink.log("a reply arrived but the thread map could not be read, so it was not acted on: %v", err)
		return
	}
	key, found := topicOf(threads, s.sink.channel, message.threadTS)
	if !found {
		return
	}
	topic, err := notify.ParseTopic(key)
	if err != nil {
		s.sink.log("a reply arrived in a thread recorded against %q, which names no topic: %v", key, err)
		return
	}

	s.answer(ctx, threads, s.act(topic, message, time.Now()))
}

// decide reads a reply to a stopped system: an answer in the thread under a
// direct message this sink sent, which is the decision rather than a message
// about one.
//
// It is the same steering the channel's threads get, extended to the threads a
// direct message opens, and it creates no more machinery than that did: the
// answer is a directive in the record every run already consults, and what makes
// it a decision rather than a remark is that it is recorded against the option it
// named.
func (s *steering) decide(ctx context.Context, message inboundMessage) {
	// Read rather than written here, which is what keeps this goroutine off a
	// record the delivery pass owns. The copy may already be behind — the pass
	// could be telling somebody else about a state right now — and it does not
	// matter: what is looked up is a message that was written before the reply
	// answering it existed.
	directs, err := s.sink.store.LoadDirects()
	if err != nil {
		s.sink.log("an answer arrived but what was asked could not be read, so it was not acted on: %v", err)
		return
	}
	answering, found := directs.Answering(message.channel, message.threadTS)
	if !found {
		return
	}
	topic, err := notify.ParseTopic(answering.Topic)
	if err != nil {
		s.sink.log("an answer arrived to an ask recorded against %q, which names no topic: %v", answering.Topic, err)
		return
	}
	decided := s.decided(topic, answering, message, time.Now())
	if _, _, err := s.sink.tell(ctx, answering.Channel, answering.ThreadTS, decided); err != nil {
		// Whatever was recorded is recorded; what failed is the receipt for it.
		// `yoyo directive list` is where somebody who saw no answer finds out
		// whether they were heard, and this line is what points them at it.
		s.sink.log("an answer was acted on but could not be acknowledged where it was made; `yoyo directive list` says what was recorded: %v", err)
	}
}

// decided decides what one answer does, and produces the receipt that says so.
// Every branch produces one, for the reason every branch of a thread reply does:
// the person reading it was interrupted by the harness and has nothing else to
// tell them whether their answer counted.
func (s *steering) decided(topic notify.Topic, answering Direct, message inboundMessage, at time.Time) notify.Notification {
	// The identity check is the channel's, applied here explicitly rather than
	// assumed away. A direct-message conversation has exactly two members and one
	// of them is this app, so in practice the sender is the person who was asked —
	// but "in practice" is not what an authority check is, and the grant can be
	// taken away between the ask and the answer.
	if !s.operators[message.user] {
		return refused(topic, at, "the answer is from somebody this project has not granted direct-work with a bound Slack member id, so nothing was recorded; `operators` in .yoyodyne/config.yaml is where that grant lives")
	}
	if message.user != answering.Member {
		return refused(topic, at, "this ask was put to somebody else, and what is recorded is which option the person it was put to chose; `yoyo directive record` is how a decision is recorded from outside a thread")
	}
	chosen, err := parseDecision(message.text, answering.Options)
	if err != nil {
		return refused(topic, at, err.Error())
	}
	answered := directive.Decision(chosen.letter, chosen.option.Text, chosen.said)
	// An option that said it would settle a directive settles that directive,
	// rather than recording a second one beside the one that stopped the work.
	// Anything else would be the ask offering something the answer cannot do: the
	// pause would stand, the work would not pick up, and the operator would be
	// interrupted about it again an hour after they believed they had answered.
	if paused := chosen.option.Settles; paused != "" {
		// The same thing `resolve` in a channel thread does, and it is held to the
		// same rule the command line holds it to: the work resumes on the answer
		// rather than on the act of answering, so a bare letter is not one.
		if chosen.said == "" {
			return refused(topic, at, "say how it was settled after the letter — the work this held resumes on the answer rather than on the act of answering")
		}
		resolved, err := s.directives.Resolve(paused, answered, at)
		if err != nil {
			return refused(topic, at, err.Error())
		}
		return acknowledged(topic, notify.KindDirectiveResolved, resolved, at)
	}
	_, receivedBy := addressed(message.text)
	// The ask's own subject: the item where one item was stopped, and every item
	// where the whole line was. An answer about the line narrowed to some item
	// would be recorded against work the ask was never about.
	recorded, err := s.record(steer{
		kind:       directive.KindOperational,
		receivedBy: receivedBy,
		text:       answered,
	}, scopeOf(topic), at)
	if err != nil {
		return refused(topic, at, err.Error())
	}
	// The receipt is the ordinary recorded-directive one rather than a shape of its
	// own. It says which directive was written and what it says, and what it says
	// is the option that was chosen — which is the whole of what "the settled
	// receipt says what was decided" asks for, in vocabulary a reader has already
	// met in the channel.
	return acknowledged(topic, notify.KindDirectiveRecorded, recorded, at)
}

// decision is one answer as it was read: the option it named, the letter it
// named it under, and what was said beside it.
type decision struct {
	option notify.Option
	letter string
	said   string
}

// parseDecision reads which option an answer chose, and whatever the operator
// said beside it.
//
// The letter is required, and that is the whole of the grammar. What is decided
// has to say which option was chosen rather than only what somebody typed —
// otherwise the record of a decision is a paragraph somebody has to interpret
// later, which is the same problem the ask was written to end. The refusal names
// the letters, because the person reading it is in a chat client rather than
// looking at the message they are answering.
func parseDecision(raw string, options []notify.Option) (decision, error) {
	text, _ := addressed(raw)
	word, rest := firstWord(text)
	letter := strings.ToLower(strings.Trim(word, "()[].,:;-—*_"))
	option, named := notify.OptionAt(options, letter)
	if !named {
		return decision{}, fmt.Errorf("say which option you are choosing — %s — because what is recorded is the option rather than the words around it; put the letter first and add whatever you want to say after it", offered(options))
	}
	// What somebody wrote after the letter is kept, with whatever they separated it
	// from the letter with taken off: "b — because" and "b, because" are the same
	// answer, and the record already has its own separator.
	return decision{
		option: option,
		letter: letter,
		said:   strings.TrimSpace(strings.TrimLeft(rest, "-—–:;,. ")),
	}, nil
}

// offered names the letters an ask put on the table, so a refusal to read an
// answer says what would have been readable.
func offered(options []notify.Option) string {
	letters := make([]string, 0, len(options))
	for index := range options {
		if letter := notify.OptionLetter(index); letter != "" {
			letters = append(letters, "("+letter+")")
		}
	}
	switch len(letters) {
	case 0:
		return "that ask offered nothing to choose between"
	case 1:
		return letters[0]
	default:
		return strings.Join(letters[:len(letters)-1], ", ") + " or " + letters[len(letters)-1]
	}
}

// act decides what one reply does, and produces the message that says so. Every
// branch produces one: the acknowledgment is the whole of what an operator has
// to go on, and a path that produced nothing would be the channel quietly
// ignoring somebody.
func (s *steering) act(topic notify.Topic, message inboundMessage, at time.Time) notify.Notification {
	// Authority first, and before the reply is even read: what an unlisted person
	// typed is not something the harness should be parsing, let alone recording.
	if !s.operators[message.user] {
		return refused(topic, at, "the reply is from somebody this project has not granted direct-work with a bound Slack member id, so nothing was recorded; `operators` in .yoyodyne/config.yaml is where that grant lives")
	}
	// A directive from a thread is scoped to the thread's work item, so a thread
	// that is not about a work item has nothing to scope one to. Recording it
	// unscoped would reach every item in the product, which is a far larger thing
	// than anybody replying to an exchange meant.
	if topic.Kind != notify.TopicWorkItem {
		return refused(topic, at, "this thread is not a work item's, and a directive from a thread is scoped to the item the thread is about; `yoyo directive record` is how one is recorded against something else")
	}
	parsed, err := parseSteer(message.text)
	if err != nil {
		return refused(topic, at, err.Error())
	}
	if parsed.resolves != "" {
		resolved, err := s.directives.Resolve(parsed.resolves, parsed.resolution, at)
		if err != nil {
			return refused(topic, at, err.Error())
		}
		return acknowledged(topic, notify.KindDirectiveResolved, resolved, at)
	}
	// The thread's item and nothing wider. The refusal above is what makes that
	// sayable here: every topic that reaches this line is a work item's, so there
	// is always exactly one item to name.
	recorded, err := s.record(parsed, []string{topic.ID}, at)
	if err != nil {
		return refused(topic, at, err.Error())
	}
	return acknowledged(topic, notify.KindDirectiveRecorded, recorded, at)
}

// record writes one directive where every process that acts on this product's
// work reads it.
//
// What work it reaches is the caller's to say rather than this function's, and
// that is deliberate: an empty scope means every item in the product, which is
// the right answer for exactly one of the two things that reach here and a
// far larger thing than the other ever means. A reply in a work item's thread is
// about that item and names it; an answer to an ask about the whole line is about
// the whole line and names nothing. Deciding that here, from the shape of the
// topic, would be one rule serving two callers that need opposite ones — so each
// says what it means and this writes it down.
func (s *steering) record(parsed steer, scope []string, at time.Time) (directive.Directive, error) {
	id, err := directive.NewID()
	if err != nil {
		return directive.Directive{}, err
	}
	recorded := directive.Directive{
		SchemaVersion: directive.SchemaVersion,
		ID:            id,
		ProductID:     s.sink.store.Product(),
		Kind:          parsed.kind,
		ReceivedBy:    parsed.receivedBy,
		ReceivedAt:    at.UTC(),
		Text:          parsed.text,
		Artifact:      parsed.artifact,
		Unresolved:    parsed.unresolved,
		Scope:         scope,
	}
	// Every bound a directive is held to is the directive package's, checked on
	// the way into the store. A reply too long to be one is refused in the thread
	// with the record's own words rather than with a second opinion about them.
	if err := s.directives.Record(recorded); err != nil {
		return directive.Directive{}, err
	}
	return recorded, nil
}

// answer posts one acknowledgment into the thread the reply arrived in.
//
// The thread already exists by construction — the reply was correlated through
// it — so the poster is not given the capability to open one. That is what keeps
// this goroutine off the thread map the delivery pass owns: the map here is a
// copy this goroutine loaded, and writing it back over a map that has moved since
// would lose whatever thread the pass opened in between.
func (s *steering) answer(ctx context.Context, threads ThreadMap, notification notify.Notification) {
	into := &poster{sink: s.sink, threads: &threads}
	if err := notification.Notify(ctx, notify.New(into, s.sink.appearance)); err != nil {
		// The record is already written at this point wherever one was written, so
		// what failed is the account of it rather than the act. An operator who saw
		// no answer has `yoyo directive list`, and this line is what points them at
		// it.
		s.sink.log("a reply was acted on but could not be acknowledged in its thread; `yoyo directive list` says what was recorded: %v", err)
	}
}

// first reports a reply this process has not acted on yet, and remembers it. A
// redelivered envelope is Slack repeating itself rather than an operator saying
// something twice, and recording it twice would leave two directives to resolve
// for one instruction.
func (s *steering) first(ts string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.acted[ts] {
		return false
	}
	s.acted[ts] = true
	s.ordered = append(s.ordered, ts)
	if len(s.ordered) > maxRememberedReplies {
		delete(s.acted, s.ordered[0])
		s.ordered = s.ordered[1:]
	}
	return true
}

// inboundMessage is one Slack message event reduced to what this reads: who said
// it, what they said, and which thread of which conversation they said it in.
type inboundMessage struct {
	user     string
	text     string
	ts       string
	threadTS string
	// channel is which conversation it arrived in, which is what decides whether
	// it is a reply about a work item or an answer to something the harness asked
	// somebody directly.
	channel string
}

// readInbound reads a message a person typed in one of this sink's threads, and
// reports whether the envelope was one at all.
//
// What it refuses is as important as what it accepts. A message with a subtype
// or a bot id is not a person typing — an edit, a join, a file share, and above
// all this sink's own posts, which arrive back on the same connection and would
// otherwise be read as instructions the harness gave itself. A message with no
// thread is somebody talking rather than answering something: in the channel it
// addresses no work item, and in a direct message it answers no ask.
//
// Which conversation it arrived in is carried out rather than checked here, and
// the caller decides. A reply is only ever acted on inside a thread this sink
// opened, and that is settled by looking the thread up rather than by the name of
// the conversation it is in.
func readInbound(envelope socketEnvelope) (inboundMessage, bool) {
	if envelope.Type != socketEventsAPI || len(envelope.Payload) == 0 {
		return inboundMessage{}, false
	}
	var payload struct {
		Event struct {
			Type     string `json:"type"`
			Subtype  string `json:"subtype"`
			User     string `json:"user"`
			BotID    string `json:"bot_id"`
			Text     string `json:"text"`
			TS       string `json:"ts"`
			ThreadTS string `json:"thread_ts"`
			Channel  string `json:"channel"`
		} `json:"event"`
	}
	if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
		return inboundMessage{}, false
	}
	event := payload.Event
	if event.Type != "message" || event.Subtype != "" || strings.TrimSpace(event.BotID) != "" {
		return inboundMessage{}, false
	}
	if strings.TrimSpace(event.Channel) == "" || strings.TrimSpace(event.User) == "" {
		return inboundMessage{}, false
	}
	// A thread's own opening message carries its own timestamp as the thread's,
	// and this sink wrote every one of those.
	if strings.TrimSpace(event.ThreadTS) == "" || event.ThreadTS == event.TS {
		return inboundMessage{}, false
	}
	return inboundMessage{
		user:     strings.TrimSpace(event.User),
		text:     event.Text,
		ts:       event.TS,
		threadTS: event.ThreadTS,
		channel:  strings.TrimSpace(event.Channel),
	}, true
}

// topicOf finds the topic a thread belongs to. The map is kept the other way
// round — a topic is looked up to post into its thread — and it is walked here
// rather than indexed because it is one entry per topic this product has ever
// reported on, read once per reply somebody types.
func topicOf(threads ThreadMap, channel, threadTS string) (string, bool) {
	for key, thread := range threads.Threads {
		if thread.Channel == channel && thread.ThreadTS == threadTS {
			return key, true
		}
	}
	return "", false
}

// steer is what one reply asks for, once it has been read.
type steer struct {
	// resolves and resolution are set for exactly the resolve verb: which
	// directive to settle, and how it was settled.
	resolves   string
	resolution string
	// The rest describe a directive to record. The kind is operational unless the
	// reply stated one of the two that pause work.
	kind       directive.Kind
	receivedBy domain.AgentRole
	artifact   string
	unresolved string
	text       string
}

// parseSteer reads a reply. The grammar is small on purpose: everything it does
// not recognize is an operational directive said in the operator's own words,
// which is the reading that cannot silently stop work.
//
// A refusal names what to type instead, because the person reading it is in a
// chat client rather than at a terminal with the usage text in front of them.
func parseSteer(raw string) (steer, error) {
	said, receivedBy := addressed(raw)
	if said == "" {
		return steer{}, errors.New("the reply said nothing that could be recorded")
	}
	parsed := steer{kind: directive.KindOperational, receivedBy: receivedBy, text: said}
	lowered := strings.ToLower(said)
	switch {
	case lowered == resolveVerb || strings.HasPrefix(lowered, resolveVerb+" "):
		reference, rest := firstWord(said[len(resolveVerb):])
		if reference == "" {
			return steer{}, errors.New("`resolve <directive> <how it was settled>` — name the directive to settle; any prefix of its id that names exactly one will do")
		}
		if rest == "" {
			return steer{}, errors.New("`resolve <directive> <how it was settled>` — say how it was settled; the work resumes on the answer rather than on the act of answering")
		}
		return steer{resolves: reference, resolution: rest}, nil
	case strings.HasPrefix(lowered, ambiguousOpening):
		// What was said is recorded whole, and the part after the opening is what
		// has to be settled. They are the same words here because a reply that says
		// it is ambiguous is saying what nobody can act on.
		parsed.kind = directive.KindAmbiguous
		parsed.unresolved = strings.TrimSpace(said[len(ambiguousOpening):])
		if parsed.unresolved == "" {
			return steer{}, errors.New("`ambiguous: <what nobody can act on without deciding>` — say what is unresolved; a pause nobody can name a reason for is a pause nobody can lift")
		}
	case strings.HasPrefix(lowered, artifactOpening):
		parsed.kind = directive.KindArtifact
		parsed.artifact, parsed.unresolved = firstWord(said[len(artifactOpening):])
		if parsed.artifact == "" {
			return steer{}, errors.New("`artifact: <name> <what has to be decided about it>` — name the governed document this changes")
		}
		if parsed.unresolved == "" {
			return steer{}, errors.New("`artifact: <name> <what has to be decided about it>` — say what has to be decided; work derived from that document waits until somebody does")
		}
	}
	return parsed, nil
}

// addressed reads who a reply is for and returns what is left of it.
//
// The persona is named in the plain `@role` a reader would type, because the
// personas are display names on one app's messages rather than members of the
// workspace: there is nothing for Slack's own mention to point at. A reply that
// names no role is the product manager's, which is where a directive that named
// no receiver already goes.
//
// Who received a directive is attribution and never routing — the record reaches
// every role whichever one is named — so a mention that is not a role is left in
// the words rather than refused. Somebody who wrote a name the harness does not
// know still said something, and dropping their sentence over its first word
// would be the channel deciding it knew better.
func addressed(raw string) (string, domain.AgentRole) {
	said := strings.TrimSpace(raw)
	// A Slack mention of the app itself arrives as <@Uxxxx>. It addresses the bot,
	// which is not a persona and not a role anything records, so it is dropped
	// rather than read as an addressee.
	if strings.HasPrefix(said, "<@") {
		if _, after, found := strings.Cut(said, ">"); found {
			said = strings.TrimSpace(after)
		}
	}
	word, rest := firstWord(said)
	if named, mentioned := strings.CutPrefix(word, "@"); mentioned {
		if role := domain.AgentRole(strings.ToLower(strings.Trim(named, ":,"))); role.Valid() {
			return rest, role
		}
	}
	return said, domain.RoleProductManager
}

// firstWord splits the first word off a value and returns what follows it, both
// trimmed. It is how the two openings that take a name read theirs: a directive
// id and an artifact name are single words, and everything after is prose.
func firstWord(value string) (string, string) {
	trimmed := strings.TrimSpace(value)
	index := strings.IndexFunc(trimmed, unicode.IsSpace)
	if index < 0 {
		return trimmed, ""
	}
	return trimmed[:index], strings.TrimSpace(trimmed[index:])
}

// acknowledged is the message that says what a reply did: the directive as
// recorded or as settled, with its identifier, in the thread it was said in.
//
// A directive that pauses work is a warning rather than a note, so it is shown in
// the channel as well as in the thread. Work stopping is the news somebody who
// has opened no threads most needs, and it is stopped by their own instruction,
// which is exactly when the confirmation matters.
//
// The operator's own words go back into the channel they came from. Nothing is
// redacted on this path and nothing needs to be: the text is already in the
// thread, said by the person it belongs to, and this is the record quoting it
// back with an identifier attached.
func acknowledged(topic notify.Topic, kind notify.Kind, recorded directive.Directive, at time.Time) notify.Notification {
	severity := report.SeverityNote
	text := recorded.Text
	if kind == notify.KindDirectiveResolved {
		text = recorded.Resolution
	} else if recorded.Pauses() {
		severity = report.SeverityWarning
	}
	return notify.Notification{
		Topic:   topic,
		Speaker: notify.Harness(),
		Event: notify.Event{
			Kind:     kind,
			At:       at.UTC(),
			Severity: severity,
			Refs:     notify.Refs{WorkItemID: workItemOf(topic), DirectiveID: recorded.ID},
			Detail: notify.Detail{
				ReceivedBy: recorded.ReceivedBy.Title(),
				Artifact:   recorded.Artifact,
				// What the directive left unsettled is what says whether the work is
				// paused, and a resolved one has left nothing.
				Unresolved: unsettled(recorded),
			},
			Text: text,
		},
	}
}

// refused is the message that says a reply recorded nothing, and why. It is said
// in the thread rather than only in the sink's log for the reason the whole
// inbound half is acknowledged: a person who steers from a phone has nothing else
// to tell them whether they were heard, and silence reads as either.
func refused(topic notify.Topic, at time.Time, why string) notify.Notification {
	return notify.Notification{
		Topic:   topic,
		Speaker: notify.Harness(),
		Event: notify.Event{
			Kind:     notify.KindDirectiveRefused,
			At:       at.UTC(),
			Severity: report.SeverityNote,
			Refs:     notify.Refs{WorkItemID: workItemOf(topic)},
			Detail:   notify.Detail{Reason: why},
		},
	}
}

// unsettled is what a directive is still waiting on, which is nothing at all once
// somebody has settled it.
func unsettled(recorded directive.Directive) string {
	if recorded.Resolved() {
		return ""
	}
	return recorded.Unresolved
}

func workItemOf(topic notify.Topic) string {
	if topic.Kind == notify.TopicWorkItem {
		return topic.ID
	}
	return ""
}

// scopeOf is the work an answer to a direct ask reaches: the item the ask was
// about, and every item where the ask was about the whole line. It is only ever
// asked of a topic a direct message was addressed to — a reply in the channel
// names its own item, because a thread that is not a work item's is refused
// before anything is recorded from it.
func scopeOf(topic notify.Topic) []string {
	if item := workItemOf(topic); item != "" {
		return []string{item}
	}
	return nil
}
