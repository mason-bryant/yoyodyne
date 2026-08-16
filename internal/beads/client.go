package beads

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"yoyodyne/internal/execution"
)

const defaultTimeout = 30 * time.Second

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

type Client struct {
	Runner  execution.ProcessRunner
	Binary  string
	Dir     string
	Timeout time.Duration
}

var issueIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

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
	if err := validateIssueID(id); err != nil {
		return err
	}
	if err := validateIssueID(blockerID); err != nil {
		return fmt.Errorf("invalid blocker: %w", err)
	}
	data, err := c.run(ctx, "dep", "add", id, blockerID, "--json")
	if err != nil {
		return err
	}
	var response dependencyResponse
	if err := decodeJSON(data, &response); err != nil {
		return fmt.Errorf("decode bd dependency response: %w", err)
	}
	if response.Status != "added" || response.IssueID != id || response.DependsOnID != blockerID {
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
	var rawItems []rawWorkItem
	if err := decodeJSON(data, &rawItems); err != nil {
		return WorkItem{}, fmt.Errorf("decode bd work item: %w", err)
	}
	if len(rawItems) != 1 {
		return WorkItem{}, fmt.Errorf("bd returned %d work items, want 1", len(rawItems))
	}
	raw := rawItems[0]
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

func validateIssueID(id string) error {
	if !issueIDPattern.MatchString(id) {
		return fmt.Errorf("invalid Beads issue id %q", id)
	}
	return nil
}
