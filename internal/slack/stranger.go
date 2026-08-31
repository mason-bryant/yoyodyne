package slack

// The same door, for somebody the project does not recognize.
//
// Two exceptions to answering only inside this sink's own threads are already
// stated — a reply in one of them, and a message that @-mentions the app. This
// is the third and the last, and it is the only one that is about who is
// speaking rather than where. Somebody whose Slack member id is bound to nobody
// in the `operators` mapping is a stranger to this project: they cannot steer
// the work, and until now what they got for trying was either a refusal written
// for an operator who mistyped their own member id, or the four lines. Neither
// tells a person the true thing, which is that this app does not know them and
// there is a human who does.
//
// So they are told, once, in words the operator wrote: this app does not know
// them, and who to reach out to instead. The contacts are the humans the
// `operators` mapping names, because that mapping is the whole of who this
// project recognizes — a list of people to contact maintained anywhere else
// would be a second answer to the same question, and the one that goes stale.
//
// Once per thread, and that bound is the rule rather than an optimization. A
// refusal that repeated would let anybody at all make this app talk as much as
// they liked in a channel it is supposed to report in, and an app that can be
// made chatty by strangers is one an operator turns off. Every attempt after the
// first is written down in the sink's own log and answered with nothing, which
// is the same shape every other bound here takes: what was said is kept, and
// what is said back is bounded.
//
// It never names a stranger to the workspace by mention. The contacts are named
// in the words the mapping files them under rather than as `<@id>`, so telling
// somebody who to ask does not notify those people every time an unknown user
// opens a thread — which would hand exactly the sender this exists to bound a
// way of reaching the operators anyway.

import (
	"context"
	"fmt"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/notify"
)

const (
	// The operator's own sentence, in two parts because only the second has
	// anything filled into it.
	strangerLead = "I don't know you."
	strangerAsk  = "Please reach out to %s if you need something."
	// unnamedContacts is who to ask where the mapping named humans without
	// naming any of them in a way this can print. It is a description rather than
	// a name for the reason every other stated absence here is one: a sentence
	// that trails off is worse than one that says what it does not know.
	unnamedContacts = "whoever runs this harness"
)

// knows reports somebody this project recognizes: a Slack member id bound to a
// human in the `operators` mapping, whatever that human was granted.
//
// A project that recognizes nobody recognizes everybody here, which is the one
// case worth stating. An empty mapping has drawn no boundary — there is nobody
// to be a stranger to and nobody to be told to contact — so a workspace that has
// named nobody behaves exactly as it did before this existed: every message
// addressed to the app is answered, and a reply that would steer is refused for
// the grant it is missing rather than for who sent it.
func (s *steering) knows(member string) bool {
	if len(s.recognized) == 0 {
		return true
	}
	return s.recognized[member]
}

// refuse answers one message from a stranger, at most once in the thread it
// arrived in.
//
// It posts and then remembers, which is the ordering the whole sink is built on:
// a process killed between the two says it a second time, and the other order
// would leave somebody with silence and this app believing it had answered them.
// A refusal the workspace would not carry is not remembered at all, so the next
// attempt is still answered.
func (s *steering) refuse(ctx context.Context, message inboundMessage) {
	thread := message.threadTS
	if thread == "" {
		// A message at the top of the channel is answered in a thread hanging from
		// itself, which is the thread this refusal is remembered against.
		thread = message.ts
	}
	refusals, err := s.sink.store.LoadRefusals()
	if err != nil {
		// Nothing is said here, and that is the deliberate choice. What cannot be
		// read cannot be written either, so answering would be this app talking
		// without the ability to remember having talked — which is the one thing the
		// rule above forbids. The line below is what says so.
		s.sink.log("a message from %s, who this project does not recognize, arrived but what has already been said to strangers could not be read, so it was not answered: %v", message.user, err)
		return
	}
	if refusals.Has(s.sink.channel, thread) {
		s.sink.log("%s said something more in a thread this app has already told them it does not know them in, saying %q, and it was recorded rather than answered again",
			message.user, singleLine(message.text, maxAskedBytes))
		return
	}
	s.sink.log("this app was addressed by %s, who is bound to nobody in this project's operators mapping, saying %q, and is being told so once in this thread",
		message.user, singleLine(message.text, maxAskedBytes))
	if err := s.sink.answerMention(ctx, message, refusalText(s.contacts)); err != nil {
		if ctx.Err() != nil {
			return
		}
		s.sink.log("this app was addressed by %s and the refusal could not be posted, so it reads as silence there: %v", message.user, err)
		return
	}
	// The mark is the same receipt an operator's refused reply wears, and it is
	// put on for the same reason: a message wearing nothing is one nobody read,
	// and this one was. It is put on after the words rather than instead of them,
	// and a workspace that will not take it costs the mark and nothing else.
	s.mark(ctx, message.ts, notify.ReceiptRefused)
	refusals.Record(s.sink.channel, thread, Refusal{Member: message.user, At: s.sink.clock().UTC()})
	if err := s.sink.store.SaveRefusals(refusals); err != nil {
		s.sink.log("%s was told this app does not know them, but that it was said could not be remembered, so it may be said in that thread again: %v", message.user, err)
	}
}

// refusalText is the sentence, with who to contact filled in.
func refusalText(contacts []string) string {
	return strangerLead + " " + fmt.Sprintf(strangerAsk, contactList(contacts))
}

// contactList names the humans a stranger is told to reach out to, the way
// somebody would say them out loud. They are joined with "or" rather than "and"
// because reaching one of them is what was asked for, not all of them.
func contactList(contacts []string) string {
	named := make([]string, 0, len(contacts))
	for _, contact := range contacts {
		if trimmed := strings.TrimSpace(contact); trimmed != "" {
			named = append(named, trimmed)
		}
	}
	switch len(named) {
	case 0:
		return unnamedContacts
	case 1:
		return named[0]
	case 2:
		return named[0] + " or " + named[1]
	default:
		return strings.Join(named[:len(named)-1], ", ") + ", or " + named[len(named)-1]
	}
}
