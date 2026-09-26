package readmodel

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// The 2026-09-23 refusals, as the log holds them: a seven_day limit quoted to
// reset on 09-27, met by the product manager's conversation and by the
// development manager's, on the one account and model every agent runs on.
var (
	sevenDayRefused = time.Date(2026, 9, 23, 6, 53, 0, 0, time.UTC)
	sevenDayResets  = time.Date(2026, 9, 27, 3, 0, 0, 0, time.UTC)
	capacityAdded   = time.Date(2026, 9, 24, 9, 0, 0, 0, time.UTC)
	dashboardRead   = time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
)

const (
	productManagerChat     = "chat-91253e0e070c17b0663651cc48602122"
	retiredDevelopmentChat = "chat-419cedb4a013b063f477e322a2a60466"
	currentDevelopmentChat = "chat-3f488b20b95402f3f61e8e27fedb6608"
	everyAgentsAccount     = "default"
	everyAgentsModel       = "opus"
	anotherAccount         = "overflow"
	anotherModel           = "sonnet"
)

func sevenDayRefusal(at time.Time, conversationID string) runstate.UsageLimitExhaustion {
	resets := sevenDayResets
	refused := refusal(at, everyAgentsModel, &resets)
	refused.ConversationID = conversationID
	refused.Waiting = "the conversation " + conversationID
	refused.AccountAlias = everyAgentsAccount
	return refused
}

type fakeCapacityServed struct {
	served []runstate.CapacityServed
	fail   error
}

func (f fakeCapacityServed) List() ([]runstate.CapacityServed, error) { return f.served, f.fail }

// currentConversations is the conversation records as the store keeps them:
// one per agent, naming the conversation each is in now.
func currentConversations(ids ...string) fakeConversations {
	recorded := make([]runstate.Conversation, 0, len(ids))
	for _, id := range ids {
		recorded = append(recorded, runstate.Conversation{ConversationID: id})
	}
	return fakeConversations{recorded: recorded}
}

// capacitySources is the standing's sources with the refusals, what the
// provider has served, and the conversations still current.
func capacitySources(refusals []runstate.UsageLimitExhaustion, served []runstate.CapacityServed, conversations fakeConversations) Sources {
	sources := quietSources()
	sources.Now = func() time.Time { return dashboardRead }
	sources.Agents = fiveAgentsOnOpus()
	sources.UnknownResetPause = 30 * time.Minute
	sources.UsageLimits = fakeUsageLimits{refusals: refusals}
	sources.CapacityServed = fakeCapacityServed{served: served}
	sources.Conversations = conversations
	return sources
}

// The case the work item names: a seven_day refusal recorded, then a turn
// served on the same account and model. Nothing is shown blocked, nothing holds
// every role, and nothing holds intake — the reset the refusal quoted is two
// days off and says nothing any more.
func TestATurnServedOnTheRefusedAccountAndModelClearsTheBlockEverywhere(t *testing.T) {
	t.Parallel()

	refusals := []runstate.UsageLimitExhaustion{sevenDayRefusal(sevenDayRefused, productManagerChat)}
	current := currentConversations(productManagerChat)

	// Before anything is served the refusal stands, so the case below is a
	// reading the evidence changed rather than one that was never blocked.
	before := ReadStanding(context.Background(), capacitySources(refusals, nil, current))
	if len(before.CapacityBlocked.Conversations) != 1 || before.CapacityHold == nil {
		t.Fatalf("before a served turn: blocked = %+v, hold = %+v; want the conversation blocked and every role held",
			before.CapacityBlocked.Conversations, before.CapacityHold)
	}

	served := []runstate.CapacityServed{{
		AccountAlias: everyAgentsAccount,
		Model:        everyAgentsModel,
		At:           capacityAdded,
		What:         "a turn of the product manager conversation " + productManagerChat,
	}}
	after := ReadStanding(context.Background(), capacitySources(refusals, served, current))
	if len(after.CapacityBlocked.Conversations) != 0 {
		t.Fatalf("capacity blocked = %+v, want no conversation once a turn was served on its account and model", after.CapacityBlocked.Conversations)
	}
	if after.CapacityHold != nil {
		t.Fatalf("capacity hold = %+v, want none once a turn was served", after.CapacityHold)
	}
	if strings.Contains(after.Paused, "usage window") {
		t.Fatalf("banner = %q, want no usage-window banner once a turn was served", after.Paused)
	}
	for _, entry := range after.NeedsHuman {
		if entry.Kind == AttentionHold && entry.ID == HoldCapacity {
			t.Fatalf("needs a human = %+v, want no capacity hold on the line", after.NeedsHuman)
		}
	}
	// And intake: the watch session's reading of the same records, narrowed to
	// the developer's endpoints and to named resets, is this same derivation.
	evidence, problem := ReadCapacityEvidence(fakeCapacityServed{served: served}, current)
	if problem != "" {
		t.Fatalf("evidence problem = %q, want none", problem)
	}
	developer := []AgentEndpoint{{Name: "developer", Provider: "claude-code", Model: everyAgentsModel}}
	if hold := ReadCapacityHold(developer, nil, refusals, dashboardRead, 0, evidence); hold.Holding {
		t.Fatalf("intake hold = %+v, want nothing holding intake on a limit a served turn disproved", hold)
	}
}

// A turn served on another account or another model says nothing about this
// window, and a turn served before the refusal was recorded is not evidence
// that it lifted.
func TestOnlyATurnServedLaterOnTheSameAccountAndModelLiftsARefusal(t *testing.T) {
	t.Parallel()

	refused := sevenDayRefusal(sevenDayRefused, productManagerChat)
	for name, served := range map[string]runstate.CapacityServed{
		"another account":        {AccountAlias: anotherAccount, Model: everyAgentsModel, At: capacityAdded},
		"another model":          {AccountAlias: everyAgentsAccount, Model: anotherModel, At: capacityAdded},
		"served before refusing": {AccountAlias: everyAgentsAccount, Model: everyAgentsModel, At: sevenDayRefused.Add(-time.Minute)},
	} {
		evidence := CapacityEvidence{Served: []runstate.CapacityServed{served}}
		if evidence.Lifted(refused) {
			t.Errorf("%s: %+v lifted %+v, want the refusal standing", name, served, refused)
		}
		blocked := ReadCapacityBlocked(nil, []runstate.UsageLimitExhaustion{refused}, dashboardRead, 30*time.Minute, evidence)
		if len(blocked.Conversations) != 1 {
			t.Errorf("%s: capacity blocked = %+v, want the conversation still blocked", name, blocked.Conversations)
		}
	}

	// A refusal recorded before refusals carried the account is lifted by the
	// model being served on any account: it cannot be told apart.
	unaccounted := refused
	unaccounted.AccountAlias = ""
	evidence := CapacityEvidence{Served: []runstate.CapacityServed{{AccountAlias: anotherAccount, Model: everyAgentsModel, At: capacityAdded}}}
	if !evidence.Lifted(unaccounted) {
		t.Fatalf("a refusal naming no account was not lifted by its model being served")
	}
}

// A refusal recorded after the served turn is the provider refusing again, and
// it stands: evidence clears what came before it and nothing after.
func TestARefusalAfterTheServedTurnStands(t *testing.T) {
	t.Parallel()

	later := sevenDayRefusal(capacityAdded.Add(time.Hour), productManagerChat)
	evidence := CapacityEvidence{Served: []runstate.CapacityServed{{AccountAlias: everyAgentsAccount, Model: everyAgentsModel, At: capacityAdded}}}
	blocked := ReadCapacityBlocked(nil, []runstate.UsageLimitExhaustion{sevenDayRefusal(sevenDayRefused, productManagerChat), later}, dashboardRead, 30*time.Minute, evidence)
	if len(blocked.Conversations) != 1 || blocked.Conversations[0].Refusals != 1 || !blocked.Conversations[0].Since.Equal(later.At) {
		t.Fatalf("capacity blocked = %+v, want only the refusal after the served turn", blocked.Conversations)
	}
}

// The development manager's conversation replaced on 09-24 is retired: nothing
// will happen in it again, so it is not listed as blocked and its refusals hold
// nobody, while the conversation that replaced it is read as usual.
func TestARetiredConversationIsNeverShownBlockedAndHoldsNobody(t *testing.T) {
	t.Parallel()

	refusals := []runstate.UsageLimitExhaustion{sevenDayRefusal(sevenDayRefused.Add(39*time.Minute), retiredDevelopmentChat)}
	standing := ReadStanding(context.Background(), capacitySources(refusals, nil, currentConversations(currentDevelopmentChat, productManagerChat)))
	if len(standing.CapacityBlocked.Conversations) != 0 {
		t.Fatalf("capacity blocked = %+v, want the retired conversation not listed", standing.CapacityBlocked.Conversations)
	}
	if standing.CapacityHold != nil {
		t.Fatalf("capacity hold = %+v, want a retired conversation's refusals to hold nobody", standing.CapacityHold)
	}

	// The current one is still read: retiring is about the conversation, not the
	// window.
	current := []runstate.UsageLimitExhaustion{sevenDayRefusal(sevenDayRefused, currentDevelopmentChat)}
	standing = ReadStanding(context.Background(), capacitySources(current, nil, currentConversations(currentDevelopmentChat)))
	if len(standing.CapacityBlocked.Conversations) != 1 || standing.CapacityBlocked.Conversations[0].ConversationID != currentDevelopmentChat {
		t.Fatalf("capacity blocked = %+v, want the current conversation listed", standing.CapacityBlocked.Conversations)
	}
}

// Retirement is read off the real conversation store rather than a fake: the
// store keeps one record per agent, naming the conversation that agent is in
// now, so after the development manager replaces her conversation only the
// replacement is current and only the replaced one's refusals are lifted.
func TestTheConversationStoreNamesOnlyEachAgentsCurrentConversation(t *testing.T) {
	t.Parallel()

	store, err := runstate.NewConversationStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("NewConversationStore() error = %v", err)
	}
	save := func(agent string, role domain.AgentRole) string {
		t.Helper()
		id, err := runstate.NewConversationID()
		if err != nil {
			t.Fatalf("NewConversationID() error = %v", err)
		}
		if err := store.Save(runstate.Conversation{
			SchemaVersion:  runstate.ConversationSchemaVersion,
			ConversationID: id,
			ProductID:      "yoyodyne",
			RepositoryID:   "yoyodyne",
			Agent:          agent,
			Role:           role,
			Backend:        domain.BackendClaudeCode,
			StartedAt:      sevenDayRefused,
			UpdatedAt:      sevenDayRefused,
		}); err != nil {
			t.Fatalf("Save() error = %v", err)
		}
		return id
	}
	retired := save("development-manager", domain.RoleDevelopmentManager)
	productManager := save("product-manager", domain.RoleProductManager)
	replacement := save("development-manager", domain.RoleDevelopmentManager)

	evidence, problem := ReadCapacityEvidence(nil, store)
	if problem != "" {
		t.Fatalf("evidence problem = %q, want none", problem)
	}
	if evidence.Current[retired] || !evidence.Current[replacement] || !evidence.Current[productManager] {
		t.Fatalf("current = %v, want the replacement and the product manager's and not the replaced one", evidence.Current)
	}
	if !evidence.Lifted(sevenDayRefusal(sevenDayRefused, retired)) {
		t.Fatal("a refusal of the replaced conversation stands, want it lifted")
	}
	for _, current := range []string{replacement, productManager} {
		if evidence.Lifted(sevenDayRefusal(sevenDayRefused, current)) {
			t.Fatalf("a refusal of the current conversation %s was lifted, want it standing", current)
		}
	}
}

// Evidence that could not be read clears nothing, and says so: a block cleared
// on a record nobody could read would be a guess, and one left standing is
// only late.
func TestUnreadableEvidenceClearsNothingAndIsNamed(t *testing.T) {
	t.Parallel()

	refusals := []runstate.UsageLimitExhaustion{sevenDayRefusal(sevenDayRefused, retiredDevelopmentChat)}
	conversations := fakeConversations{fail: errors.New("development-manager.json: permission denied")}
	sources := capacitySources(refusals, nil, conversations)
	sources.CapacityServed = fakeCapacityServed{fail: errors.New("capacity-served.json: permission denied")}
	blocked := CapacityBlockedOf(sources, dashboardRead)
	if len(blocked.Conversations) != 1 {
		t.Fatalf("capacity blocked = %+v, want the refusal standing when the evidence could not be read", blocked.Conversations)
	}
	if !strings.Contains(blocked.ConversationsProblem, "capacity-served.json") || !strings.Contains(blocked.ConversationsProblem, "development-manager.json") {
		t.Fatalf("conversations problem = %q, want both unreadable records named", blocked.ConversationsProblem)
	}
}

// A run the provider's usage window stopped is not listed once the provider has
// served its account and model since it stopped; one asleep on its deadline is
// listed whatever was served, because it is still asleep.
func TestAStoppedRunClearsOnAServedTurnAndAWaitingOneStaysListed(t *testing.T) {
	t.Parallel()

	stopped := blockedRun("run-9f8e7d6c", "yoyodyne-ifd.141")
	stopped.Phase = runstate.PhaseDeveloping
	stopped.UsageLimitModel = everyAgentsModel
	stopped.AccountAlias = everyAgentsAccount
	asleep := parkedRun("run-0a1b2c3d", "yoyodyne-ifd.140")
	asleep.UsageLimitModel = everyAgentsModel
	asleep.AccountAlias = everyAgentsAccount
	evidence := CapacityEvidence{Served: []runstate.CapacityServed{{AccountAlias: everyAgentsAccount, Model: everyAgentsModel, At: capacityReadAt.Add(-time.Minute)}}}

	blocked := ReadCapacityBlocked([]runstate.State{stopped, asleep}, nil, capacityReadAt, 30*time.Minute, evidence)
	if len(blocked.Runs) != 1 || blocked.Runs[0].RunID != asleep.RunID || blocked.Runs[0].State != CapacityStateWaiting {
		t.Fatalf("capacity blocked runs = %+v, want only the run still asleep", blocked.Runs)
	}
	// And the parked run holds nobody once its model has been served since it
	// parked, which is what the hold and the intake hold read.
	if hold := ReadCapacityHold(fiveAgentsOnOpus(), []runstate.State{asleep}, nil, capacityReadAt, 30*time.Minute, evidence); hold.Holding {
		t.Fatalf("hold = %+v, want a parked run whose model was served since to hold nobody", hold)
	}
	if hold := ReadCapacityHold(fiveAgentsOnOpus(), []runstate.State{asleep}, nil, capacityReadAt, 30*time.Minute, CapacityEvidence{}); !hold.Holding {
		t.Fatalf("hold = %+v, want the parked run holding every role with no evidence", hold)
	}
}
