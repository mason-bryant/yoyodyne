package orchestrator

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/chat"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

var correctionNow = time.Date(2026, 9, 6, 9, 0, 0, 0, time.UTC)

type correctionClock struct{}

func (correctionClock) Now() time.Time { return correctionNow }

// correctedRole is a role's conversation as a test reaches it: which conversation
// was woken, what it was told, and what the turn came to.
type correctedRole struct {
	woken    []runstate.ConversationIdentity
	messages []string
	turns    []CorrectionTurn
	errs     []error
}

func (r *correctedRole) Wake(_ context.Context, identity runstate.ConversationIdentity, message string) (CorrectionTurn, error) {
	index := len(r.woken)
	r.woken = append(r.woken, identity)
	r.messages = append(r.messages, message)
	var turn CorrectionTurn
	if index < len(r.turns) {
		turn = r.turns[index]
	}
	if index < len(r.errs) && r.errs[index] != nil {
		return turn, r.errs[index]
	}
	return turn, nil
}

// refusedConversation is a conversation whose tracker block was refused and
// nobody has answered, recorded as the refusal machinery records one.
func refusedConversation(t *testing.T, store *runstate.ConversationStore, agent string, turn int, refusedAt time.Time, problem string) runstate.Conversation {
	t.Helper()

	id, err := runstate.NewConversationID()
	if err != nil {
		t.Fatalf("NewConversationID() error = %v", err)
	}
	conversation := runstate.Conversation{
		SchemaVersion:  runstate.ConversationSchemaVersion,
		ConversationID: id,
		ProductID:      "yoyodyne",
		RepositoryID:   "yoyodyne",
		Agent:          agent,
		Role:           domain.RoleProductManager,
		Backend:        domain.BackendClaudeCode,
		ProviderModel:  "opus",
		Turns:          turn,
		StartedAt:      refusedAt.Add(-time.Hour),
		UpdatedAt:      refusedAt,
		RefusedBlock: &runstate.TrackerRefusal{
			Turn:      turn,
			Actions:   3,
			Problem:   problem,
			RefusedAt: refusedAt,
		},
	}
	if err := store.Save(conversation); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	return conversation
}

func conversationStore(t *testing.T, root string) *runstate.ConversationStore {
	t.Helper()

	store, err := runstate.NewConversationStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewConversationStore() error = %v", err)
	}
	return store
}

// The wire this exists to close: a refused block starts a turn, and the harness
// starts it.
//
// The refusal already reached the role's next turn and nothing began one, so both
// of this week's refused batches waited on a person prompting the re-issue. A pass
// now wakes that conversation itself, tells it why, and does it once — a second
// pass over the same refusal finds it claimed and wakes nobody.
func TestAPassWakesTheConversationWhoseTrackerBlockWasRefused(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := conversationStore(t, root)
	refused := refusedConversation(t, store, "product-manager", 4, correctionNow.Add(-time.Minute),
		"the product manager asked for tracker actions the harness cannot read: decode tracker actions: actions[0]: reason is the parking reason at 512 bytes, limit is 480")
	role := &correctedRole{turns: []CorrectionTurn{{ConversationID: refused.ConversationID, CostUSD: 0.12, Actions: 3}}}
	corrector := Corrector{Conversations: store, Claims: store, Roles: role, Clock: correctionClock{}}

	sweep, err := corrector.Correct(context.Background())
	if err != nil {
		t.Fatalf("Correct() error = %v", err)
	}
	if len(sweep.Corrected) != 1 {
		t.Fatalf("corrected = %#v, want the one refusal woken for", sweep.Corrected)
	}
	corrected := sweep.Corrected[0]
	if !corrected.Woken || corrected.Actions != 3 || corrected.Turn != 4 {
		t.Fatalf("corrected = %#v, want a woken turn that re-issued the three lost actions", corrected)
	}
	if corrected.CostUSD != 0.12 {
		t.Fatalf("cost = %v, want the turn's own cost carried back so a bounded session counts it", corrected.CostUSD)
	}
	if corrected.Problem != "" {
		t.Fatalf("problem = %q, want a wakeup that landed to report none", corrected.Problem)
	}
	// The conversation woken is the one that sent the block, addressed by agent
	// rather than by role: two agents on one role hold two conversations, and the
	// refusal belongs to one of them.
	if len(role.woken) != 1 || role.woken[0].Agent != "product-manager" {
		t.Fatalf("woken = %#v, want the agent whose conversation was refused", role.woken)
	}
	// What it is told is why it was woken, and not a paraphrase of the refusal:
	// the refusal itself is already at the top of the turn, in the harness's own
	// words, put there by the machinery that refused the block.
	message := role.messages[0]
	for _, wanted := range []string{"The harness woke you", "turn 4", "above this message", "goes to the operator"} {
		if !strings.Contains(message, wanted) {
			t.Fatalf("the wakeup does not say %q:\n%s", wanted, message)
		}
	}
	if strings.Contains(message, "limit is 480") {
		t.Fatalf("the wakeup restated the refusal instead of pointing at it:\n%s", message)
	}

	// Once. A second pass over the same record wakes nobody, which is what keeps a
	// refusal a role cannot answer from costing a turn on every pull.
	again, err := Corrector{Conversations: store, Claims: store, Roles: role, Clock: correctionClock{}}.Correct(context.Background())
	if err != nil {
		t.Fatalf("second Correct() error = %v", err)
	}
	if len(again.Corrected) != 0 || len(role.woken) != 1 {
		t.Fatalf("the second pass woke %d conversation(s) again: %#v", len(role.woken)-1, again.Corrected)
	}
}

// A pass wakes one conversation, and it is the one that has waited longest.
func TestAPassWakesOneConversationAndTakesTheOldestRefusalFirst(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := conversationStore(t, root)
	refusedConversation(t, store, "product-manager", 2, correctionNow.Add(-time.Minute), "the newer refusal")
	refusedConversation(t, store, "second-product-manager", 5, correctionNow.Add(-time.Hour), "the older refusal")
	role := &correctedRole{}
	corrector := Corrector{Conversations: store, Claims: store, Roles: role, Clock: correctionClock{}}

	sweep, err := corrector.Correct(context.Background())
	if err != nil {
		t.Fatalf("Correct() error = %v", err)
	}
	if len(sweep.Corrected) != 1 {
		t.Fatalf("corrected = %#v, want one wakeup per pass", sweep.Corrected)
	}
	if sweep.Corrected[0].Agent != "second-product-manager" {
		t.Fatalf("woke %q, want the conversation whose actions have been missing longest", sweep.Corrected[0].Agent)
	}
	// A turn that answered and asked for nothing is not reported as a correction:
	// the actions are still lost, and a pass that said only "woken" would read as
	// one put right.
	if !strings.Contains(sweep.Corrected[0].Problem, "without asking for any tracker action") {
		t.Fatalf("problem = %q, want a turn that re-issued nothing said out loud", sweep.Corrected[0].Problem)
	}
	// The next pass takes the next one, which is what "one per pass" costs and
	// what it buys.
	next, err := corrector.Correct(context.Background())
	if err != nil {
		t.Fatalf("second Correct() error = %v", err)
	}
	if len(next.Corrected) != 1 || next.Corrected[0].Agent != "product-manager" {
		t.Fatalf("corrected = %#v, want the newer refusal on the next pass", next.Corrected)
	}
}

// A paused harness wakes nobody and costs no refusal its wakeup. A wakeup is a
// provider invocation, so the operator's pause covers it exactly as it covers a
// run, a turn, a delivery, and a firing.
func TestAPausedHarnessWakesNobodyToCorrectARefusedBlock(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := conversationStore(t, root)
	refusedConversation(t, store, "product-manager", 1, correctionNow, "a refusal nobody will be woken for while the pause stands")
	role := &correctedRole{}
	held := runstate.OperatorHold{HeldAt: correctionNow.Add(-time.Hour)}
	corrector := Corrector{
		Conversations: store,
		Claims:        store,
		Roles:         role,
		Holds:         pausedHolds{hold: held, held: true},
		Clock:         correctionClock{},
	}

	sweep, err := corrector.Correct(context.Background())
	if err != nil {
		t.Fatalf("Correct() error = %v", err)
	}
	if sweep.Paused == nil || len(role.woken) != 0 {
		t.Fatalf("sweep = %#v, woken = %#v, want a paused pass that woke nobody", sweep, role.woken)
	}
	// Nothing was claimed, so the refusal keeps the wakeup it is owed for after
	// the pause is lifted.
	recorded, err := store.Load(runstate.ConversationIdentity{Agent: "product-manager", Role: domain.RoleProductManager})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !recorded.RefusedBlock.AwaitingWakeup() {
		t.Fatalf("refused block = %#v, want the pause to have cost it nothing", *recorded.RefusedBlock)
	}
}

// A wakeup that never reached the role is never reported as one that did, and a
// woken turn whose own block is refused again is reported as what it is: the
// self-correction spent, with the actions still lost.
func TestAWakeupThatFailedAndOneRefusedAgainAreBothSaidOutLoud(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := conversationStore(t, root)
	refusedConversation(t, store, "product-manager", 3, correctionNow, "the first refusal")
	unreachable := &correctedRole{errs: []error{ErrRoleUnreachable}}
	sweep, err := Corrector{Conversations: store, Claims: store, Roles: unreachable, Clock: correctionClock{}}.Correct(context.Background())
	if err != nil {
		t.Fatalf("Correct() error = %v", err)
	}
	if len(sweep.Corrected) != 1 || sweep.Corrected[0].Woken {
		t.Fatalf("corrected = %#v, want a wakeup reported as one that did not happen", sweep.Corrected)
	}
	if !strings.Contains(sweep.Corrected[0].Problem, "could not be woken") {
		t.Fatalf("problem = %q, want it to say the role was never asked", sweep.Corrected[0].Problem)
	}

	second := t.TempDir()
	refusedAgain := conversationStore(t, second)
	refusedConversation(t, refusedAgain, "product-manager", 3, correctionNow, "the first refusal")
	role := &correctedRole{turns: []CorrectionTurn{{Refused: "the product manager asked for tracker actions the harness cannot read: decode tracker actions: unexpected trailing content after the actions"}}}
	again, err := Corrector{Conversations: refusedAgain, Claims: refusedAgain, Roles: role, Clock: correctionClock{}}.Correct(context.Background())
	if err != nil {
		t.Fatalf("Correct() error = %v", err)
	}
	if len(again.Corrected) != 1 || !again.Corrected[0].Woken {
		t.Fatalf("corrected = %#v, want a turn that was taken", again.Corrected)
	}
	if !strings.Contains(again.Corrected[0].Problem, "the operator has it") {
		t.Fatalf("problem = %q, want a second refusal reported as the operator's", again.Corrected[0].Problem)
	}
}

// A corrector wired without what it needs refuses rather than silently waking
// nobody, because a self-correction that quietly stopped existing is exactly the
// silence this replaced.
func TestACorrectorWiredWithoutItsRecordsRefuses(t *testing.T) {
	t.Parallel()

	if _, err := (Corrector{}).Correct(context.Background()); err == nil {
		t.Fatalf("Correct() error = nil, want a corrector with nothing to read or wake refused")
	}
}

// The two incidents replayed, end to end and with no human in between.
//
// On 2026-09-05 a park reason ran past what the tracker can hold and the block
// carrying it was refused whole; on 2026-09-06 a handle action named something
// that is not a report identifier and the same thing happened. Both times the
// refusal was recorded, both times it was waiting at the top of the product
// manager's next turn, and both times nothing started that turn — so the
// operator's assistant had to prompt the re-issue of actions the product manager
// could have corrected herself.
//
// This is the same sequence with nobody prompting it: the conversation refuses
// the block, the harness's own pass finds the refusal and wakes the conversation,
// and the turn it starts re-issues the actions and lands them.
func TestARefusalIsCorrectedWithNoHumanInBetween(t *testing.T) {
	t.Parallel()

	for _, incident := range []struct {
		name    string
		refused string
		fixed   string
		refusal string
	}{
		{
			name:    "the oversized park reason of 2026-09-05",
			refused: `{"action":"park","id":"yoyodyne-ifd.311","reason":"` + strings.Repeat("y", domain.MaxWorkItemParkingBytes+1) + `"}`,
			fixed:   `{"action":"park","id":"yoyodyne-ifd.311","reason":"waiting on the design it derives from"}`,
			refusal: "parking reason",
		},
		{
			name:    "the malformed handle id of 2026-09-06",
			refused: `{"action":"handle","report":"the-third-one","reason":"already fixed by ifd.314"}`,
			fixed:   `{"action":"park","id":"yoyodyne-ifd.311","reason":"waiting on the design it derives from"}`,
			refusal: "not a report identifier",
		},
	} {
		t.Run(incident.name, func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			store := conversationStore(t, root)
			tracker := &parkingTracker{}

			// The turn that loses the batch. Nobody is told but the record.
			losing, _ := correctionChat(t, store, tracker, trackerBlock("Parking it.", incident.refused))
			if _, err := losing.Send(context.Background(), "park ifd.311"); err == nil {
				t.Fatalf("Send() error = nil, want the block refused")
			}
			if len(tracker.parked) != 0 {
				t.Fatalf("parked = %#v, want a refused block to have changed nothing", tracker.parked)
			}

			// The harness's own pass. It reads the refusal off the record, wakes the
			// conversation, and the turn it starts re-issues what was lost.
			corrected, replaying := correctionChat(t, store, tracker,
				trackerBlock("Re-issuing what was refused.", incident.fixed),
				"That is the parking done.")
			role := &sendingRole{session: corrected, backend: replaying}
			sweep, err := Corrector{Conversations: store, Claims: store, Roles: role, Clock: correctionClock{}}.Correct(context.Background())
			if err != nil {
				t.Fatalf("Correct() error = %v", err)
			}
			if len(sweep.Corrected) != 1 || !sweep.Corrected[0].Woken {
				t.Fatalf("corrected = %#v, want the conversation woken", sweep.Corrected)
			}
			if sweep.Corrected[0].Actions != 1 {
				t.Fatalf("corrected = %#v, want the re-issued action counted", sweep.Corrected[0])
			}
			// The refusal reached the woken turn in the harness's own words, carried by
			// the machinery that wrote it rather than by anything the wakeup restated.
			if !strings.Contains(role.prompt, incident.refusal) || !strings.Contains(role.prompt, "Issue the actions you still want again") {
				t.Fatalf("the woken turn did not open with the refusal:\n%s", role.prompt)
			}
			// And the action the block lost is on the tracker, with nobody having
			// carried the refusal to the product manager.
			if len(tracker.parked) != 1 || tracker.parked[0] != "yoyodyne-ifd.311" {
				t.Fatalf("parked = %#v, want the re-issued park to have landed", tracker.parked)
			}
			// Nothing is owed after it, so no later pass wakes the same conversation
			// again.
			recorded, err := store.Load(runstate.ConversationIdentity{Agent: "product-manager", Role: domain.RoleProductManager})
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			if recorded.RefusedBlock != nil {
				t.Fatalf("refused block = %#v, want it cleared by the correction", *recorded.RefusedBlock)
			}
		})
	}
}

// sendingRole wakes a real conversation, which is what makes the replay above an
// end-to-end one rather than two halves asserted separately.
type sendingRole struct {
	session *chat.Session
	prompt  string
	backend *replayBackend
}

func (r *sendingRole) Wake(ctx context.Context, _ runstate.ConversationIdentity, message string) (CorrectionTurn, error) {
	reply, err := r.session.Send(ctx, message)
	if len(r.backend.prompts) > 0 {
		r.prompt = r.backend.prompts[0]
	}
	turn := CorrectionTurn{ConversationID: r.session.Evidence().ConversationID}
	for _, action := range reply.Actions {
		if action.Applied {
			turn.Actions++
		}
	}
	var refused *chat.TrackerError
	if errors.As(err, &refused) {
		turn.Refused = refused.Error()
		return turn, nil
	}
	return turn, err
}

// correctionChat is a real conversation over a scripted provider, recorded in the
// store the corrector reads.
func correctionChat(t *testing.T, store *runstate.ConversationStore, tracker chat.Tracker, replies ...string) (*chat.Session, *replayBackend) {
	t.Helper()

	backend := &replayBackend{replies: replies}
	session, err := chat.Open(chat.Options{
		Role:         domain.RoleProductManager,
		Agent:        string(domain.RoleProductManager),
		Backend:      backend,
		Store:        store,
		Tracker:      tracker,
		Model:        "opus",
		Persona:      "You are the product manager.",
		Provider:     domain.BackendClaudeCode,
		Repository:   t.TempDir(),
		ProductID:    "yoyodyne",
		RepositoryID: "yoyodyne",
		Briefing:     chat.Briefing{Text: "# The product\n\nYoyodyne.\n", GatheredAt: correctionNow},
		Clock:        correctionClock{},
	})
	if err != nil {
		t.Fatalf("chat.Open() error = %v", err)
	}
	return session, backend
}

func trackerBlock(prose string, actions ...string) string {
	return prose + "\n\n```yoyodyne-tracker\n{\"actions\":[" + strings.Join(actions, ",") + "]}\n```\n"
}

// replayBackend answers scripted turns and keeps the prompts it was given, which
// is what says the refusal reached the woken turn.
type replayBackend struct {
	replies []string
	prompts []string
}

func (b *replayBackend) Run(_ context.Context, request backendapi.RunRequest) (backendapi.RunResult, error) {
	index := len(b.prompts)
	b.prompts = append(b.prompts, request.Prompt)
	if index >= len(b.replies) {
		return backendapi.RunResult{LastEvent: request.LastSequence + 1}, errors.New("unexpected conversation turn")
	}
	return backendapi.RunResult{
		Backend:   domain.BackendClaudeCode,
		SessionID: "session-1",
		FinalText: b.replies[index],
		LastEvent: request.LastSequence + 1,
	}, nil
}

// parkingTracker is the work tracker as this replay needs it: the parks that were
// actually carried out, and enough of an item to park.
type parkingTracker struct {
	parked []string
}

func (t *parkingTracker) Show(_ context.Context, id string) (beads.WorkItem, error) {
	return beads.WorkItem{ID: id, Title: "an admitted item", Status: "open"}, nil
}

func (t *parkingTracker) List(_ context.Context, _ string) ([]beads.WorkItem, error) { return nil, nil }

func (t *parkingTracker) Create(_ context.Context, item beads.NewWorkItem) (beads.WorkItem, error) {
	return beads.WorkItem{ID: "yoyodyne-new", Title: item.Title, Status: "open"}, nil
}

func (t *parkingTracker) Update(_ context.Context, id string, change beads.WorkItemChange) (beads.WorkItem, error) {
	if change.Parking != nil {
		t.parked = append(t.parked, id)
	}
	return beads.WorkItem{ID: id, Status: "open"}, nil
}

func (t *parkingTracker) Block(_ context.Context, id, _ string) (beads.WorkItem, error) {
	return beads.WorkItem{ID: id, Status: "open"}, nil
}

func (t *parkingTracker) AddBlocker(_ context.Context, _, _ string) error    { return nil }
func (t *parkingTracker) RemoveBlocker(_ context.Context, _, _ string) error { return nil }

func (t *parkingTracker) Complete(_ context.Context, id, _ string) (beads.WorkItem, error) {
	return beads.WorkItem{ID: id, Status: "closed"}, nil
}

var _ execution.Clock = correctionClock{}
