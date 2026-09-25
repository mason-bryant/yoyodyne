package runstate

// A queued merge's checks, as the forge last reported them.
//
// A merge the forge queued is waiting on the base branch's requirements, and
// until yoyodyne-ifd.429.16 the record said only that it was queued. Pull
// request 609 sat queued for 33 hours against a red build that way, and 713 sat
// 31 commits behind main failing two tests its change never touched, while the
// development manager waited on it because the record said the merge was
// queued. What the record was missing is the thing that decides whether a
// queued merge is ever going to land: its checks. The reconciling sweep reads
// them on every pass and writes them here, so every surface that says a merge
// is queued says what its checks are beside it.

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	// MaxRecordedFailingChecks bounds the failing checks one reading keeps. A
	// head failing more than this is red whatever the rest say.
	MaxRecordedFailingChecks = 20
	// MaxRecordedCheckPaths bounds the files kept for one failing check.
	MaxRecordedCheckPaths = 10
	// maxCheckNameBytes bounds one check's name.
	maxCheckNameBytes = 200
)

// PullRequestChecks is the forge's check state for a pull request's head, as
// the reconciling sweep last read it.
type PullRequestChecks struct {
	// HeadCommit is the head the checks ran on, which is what makes the reading
	// about one commit rather than about the request whatever it later carries.
	HeadCommit string         `json:"head_commit"`
	ReadAt     time.Time      `json:"read_at"`
	Failing    []FailingCheck `json:"failing,omitempty"`
	Pending    int            `json:"pending,omitempty"`
	Passing    int            `json:"passing,omitempty"`
	// BehindBy is how many commits the target branch carries that the head does
	// not, which is what an update onto the target would bring in.
	BehindBy int `json:"behind_by,omitempty"`
}

// FailingCheck is one check that failed on the head.
type FailingCheck struct {
	Name string `json:"name"`
	// Paths are the files the check's annotations named. OnChange is the part of
	// them the change itself touches, which is what makes the failure the
	// change's own: a failing check whose annotations name only files the change
	// does not touch failed on something the change did not bring.
	Paths    []string `json:"paths,omitempty"`
	OnChange []string `json:"on_change,omitempty"`
}

// Red reports a reading with a check that failed.
func (c PullRequestChecks) Red() bool { return len(c.Failing) > 0 }

// ChangeFails reports a failing check whose annotations name a file the change
// touches, which is the change's own failure rather than one it met.
func (c PullRequestChecks) ChangeFails() bool {
	for _, failing := range c.Failing {
		if len(failing.OnChange) > 0 {
			return true
		}
	}
	return false
}

// Describe is the one sentence every surface says of the reading: whether the
// checks pass, which failed and on what, how far behind the target the head
// is, and when this was read. It is here rather than per surface for the
// reason the outcome vocabulary is: the docket and the attention line must not
// word one reading two ways.
func (c PullRequestChecks) Describe(targetBranch string) string {
	target := targetBranch
	if strings.TrimSpace(target) == "" {
		target = "its target"
	}
	var standing string
	switch {
	case c.Red():
		named := make([]string, 0, len(c.Failing))
		for _, failing := range c.Failing {
			named = append(named, failing.describe())
		}
		standing = "failing: " + strings.Join(named, "; ")
	case c.Pending > 0:
		standing = fmt.Sprintf("%d still running, none failing", c.Pending)
	case c.Passing > 0:
		standing = "passing"
	default:
		standing = "none reported"
	}
	behind := fmt.Sprintf("level with %s", target)
	if c.BehindBy > 0 {
		behind = fmt.Sprintf("%d commit(s) behind %s", c.BehindBy, target)
	}
	head := c.HeadCommit
	if len(head) > 12 {
		head = head[:12]
	}
	return fmt.Sprintf("checks %s; head %s %s; read %s", standing, head, behind, c.ReadAt.UTC().Format(time.RFC3339))
}

func (f FailingCheck) describe() string {
	switch {
	case len(f.OnChange) > 0:
		return fmt.Sprintf("%s (on %s, which this change touches)", f.Name, strings.Join(f.OnChange, ", "))
	case len(f.Paths) > 0:
		return fmt.Sprintf("%s (on %s, which this change does not touch)", f.Name, strings.Join(f.Paths, ", "))
	default:
		return f.Name + " (naming no file)"
	}
}

// Validate rejects a reading that cannot describe a real one.
func (c PullRequestChecks) Validate() error {
	var problems []error
	if !commitPattern.MatchString(c.HeadCommit) {
		problems = append(problems, errors.New("checks head_commit is invalid"))
	}
	if c.ReadAt.IsZero() {
		problems = append(problems, errors.New("checks read_at is required"))
	}
	if c.Pending < 0 || c.Passing < 0 || c.BehindBy < 0 {
		problems = append(problems, errors.New("checks counts cannot be negative"))
	}
	if len(c.Failing) > MaxRecordedFailingChecks {
		problems = append(problems, fmt.Errorf("%d failing checks are recorded, which exceeds the bound of %d", len(c.Failing), MaxRecordedFailingChecks))
	}
	for index, failing := range c.Failing {
		if strings.TrimSpace(failing.Name) == "" || len(failing.Name) > maxCheckNameBytes {
			problems = append(problems, fmt.Errorf("checks failing[%d] name must be present and at most %d bytes", index, maxCheckNameBytes))
		}
		if len(failing.Paths) > MaxRecordedCheckPaths || len(failing.OnChange) > MaxRecordedCheckPaths {
			problems = append(problems, fmt.Errorf("checks failing[%d] names more than %d files", index, MaxRecordedCheckPaths))
		}
	}
	return errors.Join(problems...)
}
