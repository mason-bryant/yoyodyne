package runstate

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// An agent's memory, as the `### Agent memory` section of
// `docs/designs/configurable-workflows.md` specifies it (the revision dated
// 2026-09-05T17:40:00Z). Four sentences of that section are the whole of what
// this file is:
//
//   - Memory is the agent-context mechanism rather than a new concept beside it:
//     continuity modes, typed stores, append-only revisions, and compaction that
//     keeps provenance. So a memory here is a named thing an agent knows, held as
//     the revisions that produced it, and a compaction is one more revision that
//     names what it replaced rather than an edit that removes it.
//   - No fourth store. The durable conversation stays the interaction record and
//     the tracker note stays the work record; this is a derived store that may
//     reference either and copies neither, which is why a source is an identifier
//     and never text lifted out of one. A memory kept inside a conversation record
//     is refused, and it is refused by that record's own decoder, which reads
//     nothing it does not declare.
//   - It is budgeted, redacted, and audited, because it is agent-authored durable
//     state that enters every later turn. The budget is below; the redaction
//     happens here rather than at each caller, so that nothing reaches the disk
//     that did not pass it; and the audit is the invocation every revision carries.
//
// What backs it is one append-only log file per agent under the state root,
// rolled aside when it grows: the shape the design's fifteenth settled question
// describes, in the format the rest of this package's durable state already uses.
//
// The design also says the store is db-backed, with bolt named as an acceptable
// option, and this is not that. It is not that because a developer run cannot
// make it that: this repository depends on one module, an embedded database is a
// second, and a developer run's sandbox reaches no module proxy — `go get
// go.etcd.io/bbolt` is refused before it resolves a version, and nothing in the
// local module cache is an embedded database. A change adding one would not
// build in the worktree that has to verify it. Whether a database is required
// here or whether this satisfies the ruling is the architect's to settle rather
// than this comment's, and it has been put to them; what makes the answer cheap
// either way is that a revision is already the unit a store would hold, so a
// database would replace the three functions at the foot of this file — the read,
// the append, and the roll — and nothing above them.
//
// It lives beside the conversations rather than in a package of its own for the
// consolidation the design requires: the two are the same state root, the same
// durable-write primitives, and the same identity, and a second store package
// would be the thing the design says not to build.

// MemorySchemaVersion is versioned independently of the run state and of the
// conversation beside it, because a memory is neither: it has no worktree and no
// turns, and it outlives every conversation that contributed to it.
const MemorySchemaVersion = 1

// MemoryContinuity is how far one store reaches — the design's continuity modes,
// minus the one that stores nothing.
//
// `invocation` continuity is the third mode the design names and it is
// deliberately absent here: what one invocation knows dies with it, so a store
// for it would be a file that is written and never read. An agent whose context
// policy is `invocation` writes no memory at all rather than writing a memory
// nothing may reach.
type MemoryContinuity string

const (
	// MemoryContinuityAgent is what the agent knows wherever it is invoked: how the
	// operator reads a report, what this project's checks are like, which mistakes
	// this agent has already made. It is the mode that makes a personality.
	MemoryContinuityAgent MemoryContinuity = "agent"
	// MemoryContinuitySubject is what the agent knows about one thing — a work
	// item, a document, a branch. It is kept apart from the agent's own knowledge
	// because it stops being worth carrying when its subject is finished, and
	// because an agent that mixed the two would remember one item's peculiarities
	// as though they were the project's.
	MemoryContinuitySubject MemoryContinuity = "subject"
)

func (c MemoryContinuity) Valid() bool {
	return c == MemoryContinuityAgent || c == MemoryContinuitySubject
}

// The budget the design requires, in three bounds rather than one, because the
// two things being bounded are different and a single number would have to be
// wrong for one of them.
//
// MaxMemoryLiveBytes is the budget proper: the text of everything an agent still
// knows, which is what enters every later turn. It is small on purpose. Memory
// competes for the same context as the canonical artifacts and the work item, and
// an agent that remembered a hundred kilobytes of itself would be an agent whose
// prompt is mostly its own opinions.
//
// MaxMemoryTextBytes bounds one revision, so no single write can consume the
// budget in one go, and so a runaway generation is refused rather than stored.
//
// MaxMemoryLogBytes is where the live log is rolled aside, which the live budget
// does not do for it: the log keeps every superseded revision, because that
// history is what makes the store auditable, so it grows where the live set does
// not. It is generous — several hundred revisions at their own maximum.
//
// It is the point at which the store rolls rather than the point at which it
// refuses. A wall here would be one an agent could not climb back over: the way
// out of a full store is to compact or to retire something, both of which are
// writes on this same path, so a size that refused every write would refuse the
// two that make room and leave the store permanently unable to record anything —
// including its own tidying up. Rolling costs nothing an operator has to do and
// loses nothing: the superseded revisions move to a numbered archive beside the
// log, the reader takes the archives and the log together, and the history reads
// as one sequence exactly as it did before.
const (
	MaxMemoryLiveBytes = 32 << 10
	MaxMemoryTextBytes = 8 << 10
	MaxMemoryLogBytes  = 4 << 20
)

// MaxMemorySources bounds what one revision may cite, and MaxMemorySubjectBytes
// what a subject may be named by. Both exist so that a record stays a record: a
// revision citing a thousand conversations is a copy of the interaction log by
// another route, which is the thing the design refuses.
const (
	MaxMemorySources      = 16
	MaxMemorySubjectBytes = 200
)

// maxEncodedMemoryRevisionBytes bounds one encoded line, including the newline
// the encoder writes. The writer and the reader share it, so a revision that was
// written is always one that can be read back.
const maxEncodedMemoryRevisionBytes = 1 << 20

// MemorySourceKind is what a memory may point at. Each is a durable record this
// harness already keeps, which is the point: a source is a reference somebody can
// follow, and a memory that could cite anything would be a memory that cites
// prose nobody can check.
type MemorySourceKind string

const (
	MemorySourceConversation MemorySourceKind = "conversation"
	MemorySourceRun          MemorySourceKind = "run"
	MemorySourceWorkItem     MemorySourceKind = "work-item"
)

func (k MemorySourceKind) Valid() bool {
	return k == MemorySourceConversation || k == MemorySourceRun || k == MemorySourceWorkItem
}

// MemorySource is one record a memory was drawn from, by identifier alone.
//
// By identifier alone is the design's rule rather than a space saving. Durable
// conversations are the interaction record and tracker notes are the work record;
// a memory that copied either would be a second copy of it that no correction
// reaches, and the first thing anybody would do with that copy is trust it.
type MemorySource struct {
	Kind MemorySourceKind `json:"kind"`
	ID   string           `json:"id"`
}

// MemoryInvocationKind is which kind of provider invocation wrote a revision. An
// agent is invoked in a conversation or inside a run, and the audit trail has to
// say which, because those are two different things to go and read.
type MemoryInvocationKind string

const (
	MemoryInvocationConversation MemoryInvocationKind = "conversation"
	MemoryInvocationRun          MemoryInvocationKind = "run"
)

func (k MemoryInvocationKind) Valid() bool {
	return k == MemoryInvocationConversation || k == MemoryInvocationRun
}

// MemoryInvocation is the audit the design requires: the invocation that produced
// one revision, recorded on the revision itself.
//
// It pins the backend, the model, the account, and the configuration in force,
// which is what `durable-state-is-provider-independent` demands of every provider
// invocation the harness records. A memory is the sharpest case for it: what an
// agent believes about this project is a thing somebody may one day have to
// explain, and "some earlier version of this agent decided so" is not an
// explanation.
//
// The last three are absent from a record written where the harness did not know
// them, so what is checked is the shape of one that is there rather than that it
// is there at all — the same reading the conversation record beside this makes,
// and for the same reason: a field naming an account nothing could have produced
// says less than an empty one, because it reads as evidence.
type MemoryInvocation struct {
	Kind MemoryInvocationKind `json:"kind"`
	// ID is the conversation or the run, and it is held to that record's own
	// identifier shape, so a citation always names something that could exist.
	ID string `json:"id"`
	// Turn is which turn of a conversation wrote this, and is absent inside a run,
	// which has no turns to count.
	Turn    int            `json:"turn,omitempty"`
	Backend domain.Backend `json:"backend"`
	// Model is the selector the invocation asked for and ResolvedModel what the
	// provider reported serving it, because a floating family alias makes the
	// resolved identifier the only real record of what answered.
	Model          string `json:"model"`
	ResolvedModel  string `json:"resolved_model,omitempty"`
	AccountAlias   string `json:"account_alias,omitempty"`
	ConfigRevision string `json:"config_revision,omitempty"`
	Build          string `json:"build,omitempty"`
}

func (i MemoryInvocation) validate() error {
	var problems []error
	if !i.Kind.Valid() {
		problems = append(problems, fmt.Errorf("invocation kind %q is not one this harness records", i.Kind))
	}
	switch i.Kind {
	case MemoryInvocationConversation:
		if !conversationIDPattern.MatchString(i.ID) {
			problems = append(problems, errors.New("the invocation names no conversation"))
		}
		if i.Turn < 1 {
			problems = append(problems, errors.New("a conversation turn is numbered from one"))
		}
	case MemoryInvocationRun:
		if !runIDPattern.MatchString(i.ID) {
			problems = append(problems, errors.New("the invocation names no run"))
		}
		if i.Turn != 0 {
			problems = append(problems, errors.New("a run has no turns to number"))
		}
	}
	if !i.Backend.Valid() {
		problems = append(problems, errors.New("the invocation names no backend"))
	}
	if strings.TrimSpace(i.Model) == "" {
		problems = append(problems, errors.New("the invocation names no model"))
	}
	if i.AccountAlias != "" && !accountAliasPattern.MatchString(i.AccountAlias) {
		problems = append(problems, errors.New("account_alias is not an account alias"))
	}
	if i.ConfigRevision != "" && !configRevisionPattern.MatchString(i.ConfigRevision) {
		problems = append(problems, errors.New("config_revision is not a configuration revision"))
	}
	if i.Build != "" && !buildPattern.MatchString(i.Build) {
		problems = append(problems, errors.New("build is not a revision"))
	}
	return errors.Join(problems...)
}

// MemoryRevision is one durable revision of one memory: what the agent knows,
// what it was drawn from, what it replaced, and which invocation wrote it.
//
// The log is append-only, so nothing here is ever edited. A memory that changed
// gets another revision; a memory that was folded into a smaller one gets a
// revision naming the revisions it compacts; a memory that stopped being true
// gets a retired revision. Every one of those is still readable afterwards, which
// is what makes the store auditable rather than merely current.
type MemoryRevision struct {
	SchemaVersion int              `json:"schema_version"`
	ProductID     domain.ProductID `json:"product_id"`
	// Agent is whose memory this is and Role the authority its writing carried.
	// They are two fields for the reason the conversation record beside this keeps
	// two: a project may configure two agents for one role, and those are two
	// identities that must not read each other's memory. The role is recorded
	// rather than derived because it is the audit's other half — what a memory was
	// written under stays true even if the agent is later configured differently.
	Agent string           `json:"agent"`
	Role  domain.AgentRole `json:"role"`
	// Memory is what this memory is called, within the agent's own store. It is an
	// ordinary identifier because it is read by a person: a memory an operator
	// cannot name is one they cannot ask about.
	Memory string `json:"memory"`
	// Sequence is where this revision falls in its memory's history, from one. It
	// is assigned by the store rather than supplied, so two writers cannot agree on
	// a number that means two different things.
	Sequence   int              `json:"sequence"`
	Continuity MemoryContinuity `json:"continuity"`
	// Subject is what a subject-continuity memory is about, and is empty for the
	// agent's own knowledge. It is free text held to one line, because what it
	// names is not always a work item — a document and a branch are subjects too.
	Subject string `json:"subject,omitempty"`
	// Text is the memory itself, as it will be given back to the agent. It is
	// redacted before it is stored, so what is on the disk is what may be read.
	Text string `json:"text"`
	// Retired says this memory stopped being true. The revision is still stored and
	// still readable; what changes is that the memory leaves the live set and stops
	// entering later turns.
	Retired bool `json:"retired,omitempty"`
	// Sources are the records this revision was drawn from, by identifier.
	Sources []MemorySource `json:"sources,omitempty"`
	// Compacts are the earlier revisions of this memory that this one summarizes.
	// It is the provenance the design's compaction requires: a compacted memory
	// says so and says what it came from, rather than appearing to have been
	// written once.
	Compacts   []int            `json:"compacts,omitempty"`
	Invocation MemoryInvocation `json:"invocation"`
	RecordedAt time.Time        `json:"recorded_at"`
}

// Compacted reports a revision that folded earlier ones together, which is a
// thing anybody reading the history has to be told rather than left to infer.
func (r MemoryRevision) Compacted() bool { return len(r.Compacts) > 0 }

func (r MemoryRevision) Validate() error {
	var problems []error
	if r.SchemaVersion != MemorySchemaVersion {
		problems = append(problems, fmt.Errorf("schema_version must be %d", MemorySchemaVersion))
	}
	if err := domain.ValidateIdentifier("product id", string(r.ProductID)); err != nil {
		problems = append(problems, err)
	}
	if err := domain.ValidateIdentifier("agent", r.Agent); err != nil {
		problems = append(problems, err)
	}
	if err := domain.ValidateIdentifier("role", string(r.Role)); err != nil {
		problems = append(problems, err)
	}
	if err := domain.ValidateIdentifier("memory", r.Memory); err != nil {
		problems = append(problems, err)
	}
	if r.Sequence < 1 {
		problems = append(problems, errors.New("a revision is numbered from one"))
	}
	if !r.Continuity.Valid() {
		problems = append(problems, fmt.Errorf("continuity %q is not one this store keeps", r.Continuity))
	}
	switch r.Continuity {
	case MemoryContinuityAgent:
		if r.Subject != "" {
			problems = append(problems, errors.New("an agent-continuity memory is about no subject"))
		}
	case MemoryContinuitySubject:
		if err := validateMemorySubject(r.Subject); err != nil {
			problems = append(problems, err)
		}
	}
	// A memory with nothing in it is not a memory. Retiring one is how it is taken
	// out of the live set, and the retiring revision still says what it retired,
	// because a history that goes blank tells the next reader nothing.
	if strings.TrimSpace(r.Text) == "" {
		problems = append(problems, errors.New("a revision records what the agent knows, so its text is required"))
	}
	if len(r.Text) > MaxMemoryTextBytes {
		problems = append(problems, fmt.Errorf("the text is %d bytes, limit is %d", len(r.Text), MaxMemoryTextBytes))
	}
	if len(r.Sources) > MaxMemorySources {
		problems = append(problems, fmt.Errorf("%d sources are cited, limit is %d", len(r.Sources), MaxMemorySources))
	}
	for index, source := range r.Sources {
		if err := source.validate(); err != nil {
			problems = append(problems, fmt.Errorf("sources[%d]: %w", index, err))
		}
	}
	for index, compacted := range r.Compacts {
		if compacted < 1 || compacted >= r.Sequence {
			problems = append(problems, fmt.Errorf("compacts[%d] is revision %d, which is not an earlier revision of this memory", index, compacted))
		}
	}
	if err := r.Invocation.validate(); err != nil {
		problems = append(problems, err)
	}
	if r.RecordedAt.IsZero() {
		problems = append(problems, errors.New("recorded_at is required"))
	}
	if len(problems) > 0 {
		return fmt.Errorf("invalid memory revision: %w", errors.Join(problems...))
	}
	return nil
}

func (s MemorySource) validate() error {
	if !s.Kind.Valid() {
		return fmt.Errorf("source kind %q is not one this harness records", s.Kind)
	}
	switch s.Kind {
	case MemorySourceConversation:
		if !conversationIDPattern.MatchString(s.ID) {
			return errors.New("the source names no conversation")
		}
	case MemorySourceRun:
		if !runIDPattern.MatchString(s.ID) {
			return errors.New("the source names no run")
		}
	case MemorySourceWorkItem:
		if err := validateMemorySubject(s.ID); err != nil {
			return fmt.Errorf("the source names no work item: %w", err)
		}
	}
	return nil
}

// validateMemorySubject holds a subject to one readable line. It never becomes a
// path — every memory an agent has is in the one file named for that agent — so
// what it is held to is legibility rather than safety: a subject with a newline
// in it breaks every listing that prints one.
func validateMemorySubject(subject string) error {
	trimmed := strings.TrimSpace(subject)
	if trimmed == "" {
		return errors.New("a subject-continuity memory says what it is about")
	}
	if len(trimmed) > MaxMemorySubjectBytes {
		return fmt.Errorf("the subject is %d bytes, limit is %d", len(trimmed), MaxMemorySubjectBytes)
	}
	if strings.ContainsFunc(trimmed, func(r rune) bool { return unicode.IsControl(r) }) {
		return errors.New("a subject is one line, so it carries no control characters")
	}
	return nil
}

// Memory is one memory as the store holds it: what it is called, what it is
// about, and every revision behind it, oldest first.
//
// The revisions are the whole of it rather than a current value with a history
// beside it, because the history is what an operator is owed: agent-authored
// durable state that could only be read as it now stands would be state nobody
// can audit.
type Memory struct {
	Name       string
	Continuity MemoryContinuity
	Subject    string
	Revisions  []MemoryRevision
}

// Current is what the agent knows now, which is the last revision recorded. A
// memory with no revisions cannot be built by this package, so the zero value is
// returned only for one somebody assembled by hand.
func (m Memory) Current() MemoryRevision {
	if len(m.Revisions) == 0 {
		return MemoryRevision{}
	}
	return m.Revisions[len(m.Revisions)-1]
}

// Retired reports a memory that has been taken out of the live set. Its history
// stays readable, which is the point of retiring one rather than removing it.
func (m Memory) Retired() bool { return m.Current().Retired }

// Compacted reports a memory whose current revision folded earlier ones together.
func (m Memory) Compacted() bool { return m.Current().Compacted() }

// MemoryProblem is one line of a log that would not decode, reported against the
// agent it belongs to rather than raised as a failure to read anything.
//
// One unreadable record does not fail a listing. An operator asking what an agent
// remembers is owed the answer even where one line is corrupt — and a memory that
// will not parse is itself something they need to see, because agent-authored
// durable state that silently disappeared is the worst of the ways this could
// fail.
type MemoryProblem struct {
	Agent string `json:"agent"`
	// Log is the file the line is in, by name rather than by path. An agent's
	// history is its log and the archives rolled off it, so a line number on its
	// own would send whoever went looking to the wrong file.
	Log     string `json:"log"`
	Line    int    `json:"line"`
	Problem string `json:"problem"`
}

func (p MemoryProblem) String() string {
	return fmt.Sprintf("%s line %d: %s", p.Log, p.Line, p.Problem)
}

// MemoryStore keeps what each agent knows, in the same operating-system state
// root as the runs and the conversations, beside both rather than among either.
// One agent's memories are one append-only log, named for that agent, so the
// store an agent writes to and the store it is briefed from are found without
// searching.
type MemoryStore struct {
	root      string
	productID domain.ProductID
	// redactor is applied to everything an agent authored before it reaches the
	// disk. It is a field of the store rather than an argument to the write so
	// that there is no path onto the disk that skips it.
	redactor execution.Redactor
	// rollAt is the size at which the live log is rolled aside. It is a field only
	// so a test can drive the roll without writing four megabytes to reach it;
	// every store the harness builds gets MaxMemoryLogBytes.
	rollAt int64
}

// NewMemoryStore builds the store for one product. The values are what must not
// be persisted — the same values every other durable record in this harness is
// redacted against — and a store built without any redacts nothing, which is
// what a test and a read-only reader want.
func NewMemoryStore(root string, productID domain.ProductID, redactValues ...string) (*MemoryStore, error) {
	if !filepath.IsAbs(root) {
		return nil, errors.New("state root must be an absolute path")
	}
	if err := domain.ValidateIdentifier("product id", string(productID)); err != nil {
		return nil, err
	}
	return &MemoryStore{
		root:      filepath.Join(filepath.Clean(root), "products", string(productID), "memory"),
		productID: productID,
		redactor:  execution.NewRedactor(redactValues...),
		rollAt:    MaxMemoryLogBytes,
	}, nil
}

func (s *MemoryStore) Root() string { return s.root }

// ErrMemoryBudget reports a write refused for want of room rather than for being
// wrong. It is a sentinel because the two lead to opposite conclusions: a
// malformed revision is a defect in whatever produced it, and a full store is an
// agent that has to compact or retire something before it can remember anything
// more.
var ErrMemoryBudget = errors.New("the memory budget is spent")

// Remember records one revision and returns it as it was stored.
//
// What comes back is what is on the disk rather than what was handed in: the
// text has been redacted and the sequence assigned. A caller that wants to know
// what its agent will read back later has to read this rather than the value it
// passed.
//
// It is the one write path. Compacting and retiring are revisions like any other
// — one naming the revisions it replaces, one marked retired — because a store
// whose history could be reached by three different doors is a store where one
// of the three eventually forgets to record the invocation.
func (s *MemoryStore) Remember(ctx context.Context, revision MemoryRevision) (MemoryRevision, error) {
	if revision.Sequence != 0 {
		return MemoryRevision{}, fmt.Errorf("the store numbers a revision, so %s arrived already numbered %d", revision.Memory, revision.Sequence)
	}
	if revision.ProductID != s.productID {
		return MemoryRevision{}, fmt.Errorf("memory product %q does not match store product %q", revision.ProductID, s.productID)
	}
	// Redaction happens before anything else, so what the bounds are measured
	// against and what the record is validated as are what will actually be
	// written. Redacting afterwards would let a revision pass its byte bound and
	// then grow past it on the way to the disk.
	revision.Text = s.redactor.Redact(revision.Text)
	revision.Subject = s.redactor.Redact(revision.Subject)
	if revision.RecordedAt.IsZero() {
		revision.RecordedAt = time.Now().UTC()
	}
	revision.RecordedAt = revision.RecordedAt.UTC()

	release, err := s.lockAgent(ctx, revision.Agent)
	if err != nil {
		return MemoryRevision{}, err
	}
	defer release()

	path, err := s.logPath(revision.Agent)
	if err != nil {
		return MemoryRevision{}, err
	}
	recorded, problems, err := s.recorded(revision.Agent)
	if err != nil {
		return MemoryRevision{}, err
	}
	// A history with a line nobody can read is a history this refuses to append to.
	// The listing tolerates one because a reader is owed what there is; a writer
	// must not, because the sequence it is about to assign is worked out from what
	// it could read, and a revision it could not read may be the one it is
	// numbering after.
	if len(problems) > 0 {
		return MemoryRevision{}, fmt.Errorf("%s cannot be written while %d of its lines will not decode: %s", revision.Agent, len(problems), problems[0])
	}

	revision.Sequence = nextMemorySequence(recorded, revision.Memory)
	if err := s.checkContinuity(recorded, revision); err != nil {
		return MemoryRevision{}, err
	}
	if err := revision.Validate(); err != nil {
		return MemoryRevision{}, err
	}

	encoded, err := encodeMemoryRevision(revision)
	if err != nil {
		return MemoryRevision{}, err
	}
	if len(encoded) > maxEncodedMemoryRevisionBytes {
		return MemoryRevision{}, fmt.Errorf("the encoded revision is %d bytes, limit is %d", len(encoded), maxEncodedMemoryRevisionBytes)
	}
	if err := s.affordable(recorded, revision); err != nil {
		return MemoryRevision{}, err
	}
	// The roll happens before the append rather than after it, so the log a write
	// lands in is one that had room for it, and so the size that triggers a roll is
	// never a size the log actually reached.
	if err := s.rollIfFull(revision.Agent, path, recorded, len(encoded)); err != nil {
		return MemoryRevision{}, err
	}
	if err := s.append(path, encoded); err != nil {
		return MemoryRevision{}, err
	}
	return revision, nil
}

// checkContinuity refuses a revision that would make one memory two different
// things. A memory's name is how an operator asks about it, so a name that meant
// the agent's own knowledge yesterday and one work item today is a history nobody
// can read.
func (s *MemoryStore) checkContinuity(recorded []MemoryRevision, revision MemoryRevision) error {
	for _, earlier := range recorded {
		if earlier.Memory != revision.Memory {
			continue
		}
		if earlier.Continuity != revision.Continuity || earlier.Subject != revision.Subject {
			return fmt.Errorf("%s is already recorded as %s memory %q and this revision makes it %s memory %q",
				revision.Memory, earlier.Continuity, earlier.Subject, revision.Continuity, revision.Subject)
		}
	}
	// Compacting names revisions of this memory, so a number that belongs to no
	// revision is refused here rather than left as provenance pointing at nothing.
	for _, compacted := range revision.Compacts {
		if !memoryHasSequence(recorded, revision.Memory, compacted) {
			return fmt.Errorf("%s has no revision %d to compact", revision.Memory, compacted)
		}
	}
	return nil
}

// affordable is the budget, asked of the write that is about to happen.
//
// It is the live budget and only the live budget. What an agent knows is what
// enters its later invocations, so that is the thing worth refusing over; how much
// history has accumulated behind it is the roll's business rather than a reason to
// refuse anybody anything.
//
// The cost is measured over what the store would hold afterwards rather than over
// what it holds now, so the refusal names the state the caller was trying to
// reach. Compacting and retiring are what make room, and both are writes, so both
// are measured the same way — a compaction that replaces four revisions with one
// smaller one is affordable exactly when the result fits, and a retirement almost
// always is.
func (s *MemoryStore) affordable(recorded []MemoryRevision, revision MemoryRevision) error {
	live := 0
	for _, memory := range assemble(append(append([]MemoryRevision{}, recorded...), revision)) {
		if memory.Retired() {
			continue
		}
		live += len(memory.Current().Text)
	}
	if live > MaxMemoryLiveBytes {
		return fmt.Errorf("%w: %s would know %d bytes and the budget is %d; compact or retire a memory first",
			ErrMemoryBudget, revision.Agent, live, MaxMemoryLiveBytes)
	}
	return nil
}

// rollIfFull moves an agent's history aside when its live log has grown past the
// size a log is kept to, leaving a log that holds what the agent currently knows
// and an archive that holds everything it used to.
//
// Nothing is lost and nothing is edited: the archive is the log under another
// name, and the fresh log opens with the current revision of each live memory
// copied across exactly as it was written — same number, same text, same
// invocation — so the history a reader assembles is the one that was recorded.
// The reader takes the archives and the log together, and a revision that appears
// in both is counted once.
//
// It declines to roll where rolling would not help: a log whose every line is
// still current has nothing superseded to set aside, and renaming it would leave a
// fresh log the same size as the one it replaced. That case is bounded by the live
// budget instead, which is what actually holds it down.
func (s *MemoryStore) rollIfFull(agent, path string, recorded []MemoryRevision, incoming int) error {
	stored, err := s.logSize(agent, path)
	if err != nil {
		return err
	}
	if stored+int64(incoming) <= s.rollAt {
		return nil
	}
	seed, err := currentMemoryLines(recorded)
	if err != nil {
		return err
	}
	kept := int64(0)
	for _, line := range seed {
		kept += int64(len(line))
	}
	if kept >= stored {
		return nil
	}
	archive, err := s.nextArchivePath(agent)
	if err != nil {
		return err
	}
	if err := os.Rename(path, archive); err != nil {
		return fmt.Errorf("roll the %s memory log aside: %w", agent, err)
	}
	if err := syncDirectory(s.root); err != nil {
		return err
	}
	for _, line := range seed {
		if err := s.append(path, line); err != nil {
			return fmt.Errorf("carry the %s memories into a fresh log: %w", agent, err)
		}
	}
	return nil
}

// currentMemoryLines is the current revision of every memory still live, encoded
// as the lines a fresh log opens with. A retired memory is not carried across: its
// history is in the archive, which is where a retired memory's history belongs.
func currentMemoryLines(recorded []MemoryRevision) ([][]byte, error) {
	var lines [][]byte
	for _, memory := range assemble(recorded) {
		if memory.Retired() {
			continue
		}
		encoded, err := encodeMemoryRevision(memory.Current())
		if err != nil {
			return nil, err
		}
		lines = append(lines, encoded)
	}
	return lines, nil
}

func (s *MemoryStore) logSize(agent, path string) (int64, error) {
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("measure the %s memory log: %w", agent, err)
	}
	return info.Size(), nil
}

// Memories is everything one agent knows and every revision behind it, in the
// order an operator reads it: agent-continuity memories first, then the subject
// ones, each by name. The second return is the lines that would not decode, which
// are reported rather than raised.
//
// It reads the archives rolled off this agent's log as well as the log, so what
// comes back is the whole history whether or not it has ever been rolled.
func (s *MemoryStore) Memories(agent string) ([]Memory, []MemoryProblem, error) {
	recorded, problems, err := s.recorded(agent)
	if err != nil {
		return nil, nil, err
	}
	return assemble(recorded), problems, nil
}

// recorded is every revision this store holds for one agent, oldest file first:
// the archives in the order they were rolled off, and then the live log. It is
// what both the listing and the writer read, so a sequence the writer assigns
// counts the archived revisions too and can never reuse a number that is already
// in the history.
func (s *MemoryStore) recorded(agent string) ([]MemoryRevision, []MemoryProblem, error) {
	path, err := s.logPath(agent)
	if err != nil {
		return nil, nil, err
	}
	archives, err := s.archivePaths(agent)
	if err != nil {
		return nil, nil, err
	}
	var (
		revisions []MemoryRevision
		problems  []MemoryProblem
	)
	for _, each := range append(archives, path) {
		read, found, err := s.read(each, agent)
		if err != nil {
			return nil, nil, err
		}
		revisions = append(revisions, read...)
		problems = append(problems, found...)
	}
	return revisions, problems, nil
}

// Live is what the agent still knows: every memory whose latest revision is not
// retired. It is what a later turn is briefed from, and it is a separate question
// from the listing above because an operator reads the history and an agent reads
// the conclusion.
func (s *MemoryStore) Live(agent string) ([]Memory, []MemoryProblem, error) {
	memories, problems, err := s.Memories(agent)
	if err != nil {
		return nil, nil, err
	}
	live := make([]Memory, 0, len(memories))
	for _, memory := range memories {
		if !memory.Retired() {
			live = append(live, memory)
		}
	}
	return live, problems, nil
}

// Agents is every agent this product holds memories for, in name order. It
// decides nothing about what it finds: an agent whose log is unreadable is still
// an agent that has one.
func (s *MemoryStore) Agents() ([]string, error) {
	entries, err := os.ReadDir(s.root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read the memory directory: %w", err)
	}
	agents := make([]string, 0, len(entries))
	for _, entry := range entries {
		// The locks and the temporary files of a write in flight live in this
		// directory; only a log named for an agent holds memories.
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), memoryLogSuffix) || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		agents = append(agents, strings.TrimSuffix(entry.Name(), memoryLogSuffix))
	}
	sort.Strings(agents)
	return agents, nil
}

// assemble turns a history into memories: one per name, its revisions in the
// order they were recorded, agent-continuity first and then by name. Sorting here
// rather than at each reader is what makes two readings of one history read the
// same.
//
// A revision the roll carried into a fresh log is in the archive it came from as
// well, so a revision already seen is skipped. What identifies one is its memory
// and its number, which the store assigns and never reuses.
func assemble(recorded []MemoryRevision) []Memory {
	byName := map[string]*Memory{}
	seen := map[string]bool{}
	var order []string
	for _, revision := range recorded {
		carried := fmt.Sprintf("%s\x00%d", revision.Memory, revision.Sequence)
		if seen[carried] {
			continue
		}
		seen[carried] = true
		memory, known := byName[revision.Memory]
		if !known {
			memory = &Memory{
				Name:       revision.Memory,
				Continuity: revision.Continuity,
				Subject:    revision.Subject,
			}
			byName[revision.Memory] = memory
			order = append(order, revision.Memory)
		}
		memory.Revisions = append(memory.Revisions, revision)
	}
	memories := make([]Memory, 0, len(order))
	for _, name := range order {
		memory := byName[name]
		sort.SliceStable(memory.Revisions, func(i, j int) bool {
			return memory.Revisions[i].Sequence < memory.Revisions[j].Sequence
		})
		memories = append(memories, *memory)
	}
	sort.SliceStable(memories, func(i, j int) bool {
		if memories[i].Continuity != memories[j].Continuity {
			return memories[i].Continuity == MemoryContinuityAgent
		}
		if memories[i].Subject != memories[j].Subject {
			return memories[i].Subject < memories[j].Subject
		}
		return memories[i].Name < memories[j].Name
	})
	return memories
}

func nextMemorySequence(recorded []MemoryRevision, name string) int {
	next := 1
	for _, revision := range recorded {
		if revision.Memory == name && revision.Sequence >= next {
			next = revision.Sequence + 1
		}
	}
	return next
}

func memoryHasSequence(recorded []MemoryRevision, name string, sequence int) bool {
	for _, revision := range recorded {
		if revision.Memory == name && revision.Sequence == sequence {
			return true
		}
	}
	return false
}

// read is one of an agent's log files as it sits on disk. A line that will not
// decode, or that belongs to another agent or another product, is a problem
// reported against the file it is in rather than a failure to read the rest.
func (s *MemoryStore) read(path, agent string) ([]MemoryRevision, []MemoryProblem, error) {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("open the %s memory log: %w", agent, err)
	}
	defer file.Close()

	log := filepath.Base(path)
	var (
		revisions []MemoryRevision
		problems  []MemoryProblem
	)
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), maxEncodedMemoryRevisionBytes)
	line := 0
	for scanner.Scan() {
		line++
		if strings.TrimSpace(scanner.Text()) == "" {
			continue
		}
		revision, err := decodeMemoryRevision(scanner.Bytes())
		if err != nil {
			problems = append(problems, MemoryProblem{Agent: agent, Log: log, Line: line, Problem: err.Error()})
			continue
		}
		if revision.Agent != agent {
			problems = append(problems, MemoryProblem{Agent: agent, Log: log, Line: line,
				Problem: fmt.Sprintf("the revision belongs to agent %s", revision.Agent)})
			continue
		}
		if revision.ProductID != s.productID {
			problems = append(problems, MemoryProblem{Agent: agent, Log: log, Line: line,
				Problem: fmt.Sprintf("the revision belongs to product %s", revision.ProductID)})
			continue
		}
		revisions = append(revisions, revision)
	}
	if err := scanner.Err(); err != nil {
		return nil, nil, fmt.Errorf("read the %s memory log: %w", agent, err)
	}
	return revisions, problems, nil
}

func decodeMemoryRevision(encoded []byte) (MemoryRevision, error) {
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	var revision MemoryRevision
	if err := decoder.Decode(&revision); err != nil {
		return MemoryRevision{}, err
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return MemoryRevision{}, err
	}
	if err := revision.Validate(); err != nil {
		return MemoryRevision{}, err
	}
	return revision, nil
}

// encodeMemoryRevision is one revision as one line, which is the shape the log
// is: an append that a crash interrupts costs the line it was writing and nothing
// before it.
func encodeMemoryRevision(revision MemoryRevision) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(revision); err != nil {
		return nil, fmt.Errorf("encode the memory revision: %w", err)
	}
	return buffer.Bytes(), nil
}

func (s *MemoryStore) append(path string, encoded []byte) error {
	if err := os.MkdirAll(s.root, 0o700); err != nil {
		return fmt.Errorf("create the memory directory: %w", err)
	}
	_, statErr := os.Stat(path)
	created := errors.Is(statErr, os.ErrNotExist)
	if statErr != nil && !created {
		return fmt.Errorf("inspect the memory log: %w", statErr)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open the memory log: %w", err)
	}
	written, err := file.Write(encoded)
	if err != nil {
		file.Close()
		return fmt.Errorf("append the memory revision: %w", err)
	}
	if written != len(encoded) {
		file.Close()
		return fmt.Errorf("append the memory revision: %w", io.ErrShortWrite)
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return fmt.Errorf("sync the memory log: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close the memory log: %w", err)
	}
	if created {
		return syncDirectory(s.root)
	}
	return nil
}

// lockAgent serializes writes to one agent's log against every other Yoyodyne
// process, so that the sequence a writer assigns is one nobody else is assigning
// at the same moment. It is the same advisory lock the reservations use, which
// the operating system drops if its holder exits unexpectedly.
//
// It is per agent rather than per product: two agents remembering something at
// once are two files, and a lock they shared would serialize writes that never
// meet.
func (s *MemoryStore) lockAgent(ctx context.Context, agent string) (func(), error) {
	if err := os.MkdirAll(s.root, 0o700); err != nil {
		return nil, fmt.Errorf("create the memory directory: %w", err)
	}
	path, err := s.lockPath(agent)
	if err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open the %s memory lock: %w", agent, err)
	}
	if err := lockStateFile(ctx, file); err != nil {
		file.Close()
		return nil, fmt.Errorf("lock the %s memory log: %w", agent, err)
	}
	return func() { _ = releaseStateFile(file) }, nil
}

// The names an agent's files take here. The live log ends in `.memory.jsonl`,
// which is what tells the listing a file holds an agent's memories rather than
// being the lock beside it or an archive rolled off it; an archive is the same
// name with a number in it, so the archives of one agent sort into the order they
// were rolled and no archive is ever read as a live log.
const (
	memoryLogSuffix     = ".memory.jsonl"
	memoryLockSuffix    = ".memory.lock"
	memoryArchiveMiddle = ".memory.archive-"
	memoryArchiveFormat = "%s" + memoryArchiveMiddle + "%04d.jsonl"
)

// archivePaths is every archive rolled off one agent's log, oldest first. There
// are none for an agent whose log has never been rolled, which is nearly all of
// them.
func (s *MemoryStore) archivePaths(agent string) ([]string, error) {
	if err := domain.ValidateIdentifier("agent", agent); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(s.root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read the memory directory: %w", err)
	}
	prefix := agent + memoryArchiveMiddle
	var archives []string
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, ".jsonl") {
			continue
		}
		archives = append(archives, filepath.Join(s.root, name))
	}
	// The number is fixed-width, so sorting the names sorts the archives into the
	// order they were rolled.
	sort.Strings(archives)
	return archives, nil
}

// nextArchivePath is where the log is about to be rolled to: the number after the
// highest one already there, so an archive is never written over. It is read off
// the names rather than counted, because counting would reuse a number where an
// archive has been moved away by hand and put the history that is still there
// behind the history that is arriving.
func (s *MemoryStore) nextArchivePath(agent string) (string, error) {
	archives, err := s.archivePaths(agent)
	if err != nil {
		return "", err
	}
	prefix := agent + memoryArchiveMiddle
	highest := 0
	for _, archive := range archives {
		numbered, err := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(filepath.Base(archive), prefix), ".jsonl"))
		if err != nil {
			continue
		}
		if numbered > highest {
			highest = numbered
		}
	}
	return filepath.Join(s.root, fmt.Sprintf(memoryArchiveFormat, agent, highest+1)), nil
}

// logPath names the one file an agent's memories live in. The agent is validated
// as an identifier before it reaches a path, so a configured agent name can never
// escape the memory directory.
func (s *MemoryStore) logPath(agent string) (string, error) {
	if err := domain.ValidateIdentifier("agent", agent); err != nil {
		return "", err
	}
	return filepath.Join(s.root, agent+memoryLogSuffix), nil
}

func (s *MemoryStore) lockPath(agent string) (string, error) {
	if err := domain.ValidateIdentifier("agent", agent); err != nil {
		return "", err
	}
	return filepath.Join(s.root, agent+memoryLockSuffix), nil
}
