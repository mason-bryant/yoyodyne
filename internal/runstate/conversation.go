package runstate

import (
	"bufio"
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
	Turns                 int    `json:"turns"`
	// PendingTrackerResults is what an agent did to the work tracker and has not
	// been told the result of yet, already rendered as the text its next turn is
	// given. It is durable for the same reason the provider session is: the agent
	// acted, the process that watched it act may be gone, and an agent that never
	// learns what its own actions did is one that will describe them wrongly.
	PendingTrackerResults string `json:"pending_tracker_results,omitempty"`
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
	// PendingWrites are the documents an owning role wrote that the operator has
	// not decided about yet. They are durable for the reason the proposals above
	// are, and the reason is the same one sharpened again: a document written by
	// one `--message` invocation is approved by another, and a process that could
	// not read back what was written had nothing for the approval to name — so
	// the drafted document went back to being something a person transcribed by
	// hand, which is the seam this record exists to close.
	//
	// It holds only the undecided ones. The write itself is an event in the log
	// and stays there; what is kept here is what a later process may still be
	// asked to carry out.
	PendingWrites []PendingWrite `json:"pending_writes,omitempty"`
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

// PendingWrite is one document an owning role wrote, as the record keeps it:
// what would be done to which document, the document itself, and which turn
// wrote it. The conversation it belongs to is the record it sits in, so it is
// not repeated here.
//
// It is declared in this package rather than shared with the conversation code
// that builds it, for the reason the pending proposal beside it is: that code
// already depends on this one and the dependency may not run both ways. The
// field names are the ones the write contract uses, so what is written here
// reads as what the role wrote.
type PendingWrite struct {
	ID   string `json:"id"`
	Turn int    `json:"turn"`
	// Action is "create" or "revise", kept as text because this record says what
	// was waiting rather than deciding what is legal; what is legal is the
	// artifact package's, and it judges this again when the write is carried out.
	Action    string   `json:"action"`
	Artifact  string   `json:"artifact"`
	Kind      string   `json:"kind,omitempty"`
	Title     string   `json:"title,omitempty"`
	Supports  []string `json:"supports,omitempty"`
	Directory string   `json:"directory,omitempty"`
	Body      string   `json:"body"`
	Reason    string   `json:"reason"`
}

// MaxPendingWrites bounds the undecided documents one conversation carries, and
// MaxPendingWriteBytes bounds one of them. A document is the largest thing this
// record holds — a whole Markdown file, not a description of one — so the count
// is small where the proposals' is twenty: a conversation with two documents
// waiting on the operator has already asked them for more than anybody answers
// in one sitting, and the state file has to stay well inside its own limit.
//
// The byte bound matches what the write contract accepts, so a document the
// harness took from a reply is never one it then cannot write down.
const (
	MaxPendingWrites     = 2
	MaxPendingWriteBytes = 64 << 10
)

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
	if len(c.PendingTrackerResults) > MaxPendingTrackerResultBytes {
		problems = append(problems, fmt.Errorf("pending tracker results are %d bytes, limit is %d",
			len(c.PendingTrackerResults), MaxPendingTrackerResultBytes))
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
	if len(c.PendingWrites) > MaxPendingWrites {
		problems = append(problems, fmt.Errorf("%d undecided documents are recorded, limit is %d",
			len(c.PendingWrites), MaxPendingWrites))
	}
	for i, write := range c.PendingWrites {
		// An undecided document nobody can name is one nobody can approve, and one
		// with nothing under it is an approval that would write an empty file.
		if strings.TrimSpace(write.ID) == "" {
			problems = append(problems, fmt.Errorf("pending_writes[%d] has no id", i))
		}
		if strings.TrimSpace(write.Body) == "" {
			problems = append(problems, fmt.Errorf("pending_writes[%d] carries no document", i))
		}
		if len(write.Body) > MaxPendingWriteBytes {
			problems = append(problems, fmt.Errorf("pending_writes[%d] is %d bytes, limit is %d",
				i, len(write.Body), MaxPendingWriteBytes))
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
	held, err := tryLockStateFile(file)
	if err != nil {
		file.Close()
		return nil, fmt.Errorf("lock %s conversation: %w", identity, err)
	}
	if !held {
		file.Close()
		return nil, fmt.Errorf("the %s conversation is %w", identity, ErrConversationHeld)
	}
	return &Lease{file: file}, nil
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
	path, err := s.statePathFor(conversation.identity())
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
func (c Conversation) identity() ConversationIdentity {
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
