package beads

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"

	"yoyodyne/internal/execution"
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
}

type Dependency struct {
	ID     string
	Type   string
	Status string
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
	if notes := strings.TrimSpace(item.Notes); notes != "" {
		args = append(args, "--notes="+notes)
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
			item.Dependencies = append(item.Dependencies, Dependency{ID: id, Type: dependencyType, Status: dependency.Status})
		}
	}
	return item, nil
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
		c.Priority == nil && c.Parent == nil {
		problems = append(problems, errors.New("an update must change something"))
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
