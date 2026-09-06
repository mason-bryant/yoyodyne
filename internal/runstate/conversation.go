package runstate

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// ConversationSchemaVersion is versioned independently of run state. A
// conversation is not a run: it has no worktree, no checks, no verdict, and
// nothing to integrate, so it is recorded in its own shape rather than squeezed
// into a schema whose invariants describe bounded work.
//
// It stays 1 because every addition since has been an optional key: a record
// written before the work item this conversation last ran was kept still
// decodes, and its absence means what it always meant, which is that this
// conversation has started nothing.
const ConversationSchemaVersion = 1

// ErrNoConversation reports that a role has no recorded conversation, so the
// caller starts one instead of resuming.
var ErrNoConversation = errors.New("role has no recorded conversation")

// ErrConversationHeld reports that another process holds the conversation. It
// is a sentinel because "somebody else is talking to this agent" and "the state
// directory could not be read" lead to opposite conclusions, and a caller that
// cannot tell them apart reports a broken state root as a conversation in
// progress.
var ErrConversationHeld = errors.New("already held by another process")

// Conversation is the durable record of one operator conversation with an
// agent. It exists so a conversation survives the process that held it: the
// provider session identifier is what a later process resumes from, and the
// requested and resolved model selectors are the evidence of what actually
// answered.
type Conversation struct {
	SchemaVersion  int              `json:"schema_version"`
	ConversationID string           `json:"conversation_id"`
	ProductID      domain.ProductID `json:"product_id"`
	RepositoryID   string           `json:"repository_id"`
	// Agent is the configured agent this conversation is with, and Role is the
	// authority it carries. They are usually the same word, because an agent is
	// conventionally named for its role, and they are recorded separately
	// because a project may configure two agents for one role: those are two
	// identities with two personas, two model selectors, and two provider
	// sessions, and a record that named only the role would have each of them
	// resuming the other's session.
	//
	// It is empty on a record written before the agent was part of the identity.
	// Such a record was necessarily written for the agent named after its role —
	// nothing else could have addressed it — so it keeps loading under that name
	// and acquires the agent the next time it is saved.
	Agent   string           `json:"agent,omitempty"`
	Role    domain.AgentRole `json:"role"`
	Backend domain.Backend   `json:"backend"`
	// ProviderSessionID is the session a later process resumes. It is empty
	// until a turn completes, and a conversation without one can only be
	// started again rather than continued.
	ProviderSessionID string `json:"provider_session_id,omitempty"`
	// ProviderModel is the selector the conversation requested and
	// ProviderResolvedModel is what the provider reported serving it, because a
	// floating family alias makes the resolved identifier the only real record.
	ProviderModel         string `json:"provider_model,omitempty"`
	ProviderResolvedModel string `json:"provider_resolved_model,omitempty"`
	// AccountAlias is the provider account the turn this record last took was
	// answered on, and ConfigRevision the configuration in force while it was.
	// They sit beside the backend and the model selectors and are kept exactly as
	// those are: rewritten by each completed turn, so the record says what served
	// the conversation as it now stands. Under a pool that stops being
	// bookkeeping — the account answering this conversation is the agent's own
	// rather than the machine's default, so the alias is the only thing on the
	// record that says whose subscription is paying for it.
	//
	// What pins every turn rather than the last one is the cost log, which takes a
	// line per provider invocation carrying the account and the revision that
	// served it, and refuses a line that names either. So a conversation resumed
	// after a configuration edit or an account move still has each earlier turn's
	// attribution, on that turn's own line, and this record is not the only copy
	// of any of it.
	//
	// That is the whole of why this is one pair and an exchange's is one per
	// round. An exchange record holds its rounds, so a round is a thing already in
	// the record to pin; a conversation record holds no turns at all — it is a
	// summary every turn rewrites in place — and a per-turn list inside it would
	// be an unbounded array in a file that is rewritten on every turn, kept for a
	// fact the cost log already keeps correctly.
	//
	// Both are empty on a conversation recorded before the harness wrote them
	// down, and on one whose first turn has not completed.
	AccountAlias   string `json:"account_alias,omitempty"`
	ConfigRevision string `json:"config_revision,omitempty"`
	// Build is the repository revision the harness binary holding this
	// conversation was built from, rewritten by each completed turn exactly as the
	// pair above is. A conversation outlives the process that opened it, so what
	// this says is which harness is answering it now — and a conversation an
	// operator left open for days is one of the residents that quietly goes on
	// running a binary the harness has moved past.
	//
	// What pins every turn rather than the last one is the same cost log the
	// account and the revision are pinned by, which now carries the build on each
	// line for the same reason it carries those.
	//
	// It is empty on a conversation recorded before the harness wrote it down, on
	// one whose first turn has not completed, and where the binary carries no
	// revision of its own.
	Build string `json:"build,omitempty"`
	Turns int    `json:"turns"`
	// PendingTrackerResults is what an agent asked of the work tracker and has not
	// been told the result of yet, already rendered as the text its next turn is
	// given. It is durable for the same reason the provider session is: the agent
	// acted, the process that watched it act may be gone, and an agent that never
	// learns what its own actions did is one that will describe them wrongly.
	//
	// What it carries is the results of actions the harness carried out, and the
	// refusal of a block it would not read at all. The second is the same fact in
	// its starkest form — every action in the block is a thing the agent believes
	// it did and did not — so it travels the same way rather than in a field of its
	// own.
	PendingTrackerResults string `json:"pending_tracker_results,omitempty"`
	// RefusedBlock is the tracker block this conversation had refused whole and
	// has not answered yet. It is the same fact the pending results above carry
	// into the role's next turn, kept as a record rather than as prose so that
	// something other than the next person at a terminal can act on it: the words
	// are what the role reads, and this is what says a turn is owed and whether
	// the harness has started one.
	//
	// It is cleared by the first turn whose reply the harness could read, which is
	// the role having answered the refusal one way or another. A refusal arriving
	// while one is already recorded is therefore the second in a row, and is
	// escalated rather than woken for; see TrackerRefusal.
	RefusedBlock *TrackerRefusal `json:"refused_block,omitempty"`
	// ContextGatheredAt is when the picture of the product the agent is working
	// from was assembled, and ContextCommit is the repository commit it was
	// assembled against. They are durable because the process that briefed the
	// agent is usually not the one that resumes it, and a resumed conversation
	// that cannot say how old its picture is will describe a repository as it
	// was hours ago and sound exactly as certain about it. They are empty on a
	// conversation recorded before the harness wrote them down, and on one whose
	// first turn has not completed: the picture is recorded when it is
	// delivered, never when it is merely taken.
	ContextGatheredAt time.Time `json:"context_gathered_at,omitempty"`
	ContextCommit     string    `json:"context_commit,omitempty"`
	// LastRunWorkItemID is the work item of the run this conversation started
	// most recently. It is durable for the same reason the rest of this is: the
	// process that started the run is often not the one the operator comes back
	// to, and "what did that change" is a question about the run they last
	// watched rather than about whichever process was holding it. It is empty on
	// a conversation that has never started one.
	LastRunWorkItemID string `json:"last_run_work_item_id,omitempty"`
	// DeliveredAmendmentIDs are the changes proposed to this role's documents
	// that the conversation has already carried into a turn. A proposal stays
	// pending until somebody decides it, so without a record of what was already
	// said the same list would be delivered again on every turn — and, because
	// the process that delivered it is usually not the one that resumes the
	// conversation, it is durable for the same reason the provider session is.
	// It is bounded, and an id dropped from it is delivered once more rather
	// than lost, which is the right way for this to fail.
	DeliveredAmendmentIDs []string `json:"delivered_amendment_ids,omitempty"`
	// DeliveredReportIDs are the collected reports this conversation has already
	// carried into a turn. A report stays in the pile until somebody records what
	// became of it, so without this the same unhandled reports would be delivered
	// again every turn. It is durable and bounded for exactly the reasons the
	// amendment ids above are, and an id dropped from it is offered once more
	// rather than lost — which is the right way for this to fail, because the
	// failure it must never have is a report nobody is ever shown.
	DeliveredReportIDs []string `json:"delivered_report_ids,omitempty"`
	// PendingProposals are the work items an agent proposed that nobody has
	// decided yet. They are durable for the reason the provider session is, and
	// the reason is sharper here than anywhere else in this record: a proposal
	// made by one `--message` invocation is decided by another, and a process
	// that could not read back what was proposed had nothing for an approval to
	// name. The operator's "y" then arrived as ordinary speech, the proposal was
	// never decided, and nothing reached the queue.
	//
	// It holds only the undecided ones. A decision is an event in the log and
	// stays there; what is kept here is the set a later process may still act on,
	// so a proposal leaves this list the moment it is approved or declined.
	PendingProposals []PendingProposal `json:"pending_proposals,omitempty"`
	// PendingNotices is the account of harness activity the agent has not been
	// told about yet, and PendingNoticesDropped says older activity was cut to
	// keep it bounded. They are durable for the same reason the tracker results
	// beside them are: the process that watched the operator act is usually not
	// the one that asks the next question, and an agent that never learns the
	// operator approved its own proposal will describe the queue wrongly.
	PendingNotices        []string  `json:"pending_notices,omitempty"`
	PendingNoticesDropped bool      `json:"pending_notices_dropped,omitempty"`
	LastSequence          uint64    `json:"last_sequence"`
	StartedAt             time.Time `json:"started_at"`
	UpdatedAt             time.Time `json:"updated_at"`
}

// TrackerRefusal is one refused tracker block as the record keeps it, and what
// the harness has done about it.
//
// The refusal already reaches the role at the start of its next turn. What this
// adds is the half nothing carried: whether a turn has been started for it. A
// refusal recorded and never woken for waits on somebody typing, which is how
// both of this week's refused batches came to need the operator's assistant to
// prompt the re-issue.
//
// The two endings are deliberate. A refusal is woken for once, because a wakeup
// that fired again on each pass would be the harness asking a role that cannot
// answer, over and over, at a turn a time. A refusal that arrives while one is
// still outstanding — the woken turn refused again, or the same defect back —
// carries the reason it will not be woken for instead, and that is what the
// operator is told.
type TrackerRefusal struct {
	// Turn is the conversation turn whose block was refused, and Actions how many
	// it asked for. Actions is zero where the harness could not count them, which
	// is a payload it never decoded rather than a block that asked for nothing.
	Turn    int `json:"turn"`
	Actions int `json:"actions,omitempty"`
	// Problem is the refusal in the harness's own words, which is the same text
	// the role is given back.
	Problem   string    `json:"problem"`
	RefusedAt time.Time `json:"refused_at"`
	// WokenAt is when the harness started a turn for this refusal. Zero is a
	// refusal whose wakeup has not fired.
	WokenAt time.Time `json:"woken_at,omitempty"`
	// Escalated is why the harness will not wake for this refusal and has put it
	// in front of the operator instead. Empty is the ordinary refusal.
	Escalated string `json:"escalated,omitempty"`
}

// AwaitingWakeup reports a refusal the harness still owes a turn: nobody has
// been woken for it, and it is not one the operator has been handed instead.
func (r TrackerRefusal) AwaitingWakeup() bool {
	return r.Escalated == "" && r.WokenAt.IsZero()
}

// MaxTrackerRefusalProblemBytes bounds the refusal a conversation record carries.
// The whole of what the role is given back is bounded separately by the pending
// results it travels in; this is the copy the record keeps, and it is held well
// below the state file's own limit for the reason every other bound here is.
const MaxTrackerRefusalProblemBytes = 4 << 10

// ErrNoRefusalAwaitingWakeup reports a conversation with no refused tracker
// block owed a turn: none recorded, one already woken for, or one the operator
// has been handed. It is a sentinel because a pass walking past a conversation
// somebody else just woke is the record doing its job rather than a failure.
var ErrNoRefusalAwaitingWakeup = errors.New("the conversation has no refused tracker block awaiting a wakeup")

// PendingProposal is one proposed work item as the record keeps it: what was
// proposed, which turn proposed it, and why the harness did not simply admit
// it. The conversation it belongs to is the record it sits in, so it is not
// repeated here.
//
// It is declared in this package rather than shared with the conversation code
// that builds it, because that code already depends on this one and the
// dependency may not run both ways. The field names are the ones the proposal
// contract uses, so what is written here reads as what was proposed.
type PendingProposal struct {
	ID          string `json:"id"`
	Turn        int    `json:"turn"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Rationale   string `json:"rationale"`
	Goal        string `json:"goal"`
	Parent      string `json:"parent,omitempty"`
	// Dependencies name the items the proposed work waits for.
	Dependencies []string `json:"dependencies,omitempty"`
	// Class is the kind of work the proposal claims to be, where a project treats
	// a kind of work differently at admission. It is kept because it decides
	// whether the operator is asked at all, so a proposal that came back without
	// it would be a different proposal from the one that was made.
	Class string `json:"class,omitempty"`
	// Asking is what kept this proposal out of a queue it would otherwise have
	// gone into, worked out when it was proposed rather than when it is decided.
	Asking string `json:"asking,omitempty"`
}

// MaxPendingProposals bounds the undecided proposals one conversation carries.
// A proposal carries the whole of what an operator decides from — its
// description and its rationale — so unlike the amendment identifiers above it
// is a bound on something large: twenty of them at their own maximum size is
// comfortably inside what a state file may hold, while a hundred would put a
// conversation in the state where every save of it fails.
//
// It is well above any conversation anybody holds. One reply proposes at most
// ten items, and a second reply proposing ten more with none of the first
// decided is already a conversation nobody is reading.
const MaxPendingProposals = 20

// MaxPendingNotices bounds the account of harness activity one conversation
// carries forward. It matches the bound the conversation itself keeps, so what
// is written is always what was going to be delivered.
const MaxPendingNotices = 20

// MaxDeliveredAmendmentIDs bounds that record. It is far above any plausible
// backlog of undecided proposals, and it exists so a conversation's state file
// cannot grow without limit on a queue nobody works through.
const MaxDeliveredAmendmentIDs = 256

// MaxDeliveredReportIDs bounds the record of reports already carried into a
// turn, for the reason the amendment bound exists: a conversation's state file
// cannot be allowed to grow without limit on a pile nobody works through.
const MaxDeliveredReportIDs = 256

// MaxPendingTrackerResultBytes bounds the results a conversation may carry
// forward. The record has to stay reloadable, so what waits inside it is bounded
// well below the state file's own limit rather than growing with the tracker.
const MaxPendingTrackerResultBytes = 64 << 10

var conversationIDPattern = regexp.MustCompile(`^chat-[a-f0-9]{32}$`)

func NewConversationID() (string, error) {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("generate conversation id: %w", err)
	}
	return "chat-" + hex.EncodeToString(bytes), nil
}

func (c Conversation) Validate() error {
	var problems []error
	if c.SchemaVersion != ConversationSchemaVersion {
		problems = append(problems, fmt.Errorf("schema_version must be %d", ConversationSchemaVersion))
	}
	if !conversationIDPattern.MatchString(c.ConversationID) {
		problems = append(problems, errors.New("conversation_id is invalid"))
	}
	if err := domain.ValidateIdentifier("product id", string(c.ProductID)); err != nil {
		problems = append(problems, err)
	}
	if err := domain.ValidateIdentifier("repository id", c.RepositoryID); err != nil {
		problems = append(problems, err)
	}
	if err := domain.ValidateIdentifier("role", string(c.Role)); err != nil {
		problems = append(problems, err)
	}
	// The agent is optional only because records predate it; one that names an
	// agent must name a usable one, because the name is also a path.
	if c.Agent != "" {
		if err := domain.ValidateIdentifier("agent", c.Agent); err != nil {
			problems = append(problems, err)
		}
	}
	if !c.Backend.Valid() {
		problems = append(problems, errors.New("backend is invalid"))
	}
	if c.Turns < 0 {
		problems = append(problems, errors.New("turns cannot be negative"))
	}
	// A completed turn always knows which selector it asked for, so a recorded
	// turn without one would leave the conversation unauditable.
	if c.Turns > 0 && c.ProviderModel == "" {
		problems = append(problems, errors.New("a recorded turn requires the requested model selector"))
	}
	// The account, the configuration, and the build are absent from every record
	// written before they were carried, so what is checked is the shape of one
	// that is there rather than that it is there at all: a conversation recorded
	// by an older build must still load, and a record naming an account, a
	// configuration, or a revision nothing could have produced says less than one
	// naming none of them, because it reads as evidence.
	if c.AccountAlias != "" && !accountAliasPattern.MatchString(c.AccountAlias) {
		problems = append(problems, errors.New("account_alias is not an account alias"))
	}
	if c.ConfigRevision != "" && !configRevisionPattern.MatchString(c.ConfigRevision) {
		problems = append(problems, errors.New("config_revision is not a configuration revision"))
	}
	if c.Build != "" && !buildPattern.MatchString(c.Build) {
		problems = append(problems, errors.New("build is not a revision"))
	}
	if len(c.PendingTrackerResults) > MaxPendingTrackerResultBytes {
		problems = append(problems, fmt.Errorf("pending tracker results are %d bytes, limit is %d",
			len(c.PendingTrackerResults), MaxPendingTrackerResultBytes))
	}
	// A recorded refusal that says nothing about what was wrong is one nothing can
	// act on: the wakeup carries the harness's own words, and a record without
	// them would wake a role to correct something nobody stated.
	if c.RefusedBlock != nil {
		if strings.TrimSpace(c.RefusedBlock.Problem) == "" {
			problems = append(problems, errors.New("a recorded tracker refusal must carry what was wrong with the block"))
		}
		if len(c.RefusedBlock.Problem) > MaxTrackerRefusalProblemBytes {
			problems = append(problems, fmt.Errorf("the recorded tracker refusal is %d bytes, limit is %d",
				len(c.RefusedBlock.Problem), MaxTrackerRefusalProblemBytes))
		}
		if c.RefusedBlock.RefusedAt.IsZero() {
			problems = append(problems, errors.New("a recorded tracker refusal must say when it was refused"))
		}
	}
	if len(c.DeliveredAmendmentIDs) > MaxDeliveredAmendmentIDs {
		problems = append(problems, fmt.Errorf("%d delivered amendment ids are recorded, limit is %d",
			len(c.DeliveredAmendmentIDs), MaxDeliveredAmendmentIDs))
	}
	if len(c.DeliveredReportIDs) > MaxDeliveredReportIDs {
		problems = append(problems, fmt.Errorf("%d delivered report ids are recorded, limit is %d",
			len(c.DeliveredReportIDs), MaxDeliveredReportIDs))
	}
	if len(c.PendingProposals) > MaxPendingProposals {
		problems = append(problems, fmt.Errorf("%d undecided proposals are recorded, limit is %d",
			len(c.PendingProposals), MaxPendingProposals))
	}
	// An undecided proposal nobody can name is one nobody can decide, so the
	// identifier an approval has to say is required rather than merely usual.
	for i, proposal := range c.PendingProposals {
		if strings.TrimSpace(proposal.ID) == "" {
			problems = append(problems, fmt.Errorf("pending_proposals[%d] has no id", i))
		}
	}
	if len(c.PendingNotices) > MaxPendingNotices {
		problems = append(problems, fmt.Errorf("%d pending notices are recorded, limit is %d",
			len(c.PendingNotices), MaxPendingNotices))
	}
	// A picture is deliberately allowed to predate the conversation that carries
	// it: it is assembled before the record exists, and a refresh moves it
	// forward afterwards. What it may not do is claim to be from the future,
	// which would make every comparison against it read as fresher than it is.
	if !c.ContextGatheredAt.IsZero() && !c.UpdatedAt.IsZero() && c.ContextGatheredAt.After(c.UpdatedAt) {
		problems = append(problems, errors.New("context_gathered_at cannot be after updated_at"))
	}
	if c.StartedAt.IsZero() || c.UpdatedAt.IsZero() {
		problems = append(problems, errors.New("started_at and updated_at are required"))
	}
	if c.UpdatedAt.Before(c.StartedAt) {
		problems = append(problems, errors.New("updated_at cannot be before started_at"))
	}
	if len(problems) > 0 {
		return fmt.Errorf("invalid conversation state: %w", errors.Join(problems...))
	}
	return nil
}

// ConversationIdentity names one durable conversation: the agent that holds it
// and the role whose authority it carries. Both are needed and neither is
// enough. The agent decides which record this is, because two agents filling one
// role are two identities with two provider sessions; the role decides what the
// conversation may do, and is checked against the record so an agent whose
// configured role changed cannot silently carry on under its old authority.
type ConversationIdentity struct {
	Agent string
	Role  domain.AgentRole
}

func (i ConversationIdentity) String() string {
	if i.Agent == "" || i.Agent == string(i.Role) {
		return string(i.Role)
	}
	return i.Agent + " (" + string(i.Role) + ")"
}

// validate keeps an identity usable as a path before it is one.
func (i ConversationIdentity) validate() error {
	if err := domain.ValidateIdentifier("agent", i.Agent); err != nil {
		return err
	}
	return domain.ValidateIdentifier("role", string(i.Role))
}

// ConversationStore keeps conversations in the same operating-system state root
// as runs, beside them rather than among them. A conversation is stored under
// the agent it belongs to, so a restarted process finds the one conversation it
// should resume without searching for it. An agent named for its role — which is
// what every generated configuration produces — stores it exactly where the
// role-keyed layout this replaced put it, so no existing conversation moves.
type ConversationStore struct {
	root      string
	productID domain.ProductID
}

func NewConversationStore(root string, productID domain.ProductID) (*ConversationStore, error) {
	if !filepath.IsAbs(root) {
		return nil, errors.New("state root must be an absolute path")
	}
	if err := domain.ValidateIdentifier("product id", string(productID)); err != nil {
		return nil, err
	}
	return &ConversationStore{
		root:      filepath.Join(filepath.Clean(root), "products", string(productID), "conversations"),
		productID: productID,
	}, nil
}

func (s *ConversationStore) Root() string {
	return s.root
}

// Hold takes exclusive ownership of an agent's conversation for as long as this
// process is talking to it. Two processes resuming one provider session would
// interleave their turns and overwrite each other's record of them. It is an
// advisory file lock, so a conversation whose holder exited unexpectedly is
// immediately available again. It is per agent rather than per role because two
// agents on one role hold two conversations, and a lease that stopped one of
// them while the other talked would be serializing sessions that never meet.
func (s *ConversationStore) Hold(identity ConversationIdentity) (*Lease, error) {
	return s.take(context.Background(), identity, refuseHeldConversation)
}

// waitingForConversation and refuseHeldConversation are what take does when
// somebody else is mid-turn: queue behind them, or say so and give up.
const (
	waitingForConversation = true
	refuseHeldConversation = false
)

// take acquires the lock on an agent's conversation and stamps this process as
// its holder. It is the one path onto that lock, so the stamp every surface
// reads is written wherever the lock is taken rather than only where it was
// first taken — a turn taken back at the prompt is as visible as the first one.
func (s *ConversationStore) take(ctx context.Context, identity ConversationIdentity, wait bool) (*Lease, error) {
	if err := os.MkdirAll(s.root, 0o700); err != nil {
		return nil, fmt.Errorf("create conversation directory: %w", err)
	}
	path, err := s.leaseFile(identity)
	if err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open conversation lease: %w", err)
	}
	if wait {
		if err := lockStateFile(ctx, file); err != nil {
			file.Close()
			return nil, fmt.Errorf("take up the %s conversation: %w", identity, err)
		}
	} else {
		held, err := tryLockStateFile(file)
		if err != nil {
			file.Close()
			return nil, fmt.Errorf("lock %s conversation: %w", identity, err)
		}
		if !held {
			file.Close()
			return nil, fmt.Errorf("the %s conversation is %w", identity, ErrConversationHeld)
		}
	}
	// The label names what is owned, so a release that failed says which
	// conversation is still held rather than leaving the caller to guess.
	lease := &Lease{label: fmt.Sprintf("%s conversation", identity), file: file}
	holder, err := s.holderFile(identity)
	if err != nil {
		return nil, errors.Join(err, lease.Release())
	}
	// The stamp is written under the lock, so what a reader sees was written by
	// the process that actually owns the conversation. A hold that cannot be
	// stamped is refused rather than taken: what it would otherwise buy is a turn
	// that runs while every surface reports the machine idle, which is the one
	// answer the standing status exists to prevent.
	if err := s.stampHolder(holder); err != nil {
		return nil, errors.Join(err, lease.Release())
	}
	lease.holder = holder
	return lease, nil
}

// InFlight reports whether a process is holding an agent's conversation right
// now, and takes nothing to answer it. Taking the lease and dropping it again
// was the mechanism this replaces: for the instant it lasts it is
// indistinguishable from a second conversation, so a chat or a sink asking for
// its own conversation during a status reading was told another process had it
// — and the four-line status and the hourly heartbeat now ask often enough to
// hit that instant.
//
// What it reads instead is the stamp the holder wrote, which is checked against
// the process named in it rather than trusted. A holder killed outright leaves
// its stamp behind, and the answer has to match the operating system's, which
// dropped that holder's lock as it died.
//
// Two things it cannot see are worth stating. A stamp whose process identifier
// has been reused by an unrelated process reads as held until the next hold
// rewrites it, which is a conversation reported busy rather than a conversation
// anybody is locked out of. And a holder from a build older than the stamp
// wrote none, so its turn reads as free until it ends.
func (s *ConversationStore) InFlight(identity ConversationIdentity) (bool, error) {
	path, err := s.holderFile(identity)
	if err != nil {
		return false, err
	}
	holder, err := s.readHolder(path, identity)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	running, err := processIsRunning(holder.PID)
	if err != nil {
		return false, fmt.Errorf("ask whether the %s conversation's holder is running: %w", identity, err)
	}
	return running, nil
}

// conversationHolder is what a process holding a conversation writes down beside
// the lease so the hold can be observed without being taken.
type conversationHolder struct {
	// PID is the process that took the hold. It is what makes the stamp
	// self-correcting: a holder that exits without releasing leaves the file
	// behind, and a reader that finds no such process reports what the operating
	// system already decided when it dropped the lock.
	PID int `json:"pid"`
	// HeldAt is when the hold was taken. Nothing decides from it — how long a turn
	// has been going is read from the conversation record — and it is written so
	// that a state directory somebody is reading by hand says when.
	HeldAt time.Time `json:"held_at"`
}

// stampHolder writes this process's stamp for a conversation it now holds. It is
// replaced by rename rather than written in place, so a reader sees the whole of
// one stamp or none of it and never half of one.
func (s *ConversationStore) stampHolder(path string) error {
	temporary, err := os.CreateTemp(s.root, ".holder-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary conversation holder: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return fmt.Errorf("secure temporary conversation holder: %w", err)
	}
	holder := conversationHolder{PID: os.Getpid(), HeldAt: time.Now().UTC()}
	if err := writeJSONFile(temporary, "conversation holder", holder); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary conversation holder: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("replace conversation holder: %w", err)
	}
	return syncDirectory(s.root)
}

// readHolder is one stamp as it sits on disk. A file that is not there is
// reported as ErrNotExist for the caller to read as an unheld conversation; a
// file that is there and will not decode is a failure to answer, because a
// reader that guessed at it would be inventing whether somebody is mid-turn.
func (s *ConversationStore) readHolder(path string, identity ConversationIdentity) (conversationHolder, error) {
	file, err := os.Open(path)
	if err != nil {
		return conversationHolder{}, fmt.Errorf("open the %s conversation holder: %w", identity, err)
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, maxEncodedStateBytes))
	decoder.DisallowUnknownFields()
	var holder conversationHolder
	if err := decoder.Decode(&holder); err != nil {
		return conversationHolder{}, fmt.Errorf("decode the %s conversation holder: %w", identity, err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return conversationHolder{}, fmt.Errorf("decode the %s conversation holder: %w", identity, err)
	}
	if holder.PID <= 0 {
		return conversationHolder{}, fmt.Errorf("the %s conversation holder names no process", identity)
	}
	return holder, nil
}

// ConversationHold is a claim on an agent's conversation that its holder may
// put down and take up again. The plain lease above is right for a process that
// owns a conversation from end to end, and wrong for the operator's console:
// that process spends nearly all of its life waiting at a prompt, and while it
// waits nobody is talking to the agent at all. Holding throughout made an idle
// window the reason the harness and the operator's own assistant could not
// reach the product manager until the operator closed it.
//
// What has to be exclusive is a turn rather than a session — two processes
// resuming one provider session would interleave their turns and overwrite each
// other's record of them — so that is the span this is held for. It is the same
// advisory file lock underneath, so a holder that exits unexpectedly still
// leaves the conversation immediately available.
type ConversationHold struct {
	store    *ConversationStore
	identity ConversationIdentity
	// lease is nil exactly while the conversation is put down.
	lease *Lease
}

// Claim takes an agent's conversation and returns the hold that can put it down
// and take it up again. It queues behind whoever is mid-turn rather than
// refusing: what is exclusive is a turn, and a turn is something that ends on
// its own, so a caller told to go away would be told to come back for something
// that was over by the time it was said. That is also what the supervision
// design settles for conversation concurrency — one conversation serializes its
// turns and exposes queueing rather than interleaving. A caller that would
// rather not wait cancels its context, and a caller that wants the question
// answered without taking anything asks InFlight.
func (s *ConversationStore) Claim(ctx context.Context, identity ConversationIdentity) (*ConversationHold, error) {
	lease, err := s.take(ctx, identity, waitingForConversation)
	if err != nil {
		return nil, err
	}
	return &ConversationHold{store: s, identity: identity, lease: lease}, nil
}

// TryClaim is Claim for a caller that has something better to do than wait. It
// refuses with ErrConversationHeld while somebody is mid-turn, which is what a
// background delivery wants: nothing has been asked of the agent yet, so the
// attempt is given back and a later pass makes it rather than the delivery
// holding its lease and its budget open for the length of somebody else's turn.
func (s *ConversationStore) TryClaim(identity ConversationIdentity) (*ConversationHold, error) {
	lease, err := s.Hold(identity)
	if err != nil {
		return nil, err
	}
	return &ConversationHold{store: s, identity: identity, lease: lease}, nil
}

// Release puts the conversation down. Releasing one that is already down is a
// no-op, and it has to be: the process that owns a conversation defers this for
// the life of the command while the conversation is put down and taken up many
// times underneath, so the deferred release routinely runs against a hold that
// is already down — every conversation that ends at the prompt is one.
//
// The absent lease is answered here rather than left to the lease's own
// nil-receiver guard. That guard makes this safe today, and a hold that depends
// on how a type it merely refers to handles being nil is one sentence away from
// not being safe tomorrow.
func (h *ConversationHold) Release() error {
	if h == nil || h.lease == nil {
		return nil
	}
	lease := h.lease
	h.lease = nil
	return lease.Release()
}

// Held reports whether this process currently has the conversation.
func (h *ConversationHold) Held() bool {
	return h != nil && h.lease != nil
}

// Retake takes the conversation up again, waiting for whoever has it rather
// than refusing — the same queueing the first claim does, for the same reason:
// the operator has already typed, there is nothing else for them to do with
// what they said, and another process's turn ends on its own. Taking up a
// conversation this process already has is a no-op.
func (h *ConversationHold) Retake(ctx context.Context) error {
	if h == nil {
		return errors.New("there is no conversation hold to take up")
	}
	if h.lease != nil {
		return nil
	}
	lease, err := h.store.take(ctx, h.identity, waitingForConversation)
	if err != nil {
		return err
	}
	h.lease = lease
	return nil
}

// Load returns the conversation recorded for an agent, reporting
// ErrNoConversation when there is none to resume.
func (s *ConversationStore) Load(identity ConversationIdentity) (Conversation, error) {
	role := identity.Role
	path, err := s.statePathFor(identity)
	if err != nil {
		return Conversation{}, err
	}
	conversation, err := s.read(path, string(role))
	if err != nil {
		return Conversation{}, err
	}
	if conversation.Role != role {
		return Conversation{}, fmt.Errorf("conversation state for %s belongs to role %s", identity, conversation.Role)
	}
	// A record that names its agent must be the agent that was asked for. It
	// cannot be another one under the default layout, where the file is named for
	// the agent, and it is checked anyway: a record whose agent and file disagree
	// is a record somebody moved, and resuming it would put one agent's session
	// behind another's persona.
	if conversation.Agent != "" && conversation.Agent != identity.Agent {
		return Conversation{}, fmt.Errorf("conversation state for %s belongs to agent %s", identity, conversation.Agent)
	}
	return conversation, nil
}

// read is one conversation record as it sits on disk, checked as far as the
// record itself can be checked. Who it belongs to is the caller's question: an
// agent resuming its own conversation and a reader listing every conversation
// there is ask it differently, and neither can answer it from the file alone.
// The label names the record in a failure, because a reader that cannot say
// which file would not decode has been told nothing useful.
func (s *ConversationStore) read(path, label string) (Conversation, error) {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return Conversation{}, ErrNoConversation
	}
	if err != nil {
		return Conversation{}, fmt.Errorf("open conversation state: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return Conversation{}, fmt.Errorf("stat conversation state: %w", err)
	}
	if info.Size() > maxEncodedStateBytes {
		return Conversation{}, fmt.Errorf("conversation state for %s is %d bytes, limit is %d", label, info.Size(), maxEncodedStateBytes)
	}
	decoder := json.NewDecoder(io.LimitReader(file, maxEncodedStateBytes))
	decoder.DisallowUnknownFields()
	var conversation Conversation
	if err := decoder.Decode(&conversation); err != nil {
		return Conversation{}, fmt.Errorf("decode conversation state for %s: %w", label, err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return Conversation{}, fmt.Errorf("decode conversation state for %s: %w", label, err)
	}
	if err := s.validateConversation(conversation); err != nil {
		return Conversation{}, err
	}
	return conversation, nil
}

// Recorded lists the conversation this product holds a record of for each of
// its agents. It is for a reader that has to notice change rather than act on
// it — the reporting sink is the first — and it decides nothing about what it
// reads: a conversation a live process is holding is listed exactly as an idle
// one is.
//
// What it lists is one conversation per agent, because that is what the records
// name. An agent whose conversation was replaced keeps the replaced one's event
// log on disk, and nothing here points at it any more: a reader that was away
// while a conversation was replaced misses whatever it had not already read of
// it. That is the same trade the watermark makes — the durable records are
// authoritative and this is a view of them — and it is stated rather than hidden
// because a gap somebody does not know about is worse than one they do.
func (s *ConversationStore) Recorded() ([]Conversation, error) {
	entries, err := os.ReadDir(s.root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read conversation directory: %w", err)
	}
	conversations := make([]Conversation, 0, len(entries))
	for _, entry := range entries {
		// The leases, the event logs, and the temporary files of a save in flight
		// all live in this directory; only a file named for an agent holds a
		// conversation.
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		conversation, err := s.read(filepath.Join(s.root, entry.Name()), entry.Name())
		if err != nil {
			return nil, fmt.Errorf("discover recorded conversations: %w", err)
		}
		conversations = append(conversations, conversation)
	}
	sort.Slice(conversations, func(i, j int) bool {
		return conversations[i].ConversationID < conversations[j].ConversationID
	})
	return conversations, nil
}

// ClaimRefusalWakeup marks a conversation's outstanding tracker refusal as one
// the harness has started a turn for, and returns the refusal it claimed.
//
// It is the claim rather than the wakeup, and the order is the order the
// guarantee needs: the record says a turn was started before one is, so a
// process that dies between the two has recorded a wakeup nobody made rather
// than made one nobody recorded. The second is what would fire again on the next
// pass, and again on the one after, which is the looping the refusal is escalated
// rather than repeated to avoid.
//
// It is taken under the conversation's own lease, so the claim and the turn that
// follows it cannot be interleaved with somebody else's turn writing the record
// back without it. A conversation somebody is mid-turn with refuses with
// ErrConversationHeld rather than waiting: nothing has been asked of the role
// yet, so a later pass makes the wakeup rather than this one holding a lease open
// for the length of another turn — and a role that is mid-turn is one whose
// refusal may be about to be answered anyway.
func (s *ConversationStore) ClaimRefusalWakeup(identity ConversationIdentity, at time.Time) (TrackerRefusal, error) {
	hold, err := s.TryClaim(identity)
	if err != nil {
		return TrackerRefusal{}, err
	}
	defer hold.Release()
	// Read again under the lease. What was listed is what the record said before
	// the claim, and the answer that matters is what it says now.
	conversation, err := s.Load(identity)
	if err != nil {
		return TrackerRefusal{}, err
	}
	if conversation.RefusedBlock == nil || !conversation.RefusedBlock.AwaitingWakeup() {
		return TrackerRefusal{}, fmt.Errorf("%w: %s", ErrNoRefusalAwaitingWakeup, identity)
	}
	claimed := *conversation.RefusedBlock
	claimed.WokenAt = at.UTC()
	conversation.RefusedBlock = &claimed
	// The record moved, so it says when — but only forward. A claim taken against
	// a clock behind the conversation's own last turn must not rewrite the record
	// as older than the turn that wrote it.
	if claimed.WokenAt.After(conversation.UpdatedAt) {
		conversation.UpdatedAt = claimed.WokenAt
	}
	if err := s.Save(conversation); err != nil {
		return TrackerRefusal{}, err
	}
	return claimed, nil
}

// Save replaces a role's conversation record atomically. Unlike a run, a
// conversation is created and updated through the same call: every turn
// rewrites the same record, and the first turn is not a special case.
func (s *ConversationStore) Save(conversation Conversation) error {
	if err := s.validateConversation(conversation); err != nil {
		return err
	}
	if err := os.MkdirAll(s.root, 0o700); err != nil {
		return fmt.Errorf("create conversation directory: %w", err)
	}
	path, err := s.statePathFor(conversation.Identity())
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(s.root, ".conversation-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary conversation state: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return fmt.Errorf("secure temporary conversation state: %w", err)
	}
	if err := writeJSONFile(temporary, "conversation state", conversation); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary conversation state: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("replace conversation state: %w", err)
	}
	return syncDirectory(s.root)
}

// AppendEvent persists one normalized event from a conversation. The log is
// named for the conversation rather than the role, so starting a new
// conversation never appends to the record of the one it replaced.
func (s *ConversationStore) AppendEvent(event execution.Event) error {
	if err := event.Validate(); err != nil {
		return err
	}
	path, err := s.eventPathForConversation(event.RunID)
	if err != nil {
		return err
	}
	encoded, err := encodeEvent(event)
	if err != nil {
		return err
	}
	if len(encoded) > maxEncodedEventBytes {
		return fmt.Errorf("encoded event is %d bytes, limit is %d", len(encoded), maxEncodedEventBytes)
	}
	if err := os.MkdirAll(s.root, 0o700); err != nil {
		return fmt.Errorf("create conversation directory: %w", err)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open conversation event log: %w", err)
	}
	written, err := file.Write(encoded)
	if err != nil {
		file.Close()
		return fmt.Errorf("append conversation event: %w", err)
	}
	if written != len(encoded) {
		file.Close()
		return fmt.Errorf("append conversation event: %w", io.ErrShortWrite)
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return fmt.Errorf("sync conversation event log: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close conversation event log: %w", err)
	}
	return nil
}

// LoadEvents returns one conversation's normalized events in the order they
// were recorded.
func (s *ConversationStore) LoadEvents(conversationID string) ([]execution.Event, error) {
	path, err := s.eventPathForConversation(conversationID)
	if err != nil {
		return nil, err
	}
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open conversation event log: %w", err)
	}
	defer file.Close()

	var events []execution.Event
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), maxEncodedEventBytes)
	for scanner.Scan() {
		event, err := execution.DecodeEvent(scanner.Bytes())
		if err != nil {
			return nil, fmt.Errorf("decode conversation event log for %s: %w", conversationID, err)
		}
		if event.RunID != conversationID {
			return nil, fmt.Errorf("decode conversation event log for %s: event belongs to %s", conversationID, event.RunID)
		}
		events = append(events, event)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read conversation event log: %w", err)
	}
	return events, nil
}

// identity is where a record belongs. A record written before the agent was part
// of the identity has none, and it belongs where it has always been: under the
// agent named for its role, which is the only agent that could have written it.
// Identity names the conversation this record is: the agent that holds it and
// the role whose authority it carries. It is exported because a reader that
// listed the records — the wakeup a refused tracker block is owed is the first —
// has the record and needs the identity every other call here is keyed by.
//
// A record written before the agent was part of the identity names none, and such
// a record was necessarily written for the agent named after its role, because
// nothing else could have addressed it.
func (c Conversation) Identity() ConversationIdentity {
	agent := c.Agent
	if agent == "" {
		agent = string(c.Role)
	}
	return ConversationIdentity{Agent: agent, Role: c.Role}
}

func (s *ConversationStore) validateConversation(conversation Conversation) error {
	if conversation.ProductID != s.productID {
		return fmt.Errorf("conversation product %q does not match store product %q", conversation.ProductID, s.productID)
	}
	return conversation.Validate()
}

// statePathFor names the one file an agent's conversation lives in. The agent is
// validated as an identifier before it reaches a path, so a configured agent
// name can never escape the conversation directory.
func (s *ConversationStore) statePathFor(identity ConversationIdentity) (string, error) {
	if err := identity.validate(); err != nil {
		return "", err
	}
	return filepath.Join(s.root, identity.Agent+".json"), nil
}

func (s *ConversationStore) eventPathForConversation(conversationID string) (string, error) {
	if !conversationIDPattern.MatchString(conversationID) {
		return "", errors.New("conversation id is invalid")
	}
	return filepath.Join(s.root, conversationID+".events.jsonl"), nil
}

func (s *ConversationStore) leaseFile(identity ConversationIdentity) (string, error) {
	if err := identity.validate(); err != nil {
		return "", err
	}
	return filepath.Join(s.root, identity.Agent+".lease"), nil
}

// holderFile names the stamp beside an agent's lease. It is not a `.json` file,
// so what Recorded lists stays the conversations themselves.
func (s *ConversationStore) holderFile(identity ConversationIdentity) (string, error) {
	if err := identity.validate(); err != nil {
		return "", err
	}
	return filepath.Join(s.root, identity.Agent+".holder"), nil
}
