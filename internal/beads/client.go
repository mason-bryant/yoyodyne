package beads

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/goal"
)

const defaultTimeout = 30 * time.Second

// MaxPriority is the lowest Beads priority, and 0 is the highest. It is exported
// so a caller can refuse a priority it was handed rather than discovering the
// problem as a bd failure.
const MaxPriority = 4

type WorkItem struct {
	ID                 string
	Title              string
	Description        string
	Design             string
	AcceptanceCriteria string
	Notes              string
	Status             string
	Priority           int
	IssueType          string
	Assignee           string
	Parent             string
	Dependencies       []Dependency
	// CreatedAt is when the tracker recorded this item, which is what says what
	// the work was admitted knowing. It is the zero time where the tracker did not
	// say, and a caller that needs it reports that it does not know rather than
	// treating an unknown admission time as the beginning of time.
	CreatedAt time.Time
	// Cost is what the runs made for this item have cost, as the tracker holds
	// it. It is absent from an item nothing has ever priced, which is a different
	// fact from an item that cost nothing.
	Cost *Cost
	// GoalWitness is what the tracker records, outside this item's notes, about a
	// goal having been written into them. It is kept there because notes are what
	// a careless writer replaces: an item whose notes lost their goal still
	// carries this, which is what tells a destroyed attribution from one nobody
	// ever made and what says which goal to put back.
	GoalWitness goal.Witness
	// Executor is what carries this item's execution, where it is not a developer
	// run. It is empty for ordinary work, which is what an item that names none
	// is; see domain.WorkItemExecutor for why the ordinary case is the silent one.
	//
	// It is read from the tracker's own metadata rather than from the notes,
	// because selection reads it: a marker in prose is a marker the next writer
	// replaces, and the whole point of this one is that nothing chooses the item
	// after it is set.
	Executor domain.WorkItemExecutor
	// Parking is why this item is deliberately not to be pulled, and is empty for
	// the queued work that is. It is metadata for the same reason the executor is,
	// and it is a different question from the executor: this one says the work is
	// not to be started now, not that no run could start it.
	Parking domain.WorkItemParking
}

// Cost is the provider-reported price of every run made for one work item. It
// is carried by the tracker itself rather than assembled beside it, so the
// briefing, the conversation, and bd all read one number from one place.
//
// UnknownRuns is what keeps that number honest. A run whose evidence no longer
// survives cannot be priced, and pricing it as nothing would understate every
// total it entered; while it is non-zero, TotalUSD is a floor on what the item
// cost rather than what it cost.
type Cost struct {
	TotalUSD    float64 `json:"total_usd"`
	Runs        int     `json:"runs"`
	UnknownRuns int     `json:"unknown_runs,omitempty"`
}

// The tracker metadata keys the price is carried in. They are namespaced
// because the metadata is the project's own and Yoyodyne is a guest in it.
const (
	costTotalKey   = "yoyodyne_cost_usd"
	costRunsKey    = "yoyodyne_cost_runs"
	costUnknownKey = "yoyodyne_cost_unknown_runs"
)

// goalWitnessKey is where the tracker records the goal that was written onto an
// item. The notes remain where an attribution is made and read from; this is a
// copy kept where replacing those notes cannot reach it, so a destroyed
// attribution can be told from one nobody made and put back from the words
// rather than judged again.
const goalWitnessKey = "yoyodyne_goal_recorded"

// executorKey is where the tracker records what carries an item's execution.
// Like the goal witness it is metadata rather than notes, and for a sharper
// reason: this one is read by selection, so a marker the next writer of the
// notes could replace would be a marker that stops working exactly when
// somebody records an outcome on the item.
const executorKey = "yoyodyne_executor"

// parkedKey is where the tracker records that an item is parked, and why. It is
// metadata for the reason the executor is — selection reads it, and notes are
// what the next writer replaces — and it is the whole of the parking rather than
// a flag beside a reason kept elsewhere: the reason is what says whether
// releasing the work is right, and a marker that outlived its reason would be
// back to a parking nobody can account for.
//
// The key is absent from work that is not parked, and released work carries it
// with nothing in it. Both read as unparked, which is what lets a release be
// written the same way everything else here is written — one value set on the
// item — rather than needing the tracker to forget a key.
const parkedKey = "yoyodyne_parked"

// witnessValue is what the witness holds for one goal: the statement itself
// where it fits, and a bare "1" where it does not. The bound is the one a goals
// document is already held to, so anything a goal can actually be stated in is
// carried whole; something longer is witnessed without its words rather than
// stored truncated, because a statement cut in half is not the goal and would
// be put back as if it were.
func witnessValue(statement string) string {
	trimmed := strings.TrimSpace(statement)
	if trimmed == "" || len(trimmed) > goal.MaxStatementBytes {
		return "1"
	}
	return trimmed
}

// creationMetadata renders the metadata a creation carries. bd takes an item's
// whole metadata as one JSON object at creation — unlike an update, which sets
// one key — so this flag owns every key the created item will have, and any
// future key has to be added to the map here rather than as a second
// --metadata that would replace this one.
func creationMetadata(entries map[string]string) (string, error) {
	encoded, err := json.Marshal(entries)
	if err != nil {
		return "", fmt.Errorf("encode bd metadata: %w", err)
	}
	return string(encoded), nil
}

// costPrecision is how many decimal places a recorded price keeps. A single
// invocation can cost fractions of a cent, and a ledger that rounded each item
// to the cent would drift from the invocations it was summed from.
const costPrecision = 6

// Validate rejects a price that could not describe real spending.
func (c Cost) Validate() error {
	var problems []error
	if math.IsNaN(c.TotalUSD) || math.IsInf(c.TotalUSD, 0) {
		problems = append(problems, errors.New("total cost is not a number"))
	} else if c.TotalUSD < 0 {
		problems = append(problems, fmt.Errorf("total cost %v cannot be negative", c.TotalUSD))
	}
	if c.Runs < 1 {
		problems = append(problems, errors.New("a recorded price must cover at least one run"))
	}
	if c.UnknownRuns < 0 {
		problems = append(problems, errors.New("unknown runs cannot be negative"))
	}
	if c.UnknownRuns > c.Runs {
		problems = append(problems, fmt.Errorf("%d of %d runs cannot be unknown", c.UnknownRuns, c.Runs))
	}
	return errors.Join(problems...)
}

// Complete reports a price no unpriceable run is missing from.
func (c Cost) Complete() bool { return c.UnknownRuns == 0 }

type Dependency struct {
	// IssueID is the item the tracker attributes this edge to, where it says. It
	// is what tells an edge pointing at an item's parent from one a listing
	// carried in the other direction, so a reader that cares which way an edge
	// runs checks it and one that does not can ignore it. It is empty where the
	// tracker did not say, which a reader treats as this item's own.
	IssueID string
	ID      string
	Type    string
	Status  string
}

// parentChildDependency is how bd states decomposition when it states it as an
// edge rather than as a field. See WorkItem.DecomposedFrom for why that
// distinction is not academic.
const parentChildDependency = "parent-child"

// DecomposedFrom is the item this one was broken out of, and empty for work that
// was not broken out of anything.
//
// bd states that relationship two ways and a store may use either: the parent
// field beside the item, and a parent-child dependency on the parent. A capture
// of this project's own tracker states it both ways — testdata holds the listing
// it answered for yoyodyne-ifd.121.2 and its parent — but the tracker's own
// export states only the edge, carrying no parent field on any item in it, so a
// reading that consults only the field sees such a store as a backlog with no
// decomposition anywhere in it. The scheduler started yoyodyne-ifd.121 and the
// child carrying its execution as two developer runs of one scope with the guard
// against exactly that already in place, which is why which way the store states
// parentage is not something this reading assumes either way.
//
// The field wins where both are stated, because that is the tracker answering
// the question directly. An edge the tracker attributes to some other item is
// skipped, so a listing that carries an item's children alongside its parent
// cannot be read as the item having been broken out of its own child.
func (w WorkItem) DecomposedFrom() string {
	if parent := strings.TrimSpace(w.Parent); parent != "" {
		return parent
	}
	id := strings.TrimSpace(w.ID)
	for _, dependency := range w.Dependencies {
		if !strings.EqualFold(strings.TrimSpace(dependency.Type), parentChildDependency) {
			continue
		}
		if issue := strings.TrimSpace(dependency.IssueID); issue != "" && issue != id {
			continue
		}
		if parent := strings.TrimSpace(dependency.ID); parent != "" && parent != id {
			return parent
		}
	}
	return ""
}

// NewWorkItem is a work item to create. It is deliberately narrow: the harness
// creates items on someone's explicit approval, so this carries the item and
// the provenance that explains it rather than every field bd accepts.
type NewWorkItem struct {
	Title       string
	Description string
	// Type is the Beads issue type. It is required: an item created with
	// whatever type bd happened to default to is not the item that was approved.
	Type string
	// Notes records where the item came from. Beads keeps it beside the
	// description rather than inside it, so provenance stays legible as the
	// description is edited.
	Notes  string
	Parent string
	// Executor is what will carry the work, where that is not a developer run.
	// It is optional and empty for ordinary work, and it is set here rather than
	// afterwards because an item is chosen from the moment it is admitted: a
	// marker added in a later call is a window in which the harness can pull the
	// item for a run nothing can execute.
	Executor domain.WorkItemExecutor
	// Parking is why work is being admitted already parked, and is empty for the
	// work that is admitted to be pulled. It is set here rather than afterwards
	// for the reason the executor is: an item is chosen from the moment it is
	// admitted, and a marker added by a later call is a window in which a
	// draining queue can pull work somebody has just said not to do.
	Parking domain.WorkItemParking
	// Priority is where the item is admitted in the backlog's order. It is a
	// pointer because zero is the highest Beads priority rather than an absent
	// one, and because admitting work without saying where it goes is a real
	// request: the tracker's own default is then what places it.
	Priority *int
}

// WorkItemChange is a bounded edit to an item that already exists. Each field is
// applied only when it is set, so an edit says exactly what it changes and
// leaves everything it does not name alone.
type WorkItemChange struct {
	Title       string
	Description string
	// AppendNotes adds to the item's notes rather than replacing them, so an
	// edit never erases the provenance an earlier one recorded.
	AppendNotes string
	// Executor is what carries the item's execution, applied only when it is set.
	// It is here as well as on a creation because the marker had to arrive after
	// the queue did: work whose executor is a conversation was admitted for as
	// long as there was no way to say so, and an item that cannot acquire the
	// marker afterwards is an item that keeps being chosen for a run.
	Executor domain.WorkItemExecutor
	// Parking is why the item is not to be pulled, applied only when it is set.
	// It is a pointer because parking and releasing are different requests and
	// leaving the parking alone is a third: a nil parking changes nothing, an
	// empty one releases the item back into the queue, and a stated one parks it
	// with that as the reason.
	Parking *domain.WorkItemParking
	// Priority is a pointer because zero is the highest Beads priority rather
	// than an absent one.
	Priority *int
	// Parent is a pointer because reparenting and detaching are different
	// requests: a nil parent leaves the item where it is, and an empty one
	// removes the parent bd currently records.
	Parent *string
}

type Client struct {
	Runner  execution.ProcessRunner
	Binary  string
	Dir     string
	Timeout time.Duration
}

var (
	issueIDPattern   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
	statusPattern    = regexp.MustCompile(`^[a-z][a-z_]*$`)
	issueTypePattern = regexp.MustCompile(`^[a-z][a-z_]*$`)
)

func (c Client) Show(ctx context.Context, id string) (WorkItem, error) {
	if err := validateIssueID(id); err != nil {
		return WorkItem{}, err
	}
	data, err := c.run(ctx, "show", id, "--json")
	if err != nil {
		return WorkItem{}, err
	}
	return decodeSingleWorkItem(data)
}

// List reports the work items Beads currently holds, optionally narrowed to one
// status. It is read-only: nothing about listing work claims, changes, or
// closes any of it.
func (c Client) List(ctx context.Context, status string) ([]WorkItem, error) {
	args := []string{"list", "--json"}
	if trimmed := strings.TrimSpace(status); trimmed != "" {
		if !statusPattern.MatchString(trimmed) {
			return nil, fmt.Errorf("invalid Beads status %q", status)
		}
		args = append(args, "--status="+trimmed)
	}
	data, err := c.run(ctx, args...)
	if err != nil {
		return nil, err
	}
	return decodeWorkItems(data)
}

// Ready reports the work items the tracker itself considers ready to be worked
// on: admitted, not already claimed, and waiting on nothing unfinished. It is
// read-only.
//
// It exists because readiness is the tracker's answer to give rather than this
// client's to infer. A dependency lives in the tracker's own graph, and a status
// listing is not guaranteed to carry it, so deciding "this item lists no
// blockers, therefore it can be pulled" would report a blocked item as the next
// thing to work on wherever the listing leaves dependencies out.
func (c Client) Ready(ctx context.Context) ([]WorkItem, error) {
	data, err := c.run(ctx, "ready", "--json")
	if err != nil {
		return nil, err
	}
	return decodeWorkItems(data)
}

// Create records a new work item. Unlike every other call here it brings work
// into existence, so the caller is responsible for having the authority to ask:
// this adapter is the mechanism, never the approval.
func (c Client) Create(ctx context.Context, item NewWorkItem) (WorkItem, error) {
	if err := item.validate(); err != nil {
		return WorkItem{}, err
	}
	args := []string{
		"create",
		"--title=" + item.Title,
		"--description=" + item.Description,
		"--type=" + item.Type,
	}
	// Every key the created item will carry is gathered before any of it is
	// rendered, because bd takes a creation's metadata as one object: a second
	// --metadata would replace the first rather than add to it.
	entries := map[string]string{}
	if notes := strings.TrimSpace(item.Notes); notes != "" {
		args = append(args, "--notes="+notes)
		if statement, records := goal.NamedIn(notes); records {
			// The witness is derived from what is about to be written rather than
			// asked of the caller. A caller that had to remember it would eventually
			// not, and an attribution written without one is exactly an attribution
			// whose loss goes unnoticed.
			entries[goalWitnessKey] = witnessValue(statement)
		}
	}
	if executor := strings.TrimSpace(string(item.Executor)); executor != "" {
		entries[executorKey] = executor
	}
	if parking := item.Parking.Reason(); parking != "" {
		entries[parkedKey] = parking
	}
	if len(entries) > 0 {
		metadata, err := creationMetadata(entries)
		if err != nil {
			return WorkItem{}, err
		}
		args = append(args, "--metadata="+metadata)
	}
	if parent := strings.TrimSpace(item.Parent); parent != "" {
		args = append(args, "--parent="+parent)
	}
	if item.Priority != nil {
		args = append(args, "--priority="+strconv.Itoa(*item.Priority))
	}
	args = append(args, "--json")
	data, err := c.run(ctx, args...)
	if err != nil {
		return WorkItem{}, err
	}
	// Creation answers with the one item it made rather than a list, and the
	// created identifier is what everything downstream refers to, so an answer
	// without one is a failure however it was reported.
	var raw rawWorkItem
	if err := decodeJSON(data, &raw); err != nil {
		return WorkItem{}, fmt.Errorf("decode bd created work item: %w", err)
	}
	created, err := convertWorkItem(raw)
	if err != nil {
		return WorkItem{}, err
	}
	if created.Title != item.Title {
		return WorkItem{}, fmt.Errorf("bd created work item %s with title %q, want %q", created.ID, created.Title, item.Title)
	}
	// An executor that was asked for and not stored is a failure, because
	// admission is the path this marker exists for: the item is in the queue and
	// pullable the moment this returns, so a caller told the marker was set would
	// have admitted exactly the item the guard does not cover. It is read back
	// where the priority beside it is not, and the difference is what an absent
	// field means. bd's creation response omits a key it was never given, so a
	// missing executor is unambiguously one that was not stored — where a missing
	// priority is indistinguishable from the default having been applied, which is
	// why refusing on that one would lose items rather than protect the order.
	if executor := strings.TrimSpace(string(item.Executor)); executor != "" && string(created.Executor) != executor {
		return WorkItem{}, fmt.Errorf("bd created work item %s with executor %q, want %q", created.ID, created.Executor, executor)
	}
	// The parking is read back for the same reason and against the same risk: an
	// item admitted as parked is in the queue and pullable the moment this
	// returns, so a caller told the work was parked would have admitted exactly
	// the item a draining queue is free to take.
	if parking := item.Parking.Reason(); parking != "" && created.Parking.Reason() != parking {
		return WorkItem{}, fmt.Errorf("bd created work item %s parked %q, want %q", created.ID, created.Parking, parking)
	}
	// The requested priority is deliberately not read back, for the reason the
	// parent is not read back after an update: an unset field and a field bd's
	// response does not carry are indistinguishable here, and refusing a creation
	// that actually happened would lose the item rather than protect the order.
	return created, nil
}

// Update applies a bounded edit to an item that already exists. Like Create it
// changes the tracker, so the caller is responsible for having the authority to
// ask; unlike Create it names the fields it touches, which is what lets an edit
// be validated before it is run and checked against what bd reports afterwards.
func (c Client) Update(ctx context.Context, id string, change WorkItemChange) (WorkItem, error) {
	if err := validateIssueID(id); err != nil {
		return WorkItem{}, err
	}
	if err := change.validate(); err != nil {
		return WorkItem{}, err
	}
	args := []string{"update", id}
	if title := strings.TrimSpace(change.Title); title != "" {
		args = append(args, "--title="+title)
	}
	if description := strings.TrimSpace(change.Description); description != "" {
		args = append(args, "--description="+description)
	}
	if notes := strings.TrimSpace(change.AppendNotes); notes != "" {
		args = append(args, "--append-notes="+notes)
		if statement, records := goal.NamedIn(notes); records {
			args = append(args, "--set-metadata="+goalWitnessKey+"="+witnessValue(statement))
		}
	}
	if executor := strings.TrimSpace(string(change.Executor)); executor != "" {
		args = append(args, "--set-metadata="+executorKey+"="+executor)
	}
	// A release sets the key to nothing rather than removing it, so parking and
	// releasing are one write with one shape. What matters afterwards is what the
	// item reads as, and both an absent key and an empty one read as unparked.
	if change.Parking != nil {
		args = append(args, "--set-metadata="+parkedKey+"="+change.Parking.Reason())
	}
	if change.Priority != nil {
		args = append(args, "--priority="+strconv.Itoa(*change.Priority))
	}
	if change.Parent != nil {
		args = append(args, "--parent="+strings.TrimSpace(*change.Parent))
	}
	args = append(args, "--json")
	data, err := c.run(ctx, args...)
	if err != nil {
		return WorkItem{}, err
	}
	item, err := decodeSingleWorkItem(data)
	if err != nil {
		return WorkItem{}, err
	}
	// What bd echoes back is verified against what was asked for, so an edit that
	// did not take effect is a failure rather than a reported success. The parent
	// is the exception, and knowingly so: bd's update response does not carry it,
	// so a reparenting rests on bd's own report of success and is not read back.
	if title := strings.TrimSpace(change.Title); title != "" && item.Title != title {
		return WorkItem{}, fmt.Errorf("work item %s title is %q after being updated, want %q", item.ID, item.Title, title)
	}
	if change.Priority != nil && item.Priority != *change.Priority {
		return WorkItem{}, fmt.Errorf("work item %s priority is %d after being updated, want %d", item.ID, item.Priority, *change.Priority)
	}
	// The executor is read back for the reason a price is: what rests on it is
	// that nothing chooses this item for a run afterwards, and a marker reported
	// as set and not actually stored would leave the caller believing the item
	// was covered by exactly the guard it is not covered by.
	if executor := strings.TrimSpace(string(change.Executor)); executor != "" && string(item.Executor) != executor {
		return WorkItem{}, fmt.Errorf("work item %s executor is %q after being updated, want %q", item.ID, item.Executor, executor)
	}
	// The parking is read back in both directions, because both directions have
	// something resting on them. A parking that did not take leaves work the
	// operator was told is parked sitting pullable in a queue that drains; a
	// release that did not take leaves work nobody can start and no error saying
	// so, which is the harder of the two to ever notice.
	if change.Parking != nil && item.Parking.Reason() != change.Parking.Reason() {
		if change.Parking.Parked() {
			return WorkItem{}, fmt.Errorf("work item %s is parked %q after being updated, want %q", item.ID, item.Parking, change.Parking.Reason())
		}
		return WorkItem{}, fmt.Errorf("work item %s is still parked %q after being released", item.ID, item.Parking)
	}
	return item, nil
}

func (c Client) Claim(ctx context.Context, id string) (WorkItem, error) {
	if err := validateIssueID(id); err != nil {
		return WorkItem{}, err
	}
	data, err := c.run(ctx, "update", id, "--claim", "--json")
	if err != nil {
		return WorkItem{}, err
	}
	return decodeSingleWorkItem(data)
}

func (c Client) RecordOutcome(ctx context.Context, id, notes string) (WorkItem, error) {
	if err := validateIssueID(id); err != nil {
		return WorkItem{}, err
	}
	if strings.TrimSpace(notes) == "" {
		return WorkItem{}, errors.New("outcome notes are required")
	}
	data, err := c.run(ctx, "update", id, "--append-notes="+notes, "--json")
	if err != nil {
		return WorkItem{}, err
	}
	return decodeSingleWorkItem(data)
}

// Block records a durable blocker on a work item the harness could not finish,
// carrying the reason into the item's notes. The applied status is verified
// rather than assumed: a blocker that was not actually stored would leave the
// item looking like work still in progress.
func (c Client) Block(ctx context.Context, id, reason string) (WorkItem, error) {
	if err := validateIssueID(id); err != nil {
		return WorkItem{}, err
	}
	if strings.TrimSpace(reason) == "" {
		return WorkItem{}, errors.New("blocker reason is required")
	}
	data, err := c.run(ctx, "update", id, "--status=blocked", "--append-notes="+reason, "--json")
	if err != nil {
		return WorkItem{}, err
	}
	item, err := decodeSingleWorkItem(data)
	if err != nil {
		return WorkItem{}, err
	}
	if item.Status != "blocked" {
		return WorkItem{}, fmt.Errorf("work item %s status is %q after being blocked, want blocked", item.ID, item.Status)
	}
	return item, nil
}

func (c Client) AddBlocker(ctx context.Context, id, blockerID string) error {
	return c.changeBlocker(ctx, "add", "added", id, blockerID)
}

// RemoveBlocker unlinks a dependency the tracker records. It verifies what bd
// reports for the same reason adding does: a link that is still there after
// being removed would leave work looking blocked by something nobody thinks
// blocks it.
func (c Client) RemoveBlocker(ctx context.Context, id, blockerID string) error {
	return c.changeBlocker(ctx, "remove", "removed", id, blockerID)
}

func (c Client) changeBlocker(ctx context.Context, command, applied, id, blockerID string) error {
	if err := validateIssueID(id); err != nil {
		return err
	}
	if err := validateIssueID(blockerID); err != nil {
		return fmt.Errorf("invalid blocker: %w", err)
	}
	data, err := c.run(ctx, "dep", command, id, blockerID, "--json")
	if err != nil {
		return err
	}
	var response dependencyResponse
	if err := decodeJSON(data, &response); err != nil {
		return fmt.Errorf("decode bd dependency response: %w", err)
	}
	if response.Status != applied || response.IssueID != id || response.DependsOnID != blockerID {
		return fmt.Errorf("unexpected bd dependency response: status=%q issue=%q blocker=%q", response.Status, response.IssueID, response.DependsOnID)
	}
	return nil
}

// RecordGoalWitness records, outside an item's notes, the goal those notes
// already state. It writes no attribution and makes no judgement: the statement
// it stores is one the caller read off the item itself, which is why this can
// run over work the product manager attributed long ago without deciding
// anything on their behalf.
//
// It exists because an attribution written before the witness did is protected
// by nothing: the notes can be replaced tomorrow and the item afterwards reads
// as work nobody ever attributed. Nothing here reaches an item whose notes state
// no goal — there is nothing to witness, and writing one would turn an
// unattributed item into a permanently lost one.
//
// What bd echoes back is verified, for the reason a price is: a witness that was
// not actually stored would leave the caller believing an item was covered.
func (c Client) RecordGoalWitness(ctx context.Context, id, statement string) (WorkItem, error) {
	if err := validateIssueID(id); err != nil {
		return WorkItem{}, err
	}
	if strings.TrimSpace(statement) == "" {
		return WorkItem{}, errors.New("the goal to witness is required")
	}
	data, err := c.run(ctx, "update", id, "--set-metadata="+goalWitnessKey+"="+witnessValue(statement), "--json")
	if err != nil {
		return WorkItem{}, err
	}
	item, err := decodeSingleWorkItem(data)
	if err != nil {
		return WorkItem{}, err
	}
	if !item.GoalWitness.Recorded {
		return WorkItem{}, fmt.Errorf("work item %s carries no goal witness after being witnessed", item.ID)
	}
	return item, nil
}

// RecordCost stores what the runs made for one work item have cost, so the
// tracker itself carries the price and everything that reads the tracker sees
// it without a second data source. Like every other write here it is the
// mechanism rather than the authority: the caller is responsible for the price
// being the provider's own report and not an estimate.
//
// What bd echoes back is verified, for the reason an edit is: a price that was
// not actually stored would leave the item looking unpriced while the caller
// believed the ledger had been written.
func (c Client) RecordCost(ctx context.Context, id string, cost Cost) (WorkItem, error) {
	if err := validateIssueID(id); err != nil {
		return WorkItem{}, err
	}
	if err := cost.Validate(); err != nil {
		return WorkItem{}, fmt.Errorf("invalid work item cost: %w", err)
	}
	data, err := c.run(ctx, "update", id,
		"--set-metadata="+costTotalKey+"="+formatCost(cost.TotalUSD),
		"--set-metadata="+costRunsKey+"="+strconv.Itoa(cost.Runs),
		"--set-metadata="+costUnknownKey+"="+strconv.Itoa(cost.UnknownRuns),
		"--json")
	if err != nil {
		return WorkItem{}, err
	}
	item, err := decodeSingleWorkItem(data)
	if err != nil {
		return WorkItem{}, err
	}
	if item.Cost == nil {
		return WorkItem{}, fmt.Errorf("work item %s carries no cost after being priced", item.ID)
	}
	// The stored total is compared at the precision it was written with, because
	// bd stores it as a number and returns whatever that number renders as.
	if formatCost(item.Cost.TotalUSD) != formatCost(cost.TotalUSD) ||
		item.Cost.Runs != cost.Runs || item.Cost.UnknownRuns != cost.UnknownRuns {
		return WorkItem{}, fmt.Errorf("work item %s cost is %#v after being priced, want %#v", item.ID, *item.Cost, cost)
	}
	return item, nil
}

func formatCost(total float64) string {
	return strconv.FormatFloat(total, 'f', costPrecision, 64)
}

func (c Client) Complete(ctx context.Context, id, reason string) (WorkItem, error) {
	if err := validateIssueID(id); err != nil {
		return WorkItem{}, err
	}
	if strings.TrimSpace(reason) == "" {
		return WorkItem{}, errors.New("completion reason is required")
	}
	data, err := c.run(ctx, "close", id, "--reason="+reason, "--json")
	if err != nil {
		return WorkItem{}, err
	}
	return decodeSingleWorkItem(data)
}

func (c Client) run(ctx context.Context, args ...string) ([]byte, error) {
	runner := c.Runner
	if runner == nil {
		return nil, errors.New("bd process runner is required")
	}
	binary := c.Binary
	if binary == "" {
		binary = "bd"
	}
	timeout := c.Timeout
	if timeout == 0 {
		timeout = defaultTimeout
	}
	result, err := runner.Run(ctx, execution.Command{
		Name:    binary,
		Args:    args,
		Dir:     c.Dir,
		Timeout: timeout,
	}, nil)
	if err != nil {
		return nil, fmt.Errorf("run bd %s: %w", args[0], err)
	}
	if result.Status != execution.ProcessSucceeded {
		message := strings.TrimSpace(result.Stderr)
		if message == "" {
			message = strings.TrimSpace(result.Stdout)
		}
		return nil, fmt.Errorf("bd %s failed with status %s and exit code %d: %s", args[0], result.Status, result.ExitCode, message)
	}
	return []byte(result.Stdout), nil
}

type rawWorkItem struct {
	ID                 string          `json:"id"`
	Title              string          `json:"title"`
	Description        string          `json:"description"`
	Design             string          `json:"design"`
	AcceptanceCriteria string          `json:"acceptance_criteria"`
	Notes              string          `json:"notes"`
	Status             string          `json:"status"`
	Priority           int             `json:"priority"`
	IssueType          string          `json:"issue_type"`
	Assignee           string          `json:"assignee"`
	Parent             string          `json:"parent"`
	Dependencies       []rawDependency `json:"dependencies"`
	// CreatedAt is read as text and parsed here rather than decoded as a time,
	// because it is one field of an item and not the item: a tracker that wrote a
	// timestamp this cannot read must leave the admission time unknown, not fail
	// every read of the work it belongs to.
	CreatedAt string `json:"created_at"`
	// Metadata is the tracker's own key-value store on an item. Only the keys
	// the harness writes are read out of it; everything else in there belongs to
	// whoever put it there.
	Metadata map[string]json.RawMessage `json:"metadata"`
}

type rawDependency struct {
	ID             string `json:"id"`
	IssueID        string `json:"issue_id"`
	DependsOnID    string `json:"depends_on_id"`
	DependencyType string `json:"dependency_type"`
	Type           string `json:"type"`
	Status         string `json:"status"`
}

type dependencyResponse struct {
	IssueID     string `json:"issue_id"`
	DependsOnID string `json:"depends_on_id"`
	Status      string `json:"status"`
}

func decodeSingleWorkItem(data []byte) (WorkItem, error) {
	items, err := decodeWorkItems(data)
	if err != nil {
		return WorkItem{}, err
	}
	if len(items) != 1 {
		return WorkItem{}, fmt.Errorf("bd returned %d work items, want 1", len(items))
	}
	return items[0], nil
}

func decodeWorkItems(data []byte) ([]WorkItem, error) {
	var rawItems []rawWorkItem
	if err := decodeJSON(data, &rawItems); err != nil {
		return nil, fmt.Errorf("decode bd work item: %w", err)
	}
	items := make([]WorkItem, 0, len(rawItems))
	for _, raw := range rawItems {
		item, err := convertWorkItem(raw)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, nil
}

func convertWorkItem(raw rawWorkItem) (WorkItem, error) {
	if err := validateIssueID(raw.ID); err != nil {
		return WorkItem{}, fmt.Errorf("bd returned invalid work item: %w", err)
	}
	item := WorkItem{
		ID:                 raw.ID,
		Title:              raw.Title,
		Description:        raw.Description,
		Design:             raw.Design,
		AcceptanceCriteria: raw.AcceptanceCriteria,
		Notes:              raw.Notes,
		Status:             raw.Status,
		Priority:           raw.Priority,
		IssueType:          raw.IssueType,
		Assignee:           raw.Assignee,
		Parent:             raw.Parent,
		CreatedAt:          admittedAt(raw.CreatedAt),
		Dependencies:       make([]Dependency, 0, len(raw.Dependencies)),
	}
	for _, dependency := range raw.Dependencies {
		id := dependency.ID
		if id == "" {
			id = dependency.DependsOnID
		}
		dependencyType := dependency.DependencyType
		if dependencyType == "" {
			dependencyType = dependency.Type
		}
		if id != "" {
			item.Dependencies = append(item.Dependencies, Dependency{
				IssueID: dependency.IssueID,
				ID:      id,
				Type:    dependencyType,
				Status:  dependency.Status,
			})
		}
	}
	item.Cost = costFromMetadata(raw.Metadata)
	item.GoalWitness = goalWitnessIn(raw.Metadata)
	item.Executor = executorIn(raw.Metadata)
	item.Parking = parkingIn(raw.Metadata)
	return item, nil
}

// parkingIn reads why the tracker records an item as parked. An absent key and
// an empty value are both unparked, which is what work nobody ever parked and
// work somebody released both look like.
//
// Anything but a string is read as unparked, and that is the opposite of how the
// executor beside it is read, deliberately. An unreadable executor is carried
// through because the safe answer there is "no run may take this"; here the
// unreadable case is a value the harness never writes, and treating one as a
// parking would take an item out of the queue with nothing to say why or how to
// get it back.
func parkingIn(metadata map[string]json.RawMessage) domain.WorkItemParking {
	raw, present := metadata[parkedKey]
	if !present {
		return ""
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return ""
	}
	return domain.WorkItemParking(strings.TrimSpace(value))
}

// executorIn reads what the tracker records about who carries an item's
// execution. A value the harness does not recognize is carried through as it was
// written rather than dropped: the caller asks whether the item is a developer
// run, and something nobody can read was still put there by somebody who meant
// it was not one. Dropping it would answer "developer run" and spend the run
// this marker exists to save.
//
// Anything but a string is nothing at all, because nothing but a string is ever
// written here — the harness writes one at creation and one on an update, and
// neither can produce a number or an object.
func executorIn(metadata map[string]json.RawMessage) domain.WorkItemExecutor {
	raw, present := metadata[executorKey]
	if !present {
		return ""
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return ""
	}
	return domain.WorkItemExecutor(strings.TrimSpace(value))
}

// goalWitnessIn reads what the tracker records about a goal written onto an
// item: that one was, and the words where it kept them. Anything the key holds
// but a false or empty value counts as a witness, because the harness writes it
// two ways — a creation's JSON object and an update's key=value, which bd does
// not store as the same type — and because what is being asked first is whether
// the key is there at all. Reading it strictly would turn the tracker's own
// coercion into a destroyed attribution reported as a gap, which is the failure
// this exists to catch. A value that is a bare marker rather than a statement
// witnesses the loss without its words, which is what an item witnessed before
// the words were kept carries.
func goalWitnessIn(metadata map[string]json.RawMessage) goal.Witness {
	raw, present := metadata[goalWitnessKey]
	if !present {
		return goal.Witness{}
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return goal.Witness{}
	}
	switch witnessed := value.(type) {
	case nil:
		return goal.Witness{}
	case bool:
		return goal.Witness{Recorded: witnessed}
	case string:
		trimmed := strings.TrimSpace(witnessed)
		if trimmed == "" || trimmed == "0" || strings.EqualFold(trimmed, "false") {
			return goal.Witness{}
		}
		if trimmed == "1" || strings.EqualFold(trimmed, "true") {
			return goal.Witness{Recorded: true}
		}
		return goal.Witness{Recorded: true, Statement: trimmed}
	case float64:
		return goal.Witness{Recorded: witnessed != 0}
	default:
		return goal.Witness{Recorded: true}
	}
}

// admittedAt reads when the tracker says an item was recorded, and returns the
// zero time when it says nothing this can read. An unknown admission time is a
// fact a caller can report; a guessed one would date work to whenever the format
// happened to fail.
func admittedAt(recorded string) time.Time {
	admitted, err := time.Parse(time.RFC3339, strings.TrimSpace(recorded))
	if err != nil {
		return time.Time{}
	}
	return admitted.UTC()
}

// costFromMetadata reads the price the tracker carries, or nothing at all when
// it carries none. A partial record is read as no price rather than as a cheap
// one: the total alone would not say how many runs it covers or whether any of
// them went unpriced, and a floor presented as a price is worse than silence.
func costFromMetadata(metadata map[string]json.RawMessage) *Cost {
	if len(metadata) == 0 {
		return nil
	}
	total, ok := metadataNumber(metadata, costTotalKey)
	if !ok {
		return nil
	}
	runs, ok := metadataNumber(metadata, costRunsKey)
	if !ok {
		return nil
	}
	// The unknown count is absent from an item all of whose runs were priced,
	// because bd drops a key it was never given rather than storing a zero.
	unknown, _ := metadataNumber(metadata, costUnknownKey)
	cost := Cost{TotalUSD: total, Runs: int(runs), UnknownRuns: int(unknown)}
	if cost.Validate() != nil {
		return nil
	}
	return &cost
}

func metadataNumber(metadata map[string]json.RawMessage, key string) (float64, bool) {
	raw, present := metadata[key]
	if !present {
		return 0, false
	}
	var value float64
	if err := json.Unmarshal(raw, &value); err != nil {
		// A metadata value that is not a number was not written by the harness,
		// or was written over by hand. Either way it is not a price to report.
		return 0, false
	}
	return value, true
}

func decodeJSON(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values are not supported")
		}
		return err
	}
	return nil
}

func (n NewWorkItem) validate() error {
	var problems []error
	if strings.TrimSpace(n.Title) == "" {
		problems = append(problems, errors.New("title is required"))
	}
	if strings.TrimSpace(n.Description) == "" {
		problems = append(problems, errors.New("description is required"))
	}
	if !issueTypePattern.MatchString(n.Type) {
		problems = append(problems, fmt.Errorf("invalid Beads issue type %q", n.Type))
	}
	if parent := strings.TrimSpace(n.Parent); parent != "" {
		if err := validateIssueID(parent); err != nil {
			problems = append(problems, fmt.Errorf("invalid parent: %w", err))
		}
	}
	if n.Priority != nil && (*n.Priority < 0 || *n.Priority > MaxPriority) {
		problems = append(problems, fmt.Errorf("priority %d is outside 0..%d", *n.Priority, MaxPriority))
	}
	problems = append(problems, executorProblem(n.Executor)...)
	problems = append(problems, parkingProblem(n.Parking)...)
	if len(problems) > 0 {
		return fmt.Errorf("invalid new work item: %w", errors.Join(problems...))
	}
	return nil
}

func (c WorkItemChange) validate() error {
	var problems []error
	if strings.TrimSpace(c.Title) == "" &&
		strings.TrimSpace(c.Description) == "" &&
		strings.TrimSpace(c.AppendNotes) == "" &&
		strings.TrimSpace(string(c.Executor)) == "" &&
		c.Priority == nil && c.Parent == nil && c.Parking == nil {
		problems = append(problems, errors.New("an update must change something"))
	}
	problems = append(problems, executorProblem(c.Executor)...)
	if c.Parking != nil {
		problems = append(problems, parkingProblem(*c.Parking)...)
	}
	if strings.ContainsAny(c.Title, "\r\n") {
		problems = append(problems, errors.New("title cannot span lines"))
	}
	if c.Priority != nil && (*c.Priority < 0 || *c.Priority > MaxPriority) {
		problems = append(problems, fmt.Errorf("priority %d is outside 0..%d", *c.Priority, MaxPriority))
	}
	if c.Parent != nil {
		// An empty parent detaches the item, which is a request bd accepts; any
		// other value has to name an item that could exist.
		if parent := strings.TrimSpace(*c.Parent); parent != "" {
			if err := validateIssueID(parent); err != nil {
				problems = append(problems, fmt.Errorf("invalid parent: %w", err))
			}
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("invalid work item update: %w", errors.Join(problems...))
	}
	return nil
}

// executorProblem refuses an executor the harness does not recognize, wherever
// it is written. It is the other half of reading an unrecognized marker as work
// no run may take: the read is deliberately permissive so a typo cannot cost a
// run, and this is what keeps that case from being how the marker is normally
// written. An absent executor is the ordinary case and never a problem.
func executorProblem(executor domain.WorkItemExecutor) []error {
	trimmed := domain.WorkItemExecutor(strings.TrimSpace(string(executor)))
	if trimmed == "" || trimmed.Valid() {
		return nil
	}
	named := make([]string, 0, len(domain.WorkItemExecutors))
	for _, known := range domain.WorkItemExecutors {
		named = append(named, fmt.Sprintf("%q", known))
	}
	// The bare marker is refused here like anything else that may not be written,
	// but it is not unrecognized — items marked before the marker named a role
	// carry it and are read exactly as they were — so what it is told is what it
	// is missing rather than that nobody knows the word.
	if trimmed == domain.WorkItemExecutorConversation {
		return []error{fmt.Errorf("executor %q does not say whose conversation carries the work; the executors that do are: %s",
			executor, strings.Join(named, ", "))}
	}
	return []error{fmt.Errorf("executor %q is not one the harness recognizes; the executors there are: %s",
		executor, strings.Join(named, ", "))}
}

// parkingProblem refuses a parking reason the tracker could not hold as one
// value on one line. An empty parking is a release and is never a problem.
//
// It is checked where it is written rather than folded on the way in, because
// what is being stored is somebody's account of a decision: a reason silently
// cut short, or one that reads as parked-for-nothing after the newlines are
// squeezed out, is worse than a refusal that says to write it shorter.
func parkingProblem(parking domain.WorkItemParking) []error {
	reason := parking.Reason()
	if reason == "" {
		return nil
	}
	var problems []error
	if strings.ContainsAny(reason, "\r\n") {
		problems = append(problems, errors.New("a parking reason cannot span lines"))
	}
	if len(reason) > domain.MaxWorkItemParkingBytes {
		problems = append(problems, fmt.Errorf("a parking reason of %d bytes exceeds the %d byte bound", len(reason), domain.MaxWorkItemParkingBytes))
	}
	return problems
}

// ValidateIssueID reports whether a string can name a Beads issue. It is
// exported so a caller can refuse an identifier it was handed before building a
// command around it, rather than discovering the problem as a bd failure.
func ValidateIssueID(id string) error {
	return validateIssueID(id)
}

func validateIssueID(id string) error {
	if !issueIDPattern.MatchString(id) {
		return fmt.Errorf("invalid Beads issue id %q", id)
	}
	return nil
}
