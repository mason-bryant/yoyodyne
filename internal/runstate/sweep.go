package runstate

// When a recurring task last fired, and what came of it.
//
// Two records, one store, because they answer the two halves of the same
// question. The claim is what makes the cadence a cadence: a task fires when its
// interval has passed since the last time it fired, and the reading and the
// writing happen under one lock so two sessions polling the same schedule
// produce one firing rather than two. The log is what makes a firing worth
// having: a pass nobody watched reaches an operator only if what it found is
// written down somewhere they can read at leisure.
//
// The cadence is measured from the last firing rather than against a wall-clock
// grid, and that is a decision rather than an implementation detail. A harness
// that was off between two and five fires once when it comes back, not three
// times; a machine that slept through the night owes nobody eight sweeps. What a
// grid would buy is sweeps landing at the top of the hour, which is worth
// nothing to anybody reading these reports afterwards.
//
// A firing that failed still moves the clock. That is the other decision worth
// naming, and it is the opposite of the escalation record beside it: a stoppage
// that failed to reach the development manager is retried soon because it is one
// specific thing nobody has heard about, and a recurring pass that failed is
// simply run again at its next cadence, because the next pass looks at
// everything this one would have. Retrying it sooner would spend turns for
// nothing on a provider that is out of capacity, which on a busy poll loop is a
// firing every pull for as long as the outage lasts.
//
// Like the escalations beside it, it is one home per machine, for the same
// reason: what the coordination of two harnesses over one repository would take
// belongs to the team-mode epic rather than here.

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
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/sweep"
)

// SweepSchemaVersion is 1 and has never changed. It is versioned independently
// of run and conversation state, for the reason a report is: a sweep outlives
// the session that fired it, has no phase and nothing to integrate, and is
// written once and never revised.
const SweepSchemaVersion = 1

// maxEncodedSweepBytes bounds one encoded sweep record, including the trailing
// newline. The writer and the reader share it, so a sweep that was written is
// always one that can be read back.
//
// It is sized against the largest account a firing can legitimately produce
// rather than against a round number, because a bound below that is a bound that
// throws the busiest passes' reports away as it writes them. One turn's block is
// capped at sweep.MaxBlockBytes and a firing folds at most sweep.MaxMergedTurns
// of them together, so the account cannot exceed that product; what is here is
// comfortably above it, and a test in this package keeps the two in step.
const maxEncodedSweepBytes = 512 << 10

// MaxSweepTextBytes bounds the prose a record carries — what stopped a firing,
// and what stopped the last one on its claim. It is exported because what writes
// that prose is outside this package and has to be able to hold itself to the
// same number: a record refused for a problem too long to store is a firing whose
// report is lost over the description of a smaller failure.
const MaxSweepTextBytes = 4 << 10

// SweepClaim is the durable record of one recurring task's cadence: when it last
// fired, and what stopped that firing where something did.
//
// It is written before the turns are taken, so a process that dies mid-pass has
// recorded a firing that produced nothing rather than left a cadence that fires
// again on the next pull for as long as the deaths continue.
type SweepClaim struct {
	SchemaVersion int              `json:"schema_version"`
	ProductID     domain.ProductID `json:"product_id"`
	// Task is the configured name the cadence belongs to. A task renamed in the
	// configuration is a new cadence, which is the honest answer: nothing knows
	// the new name is the old one.
	Task string `json:"task"`
	// FiredAt is when the most recent firing was claimed, and is what the next
	// one is due from. Firings counts them, which is what a report of a schedule
	// is read for: a task that has fired forty times and a task nothing has ever
	// woken look identical without it.
	FiredAt   time.Time `json:"fired_at"`
	Firings   int       `json:"firings"`
	UpdatedAt time.Time `json:"updated_at"`
	// Problem is what stopped the last firing, and is cleared by one that worked.
	// A claim carrying one is a cadence that is running and producing nothing,
	// which is a state somebody has to be able to find without reading a log of
	// reports that were never written.
	Problem string `json:"problem,omitempty"`
}

// Due reports a task whose interval has passed. A task that has never fired is
// due at once rather than one interval from now: a schedule turned on at nine
// that produced nothing until ten would look broken for an hour, and the first
// pass is the one most worth having.
func (c SweepClaim) Due(every time.Duration, now time.Time) bool {
	if c.FiredAt.IsZero() {
		return true
	}
	return !now.Before(c.FiredAt.Add(every))
}

// NextDue is when this task fires again, for a report that says what a schedule
// is going to do rather than only what it has done.
func (c SweepClaim) NextDue(every time.Duration) time.Time {
	return c.FiredAt.Add(every)
}

// Validate reports every contract violation in the claim at once.
func (c SweepClaim) Validate() error {
	var problems []error
	if c.SchemaVersion != SweepSchemaVersion {
		problems = append(problems, fmt.Errorf("sweep schema version %d is not supported", c.SchemaVersion))
	}
	if err := domain.ValidateIdentifier("product id", string(c.ProductID)); err != nil {
		problems = append(problems, err)
	}
	if err := domain.ValidateIdentifier("recurring task name", c.Task); err != nil {
		problems = append(problems, err)
	}
	if c.FiredAt.IsZero() {
		problems = append(problems, errors.New("fired at is required"))
	}
	if c.Firings < 1 {
		problems = append(problems, fmt.Errorf("firings is %d, and a recorded claim is at least one firing", c.Firings))
	}
	if c.UpdatedAt.IsZero() {
		problems = append(problems, errors.New("updated at is required"))
	}
	if len(c.Problem) > MaxSweepTextBytes {
		problems = append(problems, fmt.Errorf("problem is %d bytes, limit is %d", len(c.Problem), MaxSweepTextBytes))
	}
	if err := errors.Join(problems...); err != nil {
		return fmt.Errorf("invalid recurring task claim: %w", err)
	}
	return nil
}

// Sweep is one firing's durable report: what the harness woke, what it cost, and
// the account the role gave of the pass.
//
// It is written once and never revised, which is why the account is stored as
// the role gave it rather than as a set of columns somebody would later want to
// correct. What the harness knows — which task, which role, which conversation,
// how many turns, what it cost — is the harness's and is never the role's to
// assert.
type Sweep struct {
	SchemaVersion int              `json:"schema_version"`
	ProductID     domain.ProductID `json:"product_id"`
	Task          string           `json:"task"`
	Role          domain.AgentRole `json:"role"`
	// ConversationID is the role's own durable conversation the pass happened in,
	// so what was actually said can be read from the conversation record rather
	// than only from this.
	ConversationID string    `json:"conversation_id,omitempty"`
	StartedAt      time.Time `json:"started_at"`
	EndedAt        time.Time `json:"ended_at"`
	// Turns is how many turns the pass took and CostUSD what the provider charged
	// for them. A pass that took every turn it was allowed is the signal that the
	// bound is what ended it rather than the work, which is exactly the thing a
	// week of these is read for.
	Turns   int     `json:"turns"`
	CostUSD float64 `json:"cost_usd,omitempty"`
	// Result is the account the role gave, merged across the turns of this
	// firing. It is absent where the pass produced none — a turn that failed, or
	// one that answered in prose without the block — and Problem then says why.
	Result *sweep.Result `json:"result,omitempty"`
	// Problem is what went wrong with the firing: the turn that failed, or the
	// account that could not be read. A sweep record carrying one is a pass that
	// spent a turn and told nobody anything, which must never be indistinguishable
	// from a quiet pass that found nothing.
	Problem string `json:"problem,omitempty"`
}

// FoundNothing reports the quiet pass: an account that was given and carried no
// findings. It is the ordinary result on a healthy harness, and it is a
// different fact from a pass that produced no account at all.
func (s Sweep) FoundNothing() bool {
	return s.Result != nil && len(s.Result.Findings) == 0
}

// Validate reports every contract violation in the record at once.
func (s Sweep) Validate() error {
	var problems []error
	if s.SchemaVersion != SweepSchemaVersion {
		problems = append(problems, fmt.Errorf("sweep schema version %d is not supported", s.SchemaVersion))
	}
	if err := domain.ValidateIdentifier("product id", string(s.ProductID)); err != nil {
		problems = append(problems, err)
	}
	if err := domain.ValidateIdentifier("recurring task name", s.Task); err != nil {
		problems = append(problems, err)
	}
	if !s.Role.Valid() {
		problems = append(problems, fmt.Errorf("role %q is not one this harness has", s.Role))
	}
	if s.StartedAt.IsZero() {
		problems = append(problems, errors.New("started at is required"))
	}
	if s.EndedAt.IsZero() {
		problems = append(problems, errors.New("ended at is required"))
	}
	if s.Turns < 0 {
		problems = append(problems, fmt.Errorf("turns is %d, and a pass cannot take a negative number of them", s.Turns))
	}
	// A pass that produced neither an account nor a problem would be a firing the
	// record can say nothing at all about, which is the one thing this must not be
	// able to hold: a reader would see a sweep that happened and no way to tell
	// whether it found nothing or failed.
	if s.Result == nil && strings.TrimSpace(s.Problem) == "" {
		problems = append(problems, errors.New("a sweep with no result must say what stopped it"))
	}
	if s.Result != nil {
		if err := s.Result.Validate(); err != nil {
			problems = append(problems, err)
		}
	}
	if len(s.Problem) > MaxSweepTextBytes {
		problems = append(problems, fmt.Errorf("problem is %d bytes, limit is %d", len(s.Problem), MaxSweepTextBytes))
	}
	if err := errors.Join(problems...); err != nil {
		return fmt.Errorf("invalid sweep: %w", err)
	}
	return nil
}

// ErrSweepNotDue is what a firing claimed before its interval has passed unwraps
// to, so a caller can tell "this task is not due" from a store that could not be
// read without matching on the words of either. It is the ordinary answer on
// almost every pull, which is why it is a typed refusal rather than a failure.
var ErrSweepNotDue = errors.New("this recurring task is not due to fire yet")

// SweepNotDueError names the claim that refused and when the next firing is due,
// so a caller that meets it can say which task it was.
type SweepNotDueError struct {
	Existing SweepClaim
	NextDue  time.Time
}

func (e SweepNotDueError) Error() string {
	return fmt.Sprintf("the recurring task %s last fired at %s, and the next firing is not due until %s",
		e.Existing.Task,
		e.Existing.FiredAt.UTC().Format(time.RFC3339),
		e.NextDue.UTC().Format(time.RFC3339))
}

func (e SweepNotDueError) Unwrap() error { return ErrSweepNotDue }

// SweepStore is where the recurring tasks' cadence and their reports live: one
// directory under the product, beside the escalations, with one claim file per
// task and one append-only log of what the firings produced.
type SweepStore struct {
	root      string
	productID domain.ProductID
}

func NewSweepStore(root string, productID domain.ProductID) (*SweepStore, error) {
	if !filepath.IsAbs(root) {
		return nil, errors.New("state root must be an absolute path")
	}
	if err := domain.ValidateIdentifier("product id", string(productID)); err != nil {
		return nil, err
	}
	return &SweepStore{
		root:      filepath.Join(filepath.Clean(root), "products", string(productID), "sweeps"),
		productID: productID,
	}, nil
}

// Sweeps is the recurring-task record for this run store's product, reached from
// here for the reason the escalations are: whoever can read what became of an
// item's runs can read what the harness has been doing on a cadence, without
// being told the state root a second time.
func (s *Store) Sweeps() *SweepStore {
	return &SweepStore{
		root:      filepath.Join(filepath.Dir(s.root), "sweeps"),
		productID: s.productID,
	}
}

func (s *SweepStore) Root() string { return s.root }

// Path names the log itself, so a failure can say where the recorded sweeps
// actually are.
func (s *SweepStore) Path() string { return filepath.Join(s.root, "sweeps.jsonl") }

// Claim records that a task is firing now, and refuses one whose interval has
// not passed. It is written before the turns are taken, which is what makes the
// refusal mean anything: a claim recorded afterwards would leave the window
// where two sessions fire the same task at once wide open.
func (s *SweepStore) Claim(ctx context.Context, task string, every time.Duration, now time.Time) (SweepClaim, error) {
	name := strings.TrimSpace(task)
	if err := domain.ValidateIdentifier("recurring task name", name); err != nil {
		return SweepClaim{}, err
	}
	if every <= 0 {
		return SweepClaim{}, fmt.Errorf("the recurring task %s has no interval, so nothing can say when it is due", name)
	}
	at := now
	if at.IsZero() {
		at = time.Now()
	}
	at = at.UTC()

	release, err := s.lock(ctx, name)
	if err != nil {
		return SweepClaim{}, err
	}
	defer release()

	claimed, found, err := s.load(name)
	if err != nil {
		return SweepClaim{}, err
	}
	if found {
		// Refused here rather than only where the interval is read, because here is
		// where the lock is. A caller that checked the cadence itself and then
		// claimed would be two reads and a write with a window between them, which
		// is exactly the window two concurrent sessions land in.
		if !claimed.Due(every, at) {
			return SweepClaim{}, SweepNotDueError{Existing: claimed, NextDue: claimed.NextDue(every)}
		}
	} else {
		claimed = SweepClaim{Task: name}
	}
	claimed.SchemaVersion = SweepSchemaVersion
	claimed.ProductID = s.productID
	claimed.Task = name
	claimed.Firings++
	claimed.FiredAt = at
	claimed.UpdatedAt = at
	// Cleared as the firing starts rather than as it ends, so a claim carrying a
	// problem is always the most recent firing's and never one left behind by a
	// firing two cadences ago.
	claimed.Problem = ""
	if err := claimed.Validate(); err != nil {
		return SweepClaim{}, err
	}
	if err := s.save(name, claimed); err != nil {
		return SweepClaim{}, err
	}
	return claimed, nil
}

// Settle records what became of the firing that is claimed: nothing on one that
// produced a report, and what stopped it on one that did not. The clock is
// deliberately not moved back — see this file's opening on why a failed pass
// waits for its next cadence rather than being retried at once.
func (s *SweepStore) Settle(ctx context.Context, task, problem string) (SweepClaim, error) {
	name := strings.TrimSpace(task)
	if err := domain.ValidateIdentifier("recurring task name", name); err != nil {
		return SweepClaim{}, err
	}

	release, err := s.lock(ctx, name)
	if err != nil {
		return SweepClaim{}, err
	}
	defer release()

	settled, found, err := s.load(name)
	if err != nil {
		return SweepClaim{}, err
	}
	if !found {
		return SweepClaim{}, fmt.Errorf("no firing of the recurring task %s is recorded, so there is nothing to settle", name)
	}
	settled.Problem = boundedSweepText(problem)
	settled.UpdatedAt = time.Now().UTC()
	if err := s.save(name, settled); err != nil {
		return SweepClaim{}, err
	}
	return settled, nil
}

// Find reports one task's claim. A task that has never fired is the ordinary
// answer rather than a failure to look, and a claim that cannot be read is
// neither: it is an error, because a cadence nobody can read must never be fired
// through as though it were absent.
func (s *SweepStore) Find(task string) (SweepClaim, bool, error) {
	name := strings.TrimSpace(task)
	if err := domain.ValidateIdentifier("recurring task name", name); err != nil {
		return SweepClaim{}, false, err
	}
	return s.load(name)
}

// Append records one firing's report durably. It is an append rather than a
// rewrite for the reason the collected reports are: a sweep is written once and
// never revised, and two sessions finishing a pass at the same time must not
// overwrite each other's record.
func (s *SweepStore) Append(recorded Sweep) error {
	recorded.SchemaVersion = SweepSchemaVersion
	recorded.ProductID = s.productID
	if err := recorded.Validate(); err != nil {
		return err
	}
	encoded, err := json.Marshal(recorded)
	if err != nil {
		return fmt.Errorf("encode sweep: %w", err)
	}
	encoded = append(encoded, '\n')
	if len(encoded) > maxEncodedSweepBytes {
		return fmt.Errorf("encoded sweep is %d bytes, limit is %d", len(encoded), maxEncodedSweepBytes)
	}
	if err := os.MkdirAll(s.root, 0o700); err != nil {
		return fmt.Errorf("create sweep directory: %w", err)
	}
	path := s.Path()
	_, statErr := os.Stat(path)
	created := errors.Is(statErr, os.ErrNotExist)
	if statErr != nil && !created {
		return fmt.Errorf("inspect the sweep log: %w", statErr)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open the sweep log: %w", err)
	}
	written, err := file.Write(encoded)
	if err != nil {
		file.Close()
		return fmt.Errorf("append sweep: %w", err)
	}
	if written != len(encoded) {
		file.Close()
		return fmt.Errorf("append sweep: %w", io.ErrShortWrite)
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return fmt.Errorf("sync the sweep log: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close the sweep log: %w", err)
	}
	if created {
		return syncDirectory(s.root)
	}
	return nil
}

// UnreadableSweep is one line of the log that would not decode, and why.
//
// It exists because of what this log is: appended to once per firing, never
// rewritten, and read from one surface. A write of a record this size is not
// atomic, so a process killed partway through one leaves a torn line — and a
// reader that failed the whole listing on the first line it could not decode
// would make one interrupted write cost every report before it, permanently, on
// the only surface those reports are read from. So a line that will not decode is
// set aside and named rather than fatal, and the reports around it stay
// reachable. Named rather than skipped, because a listing that quietly dropped
// records would be a worse answer than the failure it replaced.
type UnreadableSweep struct {
	// Line is the 1-based line of the log, so somebody can go and look at it.
	Line    int    `json:"line"`
	Problem string `json:"problem"`
}

// List returns every recorded sweep in the order it was written, and beside them
// the lines that would not decode. A log that does not exist yet is a product
// nothing has swept, which is not a failure to read.
//
// The error is for a log that could not be read at all. A line that will not
// decode is not one: it comes back in the second return value, and the sweeps
// around it come back with it — see UnreadableSweep for why that is the
// direction this fails in.
func (s *SweepStore) List() ([]Sweep, []UnreadableSweep, error) {
	file, err := os.Open(s.Path())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("open the sweep log: %w", err)
	}
	defer file.Close()

	var recorded []Sweep
	var unreadable []UnreadableSweep
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64<<10), maxEncodedSweepBytes)
	line := 0
	for scanner.Scan() {
		line++
		text := strings.TrimSpace(scanner.Text())
		if text == "" {
			continue
		}
		decoder := json.NewDecoder(bytes.NewReader([]byte(text)))
		decoder.DisallowUnknownFields()
		var entry Sweep
		if err := decoder.Decode(&entry); err != nil {
			unreadable = append(unreadable, UnreadableSweep{Line: line, Problem: err.Error()})
			continue
		}
		if err := ensureJSONEOF(decoder); err != nil {
			unreadable = append(unreadable, UnreadableSweep{Line: line, Problem: err.Error()})
			continue
		}
		if entry.ProductID != s.productID {
			unreadable = append(unreadable, UnreadableSweep{
				Line:    line,
				Problem: fmt.Sprintf("this record belongs to product %q, not %q", entry.ProductID, s.productID),
			})
			continue
		}
		recorded = append(recorded, entry)
	}
	if err := scanner.Err(); err != nil {
		// The scan itself failing is different from a line that will not decode:
		// nothing after the failure was read at all, so what came before it is
		// returned with the failure rather than instead of it, and the caller says
		// the listing is partial.
		return recorded, unreadable, fmt.Errorf("read the sweep log: %w", err)
	}
	return recorded, unreadable, nil
}

func (s *SweepStore) load(task string) (SweepClaim, bool, error) {
	path := s.path(task)
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return SweepClaim{}, false, nil
	}
	if err != nil {
		return SweepClaim{}, false, fmt.Errorf("open recurring task claim: %w", err)
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, maxEncodedStateBytes))
	decoder.DisallowUnknownFields()
	var claimed SweepClaim
	if err := decoder.Decode(&claimed); err != nil {
		return SweepClaim{}, false, fmt.Errorf("decode recurring task claim at %s: %w", path, err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return SweepClaim{}, false, fmt.Errorf("decode recurring task claim at %s: %w", path, err)
	}
	if err := claimed.Validate(); err != nil {
		return SweepClaim{}, false, err
	}
	if claimed.ProductID != s.productID {
		return SweepClaim{}, false, fmt.Errorf("recurring task claim belongs to product %q, not %q", claimed.ProductID, s.productID)
	}
	if claimed.Task != task {
		return SweepClaim{}, false, fmt.Errorf("recurring task claim at %s belongs to task %q, not %q", path, claimed.Task, task)
	}
	return claimed, true, nil
}

// save replaces one task's claim durably, as a temporary file and a rename, so a
// process that dies mid-write leaves the previous claim rather than a truncated
// file nothing can read — which for a cadence would be a task that never fires
// again.
func (s *SweepStore) save(task string, claimed SweepClaim) error {
	if err := claimed.Validate(); err != nil {
		return err
	}
	if err := os.MkdirAll(s.root, 0o700); err != nil {
		return fmt.Errorf("create sweep directory: %w", err)
	}
	temporary, err := os.CreateTemp(s.root, ".claim-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary recurring task claim: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return fmt.Errorf("secure temporary recurring task claim: %w", err)
	}
	if err := writeJSONFile(temporary, "recurring task claim", claimed); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary recurring task claim: %w", err)
	}
	if err := os.Rename(temporaryPath, s.path(task)); err != nil {
		return fmt.Errorf("replace recurring task claim: %w", err)
	}
	return syncDirectory(s.root)
}

// lock serializes the read-modify-write on one task's claim across every
// Yoyodyne process, and the lock file outlives the write for the reason the
// escalations' does: removing it while another process held it would let a third
// take a lock on a file nobody else can see.
func (s *SweepStore) lock(ctx context.Context, task string) (func(), error) {
	if err := os.MkdirAll(s.root, 0o700); err != nil {
		return nil, fmt.Errorf("create sweep directory: %w", err)
	}
	file, err := os.OpenFile(s.path(task)+".lock", os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open recurring task lock: %w", err)
	}
	if err := lockStateFile(ctx, file); err != nil {
		file.Close()
		return nil, fmt.Errorf("lock the recurring task %s: %w", task, err)
	}
	return func() { _ = releaseStateFile(file) }, nil
}

// path names one task's claim. A task name is a validated identifier — lowercase
// letters, digits, and single hyphens — so unlike a docket key it is already a
// file name, and the record is named for what an operator would look for.
func (s *SweepStore) path(task string) string {
	return filepath.Join(s.root, "claim-"+task+".json")
}

func boundedSweepText(text string) string {
	trimmed := strings.TrimSpace(text)
	if len(trimmed) > MaxSweepTextBytes {
		return trimmed[:MaxSweepTextBytes]
	}
	return trimmed
}
