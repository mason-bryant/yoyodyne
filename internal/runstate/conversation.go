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
	"time"

	"yoyodyne/internal/domain"
	"yoyodyne/internal/execution"
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
	Role           domain.AgentRole `json:"role"`
	Backend        domain.Backend   `json:"backend"`
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
	LastRunWorkItemID string    `json:"last_run_work_item_id,omitempty"`
	LastSequence      uint64    `json:"last_sequence"`
	StartedAt         time.Time `json:"started_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

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

// ConversationStore keeps conversations in the same operating-system state root
// as runs, beside them rather than among them. A conversation is stored under
// the role it belongs to, so a restarted process finds the one conversation it
// should resume without searching for it.
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

// Hold takes exclusive ownership of a role's conversation for as long as this
// process is talking to it. Two processes resuming one provider session would
// interleave their turns and overwrite each other's record of them. It is an
// advisory file lock, so a conversation whose holder exited unexpectedly is
// immediately available again.
func (s *ConversationStore) Hold(role domain.AgentRole) (*Lease, error) {
	if err := os.MkdirAll(s.root, 0o700); err != nil {
		return nil, fmt.Errorf("create conversation directory: %w", err)
	}
	path, err := s.leaseFile(role)
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
		return nil, fmt.Errorf("lock %s conversation: %w", role, err)
	}
	if !held {
		file.Close()
		return nil, fmt.Errorf("the %s conversation is already held by another process", role)
	}
	return &Lease{file: file}, nil
}

// Load returns the conversation recorded for a role, reporting
// ErrNoConversation when there is none to resume.
func (s *ConversationStore) Load(role domain.AgentRole) (Conversation, error) {
	path, err := s.statePathForRole(role)
	if err != nil {
		return Conversation{}, err
	}
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
		return Conversation{}, fmt.Errorf("conversation state for %s is %d bytes, limit is %d", role, info.Size(), maxEncodedStateBytes)
	}
	decoder := json.NewDecoder(io.LimitReader(file, maxEncodedStateBytes))
	decoder.DisallowUnknownFields()
	var conversation Conversation
	if err := decoder.Decode(&conversation); err != nil {
		return Conversation{}, fmt.Errorf("decode conversation state for %s: %w", role, err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return Conversation{}, fmt.Errorf("decode conversation state for %s: %w", role, err)
	}
	if conversation.Role != role {
		return Conversation{}, fmt.Errorf("conversation state for %s belongs to role %s", role, conversation.Role)
	}
	if err := s.validateConversation(conversation); err != nil {
		return Conversation{}, err
	}
	return conversation, nil
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
	path, err := s.statePathForRole(conversation.Role)
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

func (s *ConversationStore) validateConversation(conversation Conversation) error {
	if conversation.ProductID != s.productID {
		return fmt.Errorf("conversation product %q does not match store product %q", conversation.ProductID, s.productID)
	}
	return conversation.Validate()
}

// statePathForRole names the one file a role's conversation lives in. The role
// is validated as an identifier before it reaches a path, so a configured role
// name can never escape the conversation directory.
func (s *ConversationStore) statePathForRole(role domain.AgentRole) (string, error) {
	if err := domain.ValidateIdentifier("role", string(role)); err != nil {
		return "", err
	}
	return filepath.Join(s.root, string(role)+".json"), nil
}

func (s *ConversationStore) eventPathForConversation(conversationID string) (string, error) {
	if !conversationIDPattern.MatchString(conversationID) {
		return "", errors.New("conversation id is invalid")
	}
	return filepath.Join(s.root, conversationID+".events.jsonl"), nil
}

func (s *ConversationStore) leaseFile(role domain.AgentRole) (string, error) {
	if err := domain.ValidateIdentifier("role", string(role)); err != nil {
		return "", err
	}
	return filepath.Join(s.root, string(role)+".lease"), nil
}
