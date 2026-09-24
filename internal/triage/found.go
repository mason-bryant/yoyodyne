package triage

import (
	"fmt"
	"strings"
	"time"
)

// Found is what the repository held of a stopped run's change when somebody
// looked: whether its branch and its worktree were there, and when that was
// asked. It is the one answer every surface that says what a stopped run
// preserved gives — the docket entry, the note on a released claim, `yoyo
// status`, and the hold the pull reads — so it is declared once, here, where the
// lowest of them can carry it.
//
// It exists because the run's own removal flags are not that answer. A flag is
// what a cleanup or a sweep remembered to write, and on 2026-09-23 run-838ffc48
// was read as having left no preserved change while its branch held the approved
// change at 8b06428b: the development manager crossed a cap on that reading and
// the re-run that followed was handed the wrong tip to start from. So what a
// surface says here it says from the repository, and says that it looked — or
// says it could not, and why, rather than falling back on the flags silently.
type Found struct {
	// At is when the repository was asked, so an answer read later says how old
	// it is rather than passing for one made now.
	At time.Time `json:"at"`
	// Branch and WorktreePath are what the run recorded, which is what was looked
	// for. An empty one was never made and is not reported either way.
	Branch       string `json:"branch,omitempty"`
	WorktreePath string `json:"worktree_path,omitempty"`
	// BranchThere and WorktreeThere are the answers.
	BranchThere   bool `json:"branch_there,omitempty"`
	WorktreeThere bool `json:"worktree_there,omitempty"`
	// Unchecked says why the two answers above are not the repository's: nothing
	// was wired to look, so they are the run's record; or the look failed, which
	// Unknown says, and they are nothing at all.
	Unchecked string `json:"unchecked,omitempty"`
	Unknown   bool   `json:"unknown,omitempty"`
}

// MaxFoundUncheckedBytes bounds the reason a look could not be made, which is an
// error's text and so has no bound of its own.
const MaxFoundUncheckedBytes = 1024

// Looked reports an answer that is the repository's rather than a record's.
func (f Found) Looked() bool { return strings.TrimSpace(f.Unchecked) == "" }

// Holds reports a change a reader has to treat as still there: one the look
// found, or one nothing could look for, which is held as if it were there
// because the other direction starts a fresh run over work that may not be gone.
func (f Found) Holds() bool { return f.Unknown || f.BranchThere || f.WorktreeThere }

// Recorded reports a run that made something to look for at all.
func (f Found) Recorded() bool { return f.Branch != "" || f.WorktreePath != "" }

// BranchState and WorktreeState are the words for one artifact: what was found
// and how that was established, together, because a branch checked and there is
// a different claim from a branch a record says nothing removed.
func (f Found) BranchState() string   { return f.state(f.BranchThere) }
func (f Found) WorktreeState() string { return f.state(f.WorktreeThere) }

func (f Found) state(there bool) string {
	switch {
	case f.Unknown:
		return "not checked: " + f.Unchecked
	case !f.Looked() && there:
		return "there as the run's record says, not checked: " + f.Unchecked
	case !f.Looked():
		return "gone as the run's record says, not checked: " + f.Unchecked
	case there:
		return "checked and there at " + f.stamp()
	default:
		return "checked and NOT there at " + f.stamp()
	}
}

func (f Found) stamp() string { return f.At.UTC().Format(time.RFC3339) }

// Describe is the whole answer as one clause, for a note or a line that has room
// for one sentence about what the run left.
func (f Found) Describe() string {
	if !f.Recorded() {
		return "the run recorded no branch and no worktree, so there was nothing of its change to look for"
	}
	var parts []string
	if f.Branch != "" {
		parts = append(parts, fmt.Sprintf("branch %s (%s)", f.Branch, f.BranchState()))
	}
	if f.WorktreePath != "" {
		parts = append(parts, fmt.Sprintf("worktree %s (%s)", f.WorktreePath, f.WorktreeState()))
	}
	return strings.Join(parts, "; ")
}

// Validate bounds what a durable record may carry of a look.
func (f Found) Validate() error {
	if len(f.Unchecked) > MaxFoundUncheckedBytes {
		return fmt.Errorf("unchecked is %d bytes, limit is %d", len(f.Unchecked), MaxFoundUncheckedBytes)
	}
	return nil
}
