package runstate

// What triage has already spent on one piece of work, and what that work has
// cost in review rounds across every run of it.
//
// Triage decides what to do about a run that did not land: hand the developer
// another repair grant, cause the item to be run again, re-arm a merge the
// forge dropped. Each of those buys another attempt at the same work, and none
// of them is bounded by anything the run itself records — a run's repair budget
// is spent inside that run and starts again at zero in the next one. So an item
// triaged twice, and then twice more, is an item nothing was ever going to stop,
// and the record of what it had already been given lived in whichever
// conversation happened to be open at the time.
//
// This is the durable home for that: one record per work item, beside the runs
// rather than inside any of them, because what triage has spent on a piece of
// work outlives every run of it. Every counter is written before the action it
// counts takes effect, so a process that dies between the two has recorded a
// grant it did not give rather than given one it did not record — the direction
// that costs an unspent attempt rather than a duplicated one.
//
// It is one home per machine. Two collaborators running their own harnesses
// against one repository each hold a full set of budgets for the same item,
// which is a limit rather than a design: the coordination that would make them
// one is the team-mode epic's, and `docs/team-mode-scope.md` states it where
// that epic's designer will find it.
//
// Of the four counters, one has a caller in the harness today. Every run records
// its review rounds here, because a round is something the run itself produces.
// The three action counters have none, because no triage decision in this
// release grants a repair, causes a re-run, or re-arms a dropped merge — the
// harness blocks the item and hands it to a person instead, and reconciliation
// deliberately does not ask a forge again about a merge it dropped. So the three
// operations below are the gate rather than the decision: an action added later
// records itself through one of them and is refused by the cap it finds, instead
// of arriving with a budget of its own invention. Building the budget first is
// what stops the second half of that pair being written by whoever is in a hurry
// to ship the first.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// TriageCountersSchemaVersion is 1 and has never changed.
const TriageCountersSchemaVersion = 1

// maxTriageKeyPrefix bounds the readable half of a counter file's name. It
// exists so a directory listing says which item each file belongs to; the digest
// after it is what makes the name unique.
const maxTriageKeyPrefix = 60

// The triage actions a cap can refuse, named here because a refusal has to say
// which action it refused. They are the actions themselves rather than the
// counters: what an operator reads is "no more re-runs", not "the re-run
// counter".
const (
	TriageRepairGrant = "repair grant"
	TriageRerun       = "re-run"
	TriageMergeRearm  = "merge re-arm"
)

// The budgets an action is refused against, which are not the same vocabulary as
// the actions and must not be printed as though they were. Two of the three
// actions buy review rounds and are refused by the round budget; the third buys
// none and has a budget of its own. A refusal that named only the action would
// leave an operator raising the wrong number.
const (
	TriageReviewRoundBudget = "review round"
	TriageMergeRearmBudget  = "merge re-arm"
)

// TriageCounters is what one work item has been given and what it has cost.
// Every field is a total across every run of the item, which is the whole
// reason the record is not kept on a run.
type TriageCounters struct {
	SchemaVersion int              `json:"schema_version"`
	ProductID     domain.ProductID `json:"product_id"`
	WorkItemID    string           `json:"work_item_id"`
	// RepairGrants is how many times triage has handed this item another go at
	// its own change, and GrantedRounds how many review rounds those grants added
	// up to. The two differ whenever a grant was cut down to what the round cap
	// still had room for, and TruncatedGrants is how often that happened: a grant
	// that was quietly halved and one that was given in full are different facts
	// about how close this item is to the end of its budget.
	RepairGrants    int `json:"repair_grants,omitempty"`
	GrantedRounds   int `json:"granted_rounds,omitempty"`
	TruncatedGrants int `json:"truncated_grants,omitempty"`
	// Reruns is how many times triage has caused this item to be run again from
	// the start, and MergeRearms how many times it has re-armed a merge the forge
	// accepted and then dropped.
	Reruns      int `json:"reruns,omitempty"`
	MergeRearms int `json:"merge_rearms,omitempty"`
	// ReviewRounds is how many reviewer verdicts this item's developer attempts
	// have produced, across every run of it. It is the figure a repair grant is
	// truncated against, because it is the one that says what the item has
	// actually cost: repair budgets reset with each run and this does not.
	//
	// A re-review that no developer attempt produced is not a round. The
	// integration replay is the case: a promotion that loses its race replays the
	// same work onto where the target went and asks for a fresh verdict on it,
	// and counting that would charge the item for losing a race it did not cause.
	ReviewRounds int `json:"review_rounds,omitempty"`
	// LastRound identifies the developer attempt whose verdict was counted last,
	// and only that one: it is the most recent round rather than a set of every
	// round counted. Recording a round names the attempt that produced it, and an
	// attempt that names the round already at the head is recorded once — which is
	// what keeps a review re-asked for the same attempt off the bill, whether it
	// was resumed after an interrupted process or re-obtained on a replayed
	// change.
	//
	// Consecutive is the whole of what has to be deduplicated, because it is the
	// whole of what can happen. A repeat is always a re-review of the attempt the
	// run has most recently been judged on, and nothing else can slip a round for
	// this item in between: a run reserves the item exclusively, so a second run
	// of the same item is refused while the first is in flight. An attempt
	// repeated after some other round of the same item had been counted would be a
	// second run judging an attempt of the first, which is not a thing the harness
	// can produce.
	LastRound string    `json:"last_round,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Validate reports every contract violation in the record at once.
func (c TriageCounters) Validate() error {
	var problems []error
	if c.SchemaVersion != TriageCountersSchemaVersion {
		problems = append(problems, fmt.Errorf("triage counter schema version %d is not supported", c.SchemaVersion))
	}
	if err := domain.ValidateIdentifier("product id", string(c.ProductID)); err != nil {
		problems = append(problems, err)
	}
	if strings.TrimSpace(c.WorkItemID) == "" {
		problems = append(problems, errors.New("work item id is required"))
	}
	for _, counter := range []struct {
		field string
		value int
	}{
		{"repair_grants", c.RepairGrants},
		{"granted_rounds", c.GrantedRounds},
		{"truncated_grants", c.TruncatedGrants},
		{"reruns", c.Reruns},
		{"merge_rearms", c.MergeRearms},
		{"review_rounds", c.ReviewRounds},
	} {
		if counter.value < 0 {
			problems = append(problems, fmt.Errorf("%s cannot be negative", counter.field))
		}
	}
	// A grant that was cut is a grant that was given, and rounds are only ever
	// granted by one. Either the other way round describes a record that cannot
	// have been written by the only thing that writes them.
	if c.TruncatedGrants > c.RepairGrants {
		problems = append(problems, fmt.Errorf("%d grants are recorded as truncated of %d given", c.TruncatedGrants, c.RepairGrants))
	}
	if c.GrantedRounds > 0 && c.RepairGrants == 0 {
		problems = append(problems, errors.New("granted rounds require the grant that gave them"))
	}
	if c.LastRound != "" && c.ReviewRounds == 0 {
		problems = append(problems, errors.New("a counted round requires the round count that includes it"))
	}
	if c.UpdatedAt.IsZero() {
		problems = append(problems, errors.New("updated at is required"))
	}
	if err := errors.Join(problems...); err != nil {
		return fmt.Errorf("invalid triage counters: %w", err)
	}
	return nil
}

// Passes is how many times triage has acted on this item. Each of the three
// actions is one pass, because each is one decision somebody made about work
// that did not land; a pass that decided to do nothing spends nothing and is not
// one of these. Two or more is what a reader is looking for — an item nobody
// triaged and an item triaged repeatedly are the two ends of what this record
// exists to tell apart — and it is answerable from the record alone.
func (c TriageCounters) Passes() int {
	return c.RepairGrants + c.Reruns + c.MergeRearms
}

// TriagedAgain reports an item triage has come back to. It is the question a
// reader of one item's record asks first: everything is worth one look, and a
// second says the first look did not settle it.
func (c TriageCounters) TriagedAgain() bool { return c.Passes() > 1 }

// RoundsRemaining is how many further review rounds this item may still be
// given under a cap. It is never negative: an item already past its cap has
// nothing remaining rather than a debt.
func (c TriageCounters) RoundsRemaining(limit int) int {
	if remaining := limit - c.ReviewRounds; remaining > 0 {
		return remaining
	}
	return 0
}

// TriageCaps bounds each triage action for one work item. They are per item and
// per machine, so they bound what one harness will spend on one piece of work
// however many runs it takes.
//
// There are two of them rather than one per action, because that is what the
// configuration states and this is deliberately not the place that invents
// another number. `triage.review_rounds_cap` is the ceiling on what an item may
// cost, and both of the actions that buy rounds — another repair, another whole
// run — are refused against it. A merge re-arm buys no round at all, so it is
// the one action that needs a bound of its own, and it takes the size of the
// integration retries a single run is already permitted.
type TriageCaps struct {
	// ReviewRounds is the ceiling on the rounds one item may accumulate across
	// every run of it: `triage.review_rounds_cap`. It is what a repair grant is
	// truncated against and what refuses a re-run, and it is not an action
	// anybody asks permission for — nothing asks to be reviewed, and the rounds
	// an item has spent are a fact rather than a request.
	ReviewRounds int
	// MergeRearms bounds the one action that buys no round. An action that costs
	// no provider invocation is the one that can be taken forever, and a merge
	// the forge keeps dropping is a repository somebody has to look at.
	MergeRearms int
}

// Validate refuses a cap set nothing could be spent against. A cap of zero is a
// deliberate choice — never grant this — and a negative one is not a choice at
// all.
func (c TriageCaps) Validate() error {
	var problems []error
	for _, limit := range []struct {
		field string
		value int
	}{
		{"review round cap", c.ReviewRounds},
		{"merge re-arm cap", c.MergeRearms},
	} {
		if limit.value < 0 {
			problems = append(problems, fmt.Errorf("%s cannot be negative", limit.field))
		}
	}
	return errors.Join(problems...)
}

// ErrTriageCapReached is what every refusal past a cap unwraps to, so a caller
// can tell "this item has had its budget" from a store that could not be read
// without matching on the words of either.
var ErrTriageCapReached = errors.New("triage cap reached")

// TriageCapError names the action that was refused, the budget that refused it,
// and what was already spent against that budget. The action and the budget are
// separate because they are frequently not the same thing: a re-run is refused
// by the review round budget, and an operator told only "re-run refused" would
// go looking for a re-run cap that does not exist.
type TriageCapError struct {
	Action     string
	Budget     string
	WorkItemID string
	Spent      int
	Cap        int
}

func (e TriageCapError) Error() string {
	if e.Cap == 0 {
		return fmt.Sprintf("no %s is permitted for %s: the %s cap is 0", e.Action, e.WorkItemID, e.Budget)
	}
	return fmt.Sprintf("%s is refused for %s: %d of %d permitted %s(s) are spent", e.Action, e.WorkItemID, e.Spent, e.Cap, e.Budget)
}

func (e TriageCapError) Unwrap() error { return ErrTriageCapReached }

// RepairGrant is what a granted repair actually came to. Rounds is what may be
// spent, which is what the caller acts on; Requested and Truncated are what was
// asked for and whether the round cap cut it, because a grant of one where two
// were asked for is a different thing to tell somebody than a grant of one.
type RepairGrant struct {
	Requested int
	Rounds    int
	Truncated bool
	Counters  TriageCounters
}

// TriageStore is where the counters live: one directory under the product,
// beside the runs rather than among them, and one file per work item inside it.
// A file each rather than one shared record is what keeps concurrent runs of
// different items out of each other's way — the harness runs several at once,
// and every one of them records its review rounds.
type TriageStore struct {
	root      string
	productID domain.ProductID
}

func NewTriageStore(root string, productID domain.ProductID) (*TriageStore, error) {
	if !filepath.IsAbs(root) {
		return nil, errors.New("state root must be an absolute path")
	}
	if err := domain.ValidateIdentifier("product id", string(productID)); err != nil {
		return nil, err
	}
	return &TriageStore{
		root:      filepath.Join(filepath.Clean(root), "products", string(productID), "triage"),
		productID: productID,
	}, nil
}

// Triage is the counter store for this run store's product. It is reached from
// here because the two are one product's durable record: whoever can read what
// became of an item's runs can read what triage has spent on the item, without
// having to be told the state root a second time.
func (s *Store) Triage() *TriageStore {
	return &TriageStore{
		root:      filepath.Join(filepath.Dir(s.root), "triage"),
		productID: s.productID,
	}
}

func (s *TriageStore) Root() string { return s.root }

// Counters reports what one work item has been given and what it has cost. An
// item nothing has recorded anything about carries zero of everything, which is
// the ordinary answer rather than a failure to look: every item starts there.
// A record that cannot be read is not that, and is an error, because an
// unreadable budget must never be spent through as though it were empty.
func (s *TriageStore) Counters(workItemID string) (TriageCounters, error) {
	id := strings.TrimSpace(workItemID)
	if id == "" {
		return TriageCounters{}, errors.New("a work item is required to read its triage counters")
	}
	return s.load(id)
}

// RecordReviewRound counts one reviewer verdict against a work item and reports
// what the item now stands at. It never refuses: a round is something that
// happened rather than something being asked for, and a cap that could stop it
// being written down would only make the record disagree with the world.
//
// attemptID identifies the developer attempt whose change was judged, and
// recording the attempt that is already at the head of the record counts once.
// That is what excludes the integration replay: the replayed change is
// re-reviewed under the attempt that produced it, so the second verdict names
// the round just counted. It is also what makes an interrupted review safe to
// resume — the review is re-asked for, and the round is not counted again.
//
// Only the most recent round is compared against, which is sufficient rather
// than approximate; LastRound says why.
func (s *TriageStore) RecordReviewRound(ctx context.Context, workItemID, attemptID string, at time.Time) (TriageCounters, error) {
	if strings.TrimSpace(attemptID) == "" {
		return TriageCounters{}, errors.New("a developer attempt is required to count the round its review produced")
	}
	return s.update(ctx, workItemID, at, func(counters *TriageCounters) error {
		if counters.LastRound == attemptID {
			return errNoTriageChange
		}
		counters.ReviewRounds++
		counters.LastRound = attemptID
		return nil
	})
}

// GrantRepair records a repair grant and reports what it came to. The grant is
// truncated to the review rounds the cap still has room for, because a grant
// larger than that is unspendable: at the harness defaults an item that has been
// through its repair budget once has spent three rounds of four, so a grant of
// the configured two attempts would promise a round nothing would ever let it
// take, and a grant that simply overshot the cap would make the cap decorative.
// The truncation is recorded rather than applied silently — a grant that was cut
// is what says this item is at the end of what it will be given.
//
// rounds is what `triage.repair_grant_attempts` states, which is a count of
// repair attempts and is therefore also a count of rounds: every repair attempt
// is judged, so each one an item is granted is one more verdict it will produce.
//
// It is written before the developer is handed anything, so a process that dies
// between the two has spent a grant it did not give. That is the direction this
// record exists to fail in: the other one is a grant given twice.
func (s *TriageStore) GrantRepair(ctx context.Context, workItemID string, rounds int, caps TriageCaps) (RepairGrant, error) {
	if rounds < 1 {
		return RepairGrant{}, errors.New("a repair grant gives at least one round")
	}
	if err := caps.Validate(); err != nil {
		return RepairGrant{}, err
	}
	granted := RepairGrant{Requested: rounds}
	counters, err := s.update(ctx, workItemID, time.Time{}, func(counters *TriageCounters) error {
		remaining := counters.RoundsRemaining(caps.ReviewRounds)
		if remaining == 0 {
			return TriageCapError{
				Action:     TriageRepairGrant,
				Budget:     TriageReviewRoundBudget,
				WorkItemID: counters.WorkItemID,
				Spent:      counters.ReviewRounds,
				Cap:        caps.ReviewRounds,
			}
		}
		granted.Rounds = rounds
		if granted.Rounds > remaining {
			granted.Rounds = remaining
			granted.Truncated = true
			counters.TruncatedGrants++
		}
		counters.RepairGrants++
		counters.GrantedRounds += granted.Rounds
		return nil
	})
	if err != nil {
		return RepairGrant{}, err
	}
	granted.Counters = counters
	return granted, nil
}

// RecordRerun records that triage caused this item to be run again, and refuses
// once the item has no review rounds left. A re-run buys a whole fresh run,
// repair budget and all, so what it costs is not one round but every round the
// new run goes on to spend — and an item with none remaining would produce its
// first verdict already past the cap.
//
// Like a grant it is written before the run it causes is started, so a crash
// between the two costs a re-run nobody took rather than a re-run nobody
// counted.
func (s *TriageStore) RecordRerun(ctx context.Context, workItemID string, caps TriageCaps) (TriageCounters, error) {
	if err := caps.Validate(); err != nil {
		return TriageCounters{}, err
	}
	return s.update(ctx, workItemID, time.Time{}, func(counters *TriageCounters) error {
		if counters.RoundsRemaining(caps.ReviewRounds) == 0 {
			return TriageCapError{
				Action:     TriageRerun,
				Budget:     TriageReviewRoundBudget,
				WorkItemID: counters.WorkItemID,
				Spent:      counters.ReviewRounds,
				Cap:        caps.ReviewRounds,
			}
		}
		counters.Reruns++
		return nil
	})
}

// RecordMergeRearm records that triage re-armed a merge the forge accepted and
// then dropped, and refuses once the item has had the re-arms its cap permits.
// It spends no provider invocation at all, which is exactly why it is bounded
// separately: an action that costs nothing to take is the one that can be taken
// forever, and a merge that keeps being dropped is a repository somebody has to
// look at.
func (s *TriageStore) RecordMergeRearm(ctx context.Context, workItemID string, caps TriageCaps) (TriageCounters, error) {
	if err := caps.Validate(); err != nil {
		return TriageCounters{}, err
	}
	return s.update(ctx, workItemID, time.Time{}, func(counters *TriageCounters) error {
		if counters.MergeRearms >= caps.MergeRearms {
			return TriageCapError{
				Action:     TriageMergeRearm,
				Budget:     TriageMergeRearmBudget,
				WorkItemID: counters.WorkItemID,
				Spent:      counters.MergeRearms,
				Cap:        caps.MergeRearms,
			}
		}
		counters.MergeRearms++
		return nil
	})
}

// RoundKey names the developer attempt one review judged: the run it was made
// in and how many repairs into that run it is. It is the identity a counted
// round is recorded under, and it is derived rather than minted so that the same
// attempt asked about twice always answers with the same key.
func RoundKey(runID string, repairAttempts int) string {
	return fmt.Sprintf("%s#%d", runID, repairAttempts)
}

// errNoTriageChange reports an update that turned out to have nothing to write.
// It never reaches a caller: the record is already what the caller asked for, so
// it is returned as it stands rather than rewritten.
var errNoTriageChange = errors.New("triage counters are already what this update would make them")

// update takes one item's counters, applies a change to them under the item's
// own lock, and makes the result durable before returning it. The lock is per
// item and held only for the read and the write, so two runs recording rounds
// for two items never wait on each other, and two processes recording against
// the same item cannot lose one of the two increments to a read that predated
// the other's write.
func (s *TriageStore) update(ctx context.Context, workItemID string, at time.Time, change func(*TriageCounters) error) (TriageCounters, error) {
	id := strings.TrimSpace(workItemID)
	if id == "" {
		return TriageCounters{}, errors.New("a work item is required to record its triage counters")
	}
	release, err := s.lock(ctx, id)
	if err != nil {
		return TriageCounters{}, err
	}
	defer release()

	counters, err := s.load(id)
	if err != nil {
		return TriageCounters{}, err
	}
	if err := change(&counters); err != nil {
		if errors.Is(err, errNoTriageChange) {
			return counters, nil
		}
		return TriageCounters{}, err
	}
	when := at
	if when.IsZero() {
		when = time.Now()
	}
	counters.UpdatedAt = when.UTC()
	if err := s.save(id, counters); err != nil {
		return TriageCounters{}, err
	}
	return counters, nil
}

// load reads one item's counters, or the empty set an item nothing has recorded
// anything about stands at.
func (s *TriageStore) load(workItemID string) (TriageCounters, error) {
	file, err := os.Open(s.path(workItemID))
	if errors.Is(err, os.ErrNotExist) {
		return TriageCounters{
			SchemaVersion: TriageCountersSchemaVersion,
			ProductID:     s.productID,
			WorkItemID:    workItemID,
		}, nil
	}
	if err != nil {
		return TriageCounters{}, fmt.Errorf("open triage counters: %w", err)
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, maxEncodedStateBytes))
	decoder.DisallowUnknownFields()
	var counters TriageCounters
	if err := decoder.Decode(&counters); err != nil {
		return TriageCounters{}, fmt.Errorf("decode triage counters for %s: %w", workItemID, err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return TriageCounters{}, fmt.Errorf("decode triage counters for %s: %w", workItemID, err)
	}
	if err := counters.Validate(); err != nil {
		return TriageCounters{}, err
	}
	if counters.ProductID != s.productID {
		return TriageCounters{}, fmt.Errorf("triage counters belong to product %q, not %q", counters.ProductID, s.productID)
	}
	// The file is named for a digest of the work item rather than for the item
	// itself, so this is what catches two items that were ever to land on one
	// name: the record says whose it is, and a record that is not this item's is
	// refused rather than spent.
	if counters.WorkItemID != workItemID {
		return TriageCounters{}, fmt.Errorf("triage counters at %s belong to work item %q, not %q", s.path(workItemID), counters.WorkItemID, workItemID)
	}
	return counters, nil
}

// save replaces one item's counters durably. The write is a temporary file and a
// rename, as every other revised record here is, so a process that dies mid-write
// leaves the previous counters rather than a truncated file nothing can read.
func (s *TriageStore) save(workItemID string, counters TriageCounters) error {
	counters.SchemaVersion = TriageCountersSchemaVersion
	counters.ProductID = s.productID
	counters.WorkItemID = workItemID
	if err := counters.Validate(); err != nil {
		return err
	}
	if err := os.MkdirAll(s.root, 0o700); err != nil {
		return fmt.Errorf("create triage counter directory: %w", err)
	}
	temporary, err := os.CreateTemp(s.root, ".triage-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary triage counters: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return fmt.Errorf("secure temporary triage counters: %w", err)
	}
	if err := writeJSONFile(temporary, "triage counters", counters); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary triage counters: %w", err)
	}
	if err := os.Rename(temporaryPath, s.path(workItemID)); err != nil {
		return fmt.Errorf("replace triage counters: %w", err)
	}
	return syncDirectory(s.root)
}

// lock serializes the read-modify-write on one item's counters across every
// Yoyodyne process. The lock file outlives the update for the reason a run's
// lease file does: removing it while another process held it would let a third
// take a lock on a file nobody else can see.
func (s *TriageStore) lock(ctx context.Context, workItemID string) (func(), error) {
	if err := os.MkdirAll(s.root, 0o700); err != nil {
		return nil, fmt.Errorf("create triage counter directory: %w", err)
	}
	file, err := os.OpenFile(s.path(workItemID)+".lock", os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open triage counter lock: %w", err)
	}
	if err := lockStateFile(ctx, file); err != nil {
		file.Close()
		return nil, fmt.Errorf("lock triage counters for %s: %w", workItemID, err)
	}
	return func() { file.Close() }, nil
}

func (s *TriageStore) path(workItemID string) string {
	return filepath.Join(s.root, triageKey(workItemID)+".json")
}

// triageKey names one work item's counter file. A tracker identifier is not a
// file name — nothing stops one carrying a slash — so the name is a bounded,
// lowercased rendering of the identifier for whoever reads the directory, with a
// digest of the exact identifier after it so two that render alike still get
// their own file.
func triageKey(workItemID string) string {
	digest := sha256.Sum256([]byte(workItemID))
	var rendered strings.Builder
	for _, character := range strings.ToLower(workItemID) {
		if rendered.Len() >= maxTriageKeyPrefix {
			break
		}
		switch {
		case character >= 'a' && character <= 'z', character >= '0' && character <= '9':
			rendered.WriteRune(character)
		default:
			rendered.WriteByte('-')
		}
	}
	return rendered.String() + "-" + hex.EncodeToString(digest[:8])
}
