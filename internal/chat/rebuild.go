package chat

// Continuing a conversation on a provider that has never held it.
//
// Every turn but the first resumes a provider session, and that is the whole of
// why a later turn's prompt carries so little: the session already holds the
// briefing, the operator's earlier messages, and everything the role has said
// back. It is an accelerator the harness does not own — the session belongs to
// the provider, expires on its clock, and means nothing to any other provider —
// so the moment a turn is served somewhere else there is nothing to resume and
// the context has to come from the record the harness does own.
//
// So a crossing rebuilds. The prompt the second provider is handed is assembled
// from the conversation's durable state — the picture it is working from, and
// the account of what has been said that its event log holds — with the turn's
// own prompt after it and no session identifier anywhere. That is the
// durable-state guarantee doing the work it exists for: a conversation whose
// provider becomes unavailable carries on, and what carries it is the record
// rather than somebody else's session.
//
// What the rebuild can say is bounded by what the record holds, and it says so
// rather than implying otherwise. The event log carries what the role said,
// which is the half of the conversation the provider produced; the operator's own
// messages are not in it, so the reconstruction is framed as an account of the
// conversation rather than presented as a transcript of it. A role that is told
// which it is reading can say it is missing something; one that is handed a gap
// dressed as a transcript will fill it in.

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/modelfailover"
)

// maxRebuiltContextBytes bounds what a rebuild puts in front of the turn's own
// prompt. It is a fraction of MaxTurnInputBytes rather than the whole of it,
// because the turn still has to fit beside it: a rebuild that filled the budget
// would produce a turn refused for its own size, which is the substitution
// failing in a way that reads as the operator's message being too long.
const maxRebuiltContextBytes = 256 << 10

// maxRebuiltMessages bounds how many of the role's recorded messages the rebuild
// carries. The most recent are the ones a continuation needs, and a conversation
// somebody has held for days is one whose earliest turns are further from what is
// being said now than the budget above is worth spending on.
const maxRebuiltMessages = 40

// servingEndpoint is where a turn was actually served, as the record should say
// it. The failover answers it where it resolved an endpoint, and this
// conversation's own configuration answers it where nothing did — a policy with
// no endpoint to name is one nobody resolved, and the record is better off saying
// what was configured than saying nothing.
func (s *Session) servingEndpoint(served modelfailover.Served) backend.Endpoint {
	if served.Endpoint.Provider != "" {
		return served.Endpoint
	}
	return backend.Endpoint{
		Provider:     s.options.Provider,
		AccountAlias: s.options.AccountAlias,
		Model:        served.Model,
	}
}

// resumableSession is the provider session this turn may continue from, and
// empty where there is none to continue.
//
// A session belongs to the provider that issued it and means nothing to any
// other, so a conversation whose last turn crossed providers holds a session
// identifier the provider about to be asked has never seen. Sending it would be
// asking one provider to resume another's conversation; what happens instead is
// that this turn starts a session of its own, and the context the old one held is
// rebuilt from the record — which is the same answer the crossing itself made,
// applied to the crossing back.
//
// The comparison is against the recorded backend rather than against anything
// this process remembers, because the process that took the crossing turn is
// rarely the process that takes the next one.
func (s *Session) resumableSession() string {
	if s.state.Backend != "" && s.state.Backend != s.options.Provider {
		return ""
	}
	return s.state.ProviderSessionID
}

// alternateSession is the session the endpoint this conversation fails over to
// already holds for it, and empty where it holds none.
//
// A window lasts longer than one turn, so the turn after a crossing goes to the
// same alternate — and by then that provider has a session of its own, recorded
// exactly as the configured provider's is. Resuming it is what keeps an outage
// costing one reconstruction rather than one per turn, and it is the same
// question resumableSession asks, asked about the other endpoint.
func (s *Session) alternateSession() string {
	alternate := s.options.FailoverEndpoint.Provider
	if alternate == "" || s.state.Backend != alternate {
		return ""
	}
	return s.state.ProviderSessionID
}

// rebuiltContextHeader opens the reconstruction, and is how a request that has
// already been rebuilt is recognized as one. A turn can reach the rebuild twice —
// prepared here for the endpoint it was going to and then moved onto the other
// one by a refusal nobody could have known about in advance — and two
// reconstructions in one prompt is the conversation told to itself twice.
const rebuiltContextHeader = "# This conversation, rebuilt from its record"

// rebuildForOwnEndpoint prepares a turn going to the provider this conversation
// is configured for, where that provider holds no session to resume — which is
// the turn after a crossing, once the window it was waiting out has lifted. The
// alternate's session is no answer there: it belongs to the provider not being
// asked.
func (s *Session) rebuildForOwnEndpoint(request backend.RunRequest) (backend.RunRequest, error) {
	return s.rebuildFromRecord(request)
}

// rebuildForAlternate prepares a turn the failover is moving onto the alternate,
// and is what the policy calls.
//
// It adds nothing where the alternate is already holding a session for this
// conversation, which is every turn of an outage after the first: that session
// carries the context, so reconstructing it would be telling the provider what it
// already knows, once per turn, for as long as the window stands.
func (s *Session) rebuildForAlternate(request backend.RunRequest) (backend.RunRequest, error) {
	if s.alternateSession() != "" {
		return request, nil
	}
	return s.rebuildFromRecord(request)
}

// rebuildFromRecord assembles the request a provider that holds no session for
// this conversation is asked, from the conversation's own durable state.
//
// The request it is given is the one the turn was built with, so the system
// prompt, the role, the tools, and the turn's own prompt are already right — none
// of those came from a provider session. What is added in front of the prompt is
// what a session would have been carrying: the picture, and what has been said.
//
// A request that already carries the reconstruction is returned as it is. A turn
// can reach this twice — prepared for the endpoint it was nominally on, then
// moved onto the other one by a refusal nobody could have known about in advance
// — and the conversation told to itself twice is worse than either endpoint
// getting it once.
func (s *Session) rebuildFromRecord(request backend.RunRequest) (backend.RunRequest, error) {
	if strings.HasPrefix(request.Prompt, rebuiltContextHeader) {
		return request, nil
	}
	// A failure hands the request back as it came rather than as a zero value. The
	// caller discards it either way, and nothing here is a provider invocation —
	// this assembles what one will be asked, and the invocation itself is made by
	// whoever called for the rebuild.
	events, err := s.options.Store.LoadEvents(s.state.ConversationID)
	if err != nil {
		return request, fmt.Errorf("read what this conversation has recorded: %w", err)
	}
	// The turn's own prompt was redacted before it reached here, and this is
	// assembled afterwards out of the briefing and the event log, so it is redacted
	// here rather than inheriting a pass it was not part of. Anything recognizably
	// sensitive leaves the harness once per invocation, and a crossing is one more
	// invocation to a provider that has never seen any of it.
	rebuilt := execution.NewRedactor(s.options.RedactValues...).Redact(s.rebuiltContext(events))
	if rebuilt == "" {
		// A conversation with nothing recorded is one whose first turn is being
		// taken, and its prompt already carries the briefing. There is nothing to
		// rebuild and nothing missing, so the request stands as it is.
		return request, nil
	}
	if len(rebuilt)+len(request.SystemPrompt)+len(request.Prompt) > MaxTurnInputBytes {
		return request, fmt.Errorf(
			"the rebuilt context and this turn are %d bytes together, limit is %d",
			len(rebuilt)+len(request.SystemPrompt)+len(request.Prompt), MaxTurnInputBytes)
	}
	request.Prompt = rebuilt + request.Prompt
	return request, nil
}

// rebuiltContext is what the second provider is told before the turn itself:
// which conversation this is and why it is being handed this at all, the picture
// the conversation is working from, and the account of what has been said.
func (s *Session) rebuiltContext(events []execution.Event) string {
	said := recordedMessages(events)
	briefing := s.workingBriefing()
	if said == "" && briefing == "" {
		return ""
	}
	var rebuilt strings.Builder
	rebuilt.WriteString(rebuiltContextHeader + "\n\n")
	rebuilt.WriteString(fmt.Sprintf(
		"You are continuing conversation %s, which has taken %d turn(s) so far. The provider that was holding it is not the one serving this turn, so none of its session reaches you. What follows is assembled from the harness's own durable record of the conversation, and it is the whole of what you have.\n\n",
		s.state.ConversationID, s.state.Turns))
	if briefing != "" {
		rebuilt.WriteString(briefing)
		rebuilt.WriteString("\n")
	}
	if said != "" {
		rebuilt.WriteString("## What you have said so far\n\n")
		rebuilt.WriteString("These are your own replies, in order, as the harness recorded them. The operator's messages are not recorded, so what they asked has to be read from what you answered. Where that leaves you unsure what was asked, say so rather than assuming.\n\n")
		rebuilt.WriteString(said)
		rebuilt.WriteString("\n")
	}
	return rebuilt.String()
}

// workingBriefing is the picture this conversation is working from: the one a
// refresh replaced it with where the operator asked for one, and the one it was
// opened on otherwise. A later turn does not carry it, because the session it
// resumes already held it — which is exactly why a rebuild has to.
//
// It is nothing on a turn whose own prompt already carries the picture: the first
// turn, and any turn the operator asked for a refresh on. Repeating it there
// would hand the provider the same document twice, which spends context to say
// the same thing and reads as two pictures to reconcile.
func (s *Session) workingBriefing() string {
	if s.refresh != nil || s.state.Turns == 0 {
		return ""
	}
	if s.carried != nil {
		return strings.TrimSpace(s.carried.Text)
	}
	return strings.TrimSpace(s.options.Briefing.Text)
}

// recordedMessages is what the role has said, read back out of the conversation's
// event log in the order it was recorded and bounded twice: to the most recent
// messages, and to the byte budget a rebuild may spend. An account that dropped
// something says so, because a role told it has everything and given part of it
// will reason as though the missing part never happened.
func recordedMessages(events []execution.Event) string {
	spoken := make([]string, 0, len(events))
	for _, event := range events {
		if event.Type != execution.EventAgentMessage {
			continue
		}
		if text := messageText(event); text != "" {
			spoken = append(spoken, text)
		}
	}
	if len(spoken) == 0 {
		return ""
	}
	dropped := 0
	if len(spoken) > maxRebuiltMessages {
		dropped = len(spoken) - maxRebuiltMessages
		spoken = spoken[dropped:]
	}
	// The budget is spent from the most recent backwards, so what a long
	// conversation keeps is the part nearest to what is being said now.
	kept, budget := make([]string, 0, len(spoken)), maxRebuiltContextBytes
	for index := len(spoken) - 1; index >= 0; index-- {
		if len(spoken[index]) > budget {
			dropped += index + 1
			break
		}
		budget -= len(spoken[index])
		kept = append([]string{spoken[index]}, kept...)
	}
	if len(kept) == 0 {
		return ""
	}
	var rendered strings.Builder
	if dropped > 0 {
		rendered.WriteString(fmt.Sprintf("- %d earlier repl(ies) are not carried here.\n\n", dropped))
	}
	for _, text := range kept {
		rendered.WriteString(text)
		rendered.WriteString("\n\n")
	}
	return rendered.String()
}

// messageText is the prose one recorded agent message carries. An event whose
// payload is not one the harness wrote is skipped rather than guessed at: what a
// rebuild puts in front of a role has to be what the record actually says.
func messageText(event execution.Event) string {
	if len(event.Payload) == 0 {
		return ""
	}
	var payload struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return ""
	}
	return strings.TrimSpace(payload.Text)
}
