package slack

// The door: a message that addresses this app is always answered.
//
// The sink answers inside the threads it opened and is silent everywhere else,
// which is right for a reporting channel and wrong for exactly one case. An
// operator who @-mentions the app at the top of the channel is not talking to
// the channel — they are talking to the harness, and the top level had no
// handler at all, so three of those in a row on 2026-08-31 were not answered,
// not refused, and not even written down. Silence there is indistinguishable
// from a sink that has died, which is the one reading a reporting process must
// never leave somebody with.
//
// So this is the second stated exception to answering only inside this sink's
// own threads, and it is drawn as narrowly as the first: a message that names
// this app's member id is answered where it was said, and a message that does
// not is left exactly as alone as it always was. Nothing here records a
// directive, changes anything, or reads any record the operator could not
// already read in this channel — the four lines below are the same four lines
// the heartbeat posts to the same channel.
//
// What it does keep is what was said. A message addressed to this app goes into
// the sink's own log with the operator's own words in it, before the answer is
// posted and whether or not the workspace takes the answer, because "not even
// written down" is half of what the silence cost: an operator who was answered
// badly can at least be shown what they asked, and one who was not answered at
// all cannot be shown anything. It is a note of having been heard rather than a
// directive — nothing reads it, and no work moves because of it.
//
// Two answers, and no more than two. A question about where things stand gets
// the read model's own four lines, because that derivation is the read model's
// and a surface that assembled its own would be a second answer to the one
// question an operator asks most. Everything else gets one sentence saying what
// this app cannot do yet and where to do it instead — which is a worse answer
// than the operator wanted and an immeasurably better one than nothing.

import (
	"context"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/mason-bryant/yoyodyne/internal/notify"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
)

// The two answers this door has.
//
// The lead line goes in front of the four lines rather than in place of any of
// them: what somebody asked for is the standing, and a message that opened
// straight into "Running:" reads as a fragment of something they missed the
// start of.
const (
	standingLead = "Where the harness stands right now:"
	// unanswerable is said when the standing cannot be read at all. It is a
	// stated absence rather than an empty block, for the reason every line of the
	// four is: a confident nothing assembled from a source nobody could read is
	// the worst answer this could give.
	unanswerable = "Where the harness stands cannot be read from here — this sink was started without the read model, so `yoyo status` at the terminal is the answer."
	// unhandled is what everything that is not a question about the standing gets.
	// One sentence, saying the thing this app cannot do and the two places the
	// work is actually driven from, because somebody who has just been told no is
	// owed the next thing to try rather than an apology.
	unhandled = "Asking me where things stand is the only thing I can answer here yet — to steer the work, reply inside a work item's thread in this channel, and everything else is driven by `yoyo` at the terminal."
)

// maxAskedBytes bounds how much of somebody's own words one log line carries. It
// is generous enough to hold a question whole and short enough that a log stays
// a log; what was said in full is in the channel, where they said it, and the
// line points at the person and the moment rather than standing in for either.
const maxAskedBytes = 300

// standingQuestions is what a question about the standing is asked in. It is a
// stated list for the reason the pausing kinds of a directive are stated: the
// alternative is something guessing at intent, and the two answers here are
// different enough that guessing wrong is answering a question nobody asked.
//
// It is generous rather than exact, because the cost of the two mistakes is not
// symmetric. A question this list misses still gets an answer — the sentence
// saying what to ask for — and a sentence it matches by accident gets the
// standing, which is true, harmless, and already in this channel.
var standingQuestions = []string{
	"status",
	"standing",
	"sitrep",
	"what is running",
	"whats running",
	"what are you running",
	"anything running",
	"what is going on",
	"whats going on",
	"what is happening",
	"whats happening",
	"what are you doing",
	"what are you working on",
	"where are we",
	"where do things stand",
	"where does it stand",
	"how is it going",
	"hows it going",
}

// mentioned answers one message that reached this sink outside its own threads.
//
// It is silent unless the message names this app, and that order matters: the
// channel carries other people's conversations, and reading every one of them
// as something addressed to the harness is how a reporting channel becomes a
// participant in a discussion nobody invited it to.
//
// Nothing here is a gate and nothing here reports a failure upwards. An answer
// that could not be posted is said in the sink's own log, because the operator
// is owed a record of having been heard even where the workspace refused to
// carry it.
func (s *steering) mentioned(ctx context.Context, message inboundMessage) {
	// The cheap refusal first: a message with no mention in it at all cannot be
	// addressed to this app, and settling that here means an ordinary channel
	// never causes the workspace to be asked anything.
	if !strings.Contains(message.text, "<@") {
		return
	}
	member, known := s.identity(ctx)
	if !known || !addresses(message.text, member) {
		return
	}
	if !s.first(message.ts) {
		return
	}

	said := withoutMentions(message.text)
	answer, subject := unhandled, "what it cannot do yet"
	if asksForStanding(said) {
		answer, subject = s.sink.standingAnswer(ctx), "where the harness stands"
	}
	// Written down before the answer is posted rather than after it, and carrying
	// the words rather than only the fact that somebody typed something. A message
	// the workspace then refuses to carry an answer for is still one this operator
	// sent, and the line below is the whole of what says so.
	s.sink.log("this app was addressed by %s outside its own threads, saying %q, and is being answered with %s",
		message.user, singleLine(message.text, maxAskedBytes), subject)
	if err := s.sink.answerMention(ctx, message, answer); err != nil {
		if ctx.Err() != nil {
			return
		}
		s.sink.log("this app was addressed by %s and the answer could not be posted, so it reads as silence there: %v", message.user, err)
	}
}

// identity is this app's own Slack member id: what a message has to name to be
// addressed to it.
//
// It is asked of the workspace once and then remembered, and it is asked lazily
// rather than required at startup. A sink whose first `auth.test` was refused
// still posts — reporting is never a gate — and answering a mention is exactly
// the thing that must not be lost to a call that failed minutes before anybody
// typed anything.
//
// The memory is under the connection's own mutex, beside what it has already
// acted on, because both are the connection's and neither is the delivery
// pass's.
func (s *steering) identity(ctx context.Context) (string, bool) {
	s.mu.Lock()
	member := s.member
	s.mu.Unlock()
	if member != "" {
		return member, true
	}
	identity, err := s.sink.api.Identify(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return "", false
		}
		s.sink.log("a message naming somebody arrived, but the workspace would not say who this app is, so whether it was addressed cannot be told: %v", err)
		return "", false
	}
	member = strings.TrimSpace(identity.UserID)
	if member == "" {
		s.sink.log("the workspace named this app without a member id, so a message addressed to it cannot be recognized")
		return "", false
	}
	s.learn(member)
	return member, true
}

// learn remembers this app's own member id, from wherever the workspace was
// asked. The sink asks once at startup and hands the answer here, so the
// ordinary running sink never spends a second call on it.
func (s *steering) learn(member string) {
	trimmed := strings.TrimSpace(member)
	if trimmed == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.member = trimmed
}

// standingAnswer is the four lines, as an answer to somebody who asked for them.
//
// They are the read model's rendering and not this surface's: the same
// derivation `yoyo status` prints and the heartbeat posts, so a channel and a
// terminal cannot come to answer one question two ways. What is added is the
// line saying what they are.
func (s *Sink) standingAnswer(ctx context.Context) string {
	if s.sources == nil {
		return unanswerable
	}
	rendered := strings.TrimRight(readmodel.ReadStanding(ctx, *s.sources).Render(), "\n")
	if rendered == "" {
		return unanswerable
	}
	return standingLead + "\n\n" + rendered
}

// answerMention puts one answer where the question was asked, tagging whoever
// asked it.
//
// It hangs from the message that asked rather than adding a line to the top of
// the channel, so a question and its answer read as one exchange and the top
// level stays the status board it is. Where the question was already inside a
// thread — one this sink never opened — the answer stays in that thread, which
// is the same rule said the same way.
//
// The thread map is not touched. These are not a topic's threads and never
// become one: the map is the delivery pass's, this runs on the connection, and
// a second writer to it is the one way this path could damage anything.
func (s *Sink) answerMention(ctx context.Context, message inboundMessage, answer string) error {
	identity := s.appearance.Identity(notify.Harness())
	emoji, url := icon(identity.Avatar)
	// A message that is already in a thread is answered there; one at the top of
	// the channel opens a thread of its own under itself.
	thread := message.threadTS
	if thread == "" {
		thread = message.ts
	}
	_, err := s.post(ctx, Message{
		Channel:   s.channel,
		Text:      boundAnswer(tagged(message.user, answer)),
		ThreadTS:  thread,
		Username:  identity.Name,
		IconEmoji: emoji,
		IconURL:   url,
	})
	return err
}

// boundAnswer keeps one answer inside what a message may carry, and says where
// the whole of it is.
//
// It names the terminal rather than a durable record, which is what every other
// truncation here names, because this answer is not a rendering of a record
// somebody can go and open: it is the standing, and `yoyo status` is where the
// unbounded version of it is printed. The read model already bounds each of the
// four lines, so what this catches is a standing far past anything a channel
// could carry — which Slack would otherwise refuse forever rather than once.
func boundAnswer(answer string) string {
	if len(answer) <= maxTextBytes {
		return answer
	}
	marker := "\n… truncated; `yoyo status` at the terminal prints the whole of it"
	cut := maxTextBytes - len(marker)
	for cut > 0 && !utf8.RuneStart(answer[cut]) {
		cut--
	}
	return answer[:cut] + marker
}

// addresses reports a message naming this app.
//
// Slack writes a mention as `<@U0123>`, and in an older labelled form as
// `<@U0123|name>`. Both are the same member id and both address this app, so
// both are matched: the labelled form is uncommon in the message events this
// reads, and a workspace that emits it would otherwise get exactly the silence
// this whole door exists to remove — the one failure that is invisible from
// here and indistinguishable from a dead sink from there.
//
// The terminator is the whole of the test. A member id is a prefix of longer
// ids, so `<@U0YOY` matching inside `<@U0YOYODYNE>` would answer messages
// addressed to somebody else.
func addresses(text, member string) bool {
	rest := text
	for {
		_, after, found := strings.Cut(rest, "<@"+member)
		if !found {
			return false
		}
		if strings.HasPrefix(after, ">") || strings.HasPrefix(after, "|") {
			return true
		}
		rest = after
	}
}

// asksForStanding reports a message asking where things stand.
func asksForStanding(said string) bool {
	folded := fold(said)
	for _, question := range standingQuestions {
		if strings.Contains(folded, question) {
			return true
		}
	}
	return false
}

// fold reduces what somebody typed to the words in it: lower case, apostrophes
// dropped so "what's" and "whats" are one thing, and everything else that is
// not a letter or a digit a single space. It is what lets the stated list above
// be written once per phrasing rather than once per punctuation.
func fold(said string) string {
	var folded strings.Builder
	spaced := true
	for _, character := range strings.ToLower(said) {
		switch {
		case character == '\'' || character == '’':
			continue
		case unicode.IsLetter(character) || unicode.IsDigit(character):
			folded.WriteRune(character)
			spaced = false
		case !spaced:
			folded.WriteByte(' ')
			spaced = true
		}
	}
	return strings.TrimSpace(folded.String())
}

// withoutMentions is what somebody said with Slack's own mention syntax taken
// out of it, so what is read is their words rather than the member ids the
// client wrapped around the names they typed.
func withoutMentions(text string) string {
	var said strings.Builder
	rest := text
	for {
		before, after, found := strings.Cut(rest, "<@")
		said.WriteString(before)
		if !found {
			return strings.TrimSpace(said.String())
		}
		_, remainder, closed := strings.Cut(after, ">")
		if !closed {
			// An unclosed mention is not something Slack writes. What is left of the
			// message is whatever came before it, which is read rather than refused:
			// somebody still said something.
			return strings.TrimSpace(said.String())
		}
		said.WriteString(" ")
		rest = remainder
	}
}
