package orchestrator

// Closing an item a conversation carries when what it asked for lands.
//
// A developer item closes when its change merges: the run claimed it, the
// reviewer approved it, and the reconciler reads the forge and closes the item
// with nobody typing anything. An item the architect's conversation carries had
// nothing of the kind. Its landing is a revision in a document the architect
// owns, and the only thing that ever read that revision back to the tracker was
// the product manager, closing the item on evidence some turns later —
// yoyodyne-ifd.282 at turn 440, yoyodyne-ifd.330 at turn 512, after a developer
// run had been spent on it. On the day this was written five more architect
// items (209.21, 294, 306, 313, 348) stood open with their revisions already in
// the designs; docs/diagnoses/yoyodyne-ifd-367-conversation-item-dispatched.md
// lists them.
//
// So the pass reads the landing. An item whose executor is a role's
// conversation is closed once a document that role owns carries a revision, by
// that role, whose reason opens with the item's identifier: "yoyodyne-ifd.330 -
// side conversations designed". Opening with it is the convention, and it is
// the whole of the judgement: a revision that merely mentions an item in
// passing — "published under yoyodyne-ifd.280 after three reviewer reports" —
// is a revision about something else, and an item with two deliverables of
// which the revision carries one is not closed on it. The architect's revision
// log is the architect saying the item's work is in the document, which is
// exactly the evidence the product manager was reading by hand.
//
// It reads the repository the pull is made from, which is the same tree every
// other reader of the artifact homes reads — the invariants delivered to a run,
// the documents a done-condition is checked against — and closes with a reason
// naming the document, the revision, and the convention, so a close that was
// wrong is one the product manager can read and reopen with a note.
//
// The reopen holds because the close is made once per revision rather than once
// per pull. Every pull reads every landing again, so a sweep with no memory
// would close a reopened item again a minute later and the note would be the
// only trace of the reopen. So the revision an item is closed on is written onto
// the item, in the tracker, before the close, and an entry still carrying that
// revision is one the harness already closed on it and somebody put back: it is
// left where they put it. A later revision opening with the same identifier is a
// new landing, and closes it again.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/artifact"
	"github.com/mason-bryant/yoyodyne/internal/backlog"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// LandingTracker is the tracker access one landing sweep needs: recording the
// revision an item is closed on, and closing it with the reason on it. It is
// satisfied by beads.Client.
type LandingTracker interface {
	RecordLanding(ctx context.Context, id, landing string) (beads.WorkItem, error)
	Complete(ctx context.Context, id, reason string) (beads.WorkItem, error)
}

// ConversationLander closes conversation-executed items whose landing is in
// the repository.
type ConversationLander struct {
	Tracker LandingTracker
	// Repository is the checkout the artifact homes are read from, and Product
	// is where they are.
	Repository string
	Product    config.Product
}

// LandedConversation is one item this sweep closed, and the revision it closed
// it on.
type LandedConversation struct {
	WorkItemID string                  `json:"work_item_id"`
	Executor   domain.WorkItemExecutor `json:"executor"`
	// Document is the repository-relative path of the document the revision is
	// in, and Revision is the revision's own timestamp and reason.
	Document    string    `json:"document"`
	RevisedAt   time.Time `json:"revised_at"`
	Reason      string    `json:"reason"`
	CloseReason string    `json:"close_reason"`
}

// Key is the revision as the tracker records it on the item: the document and
// the revision's time, which together name one revision and no other.
func (l LandedConversation) Key() string {
	return l.Document + "@" + l.RevisedAt.UTC().Format(time.RFC3339)
}

// LandingSweep is what one sweep did: the items it closed, the ones it found
// closed once already and reopened, and the ones it found landed and could not
// close.
type LandingSweep struct {
	Landed []LandedConversation `json:"landed,omitempty"`
	// Reopened is the entries whose landing this sweep found and left alone,
	// because the item already carries that revision as the one the harness
	// closed it on: somebody reopened it, and the reopen holds.
	Reopened []string `json:"reopened,omitempty"`
	// Problems name the items whose landing was found and whose close the
	// tracker refused, or the reading that failed. They are named rather than
	// counted because what a person does about one is specific to the item.
	Problems []string `json:"problems,omitempty"`
}

// Render is the sweep as a person reads it, one line per item.
func (s LandingSweep) Render() string {
	var rendered strings.Builder
	for _, landed := range s.Landed {
		fmt.Fprintf(&rendered, "%s was closed: its %s landed as the %s revision of %s\n",
			landed.WorkItemID, landed.Executor.Role(), landed.RevisedAt.UTC().Format("2006-01-02 15:04:05Z"), landed.Document)
	}
	return rendered.String()
}

func (l ConversationLander) validate() error {
	var problems []error
	if l.Tracker == nil {
		problems = append(problems, errors.New("a landing sweep requires a work tracker"))
	}
	if strings.TrimSpace(l.Repository) == "" {
		problems = append(problems, errors.New("a landing sweep requires the repository the artifact homes are read from"))
	}
	return errors.Join(problems...)
}

// Settle closes every entry among those given whose landing is in the
// repository, and reports what it did. Entries a developer run carries, and
// entries whose conversation's role owns no document, are passed over without
// the repository being read at all: a pull with nothing of the kind in its
// queue costs exactly what it cost before this existed.
//
// One reading of the artifact homes serves every entry. A reading that fails
// is reported and closes nothing, in the direction every other optional part
// of a pull fails in: an item closed on a document nobody could read would be
// the one thing worse than an item left open.
func (l ConversationLander) Settle(ctx context.Context, entries []backlog.Entry) (LandingSweep, error) {
	if err := l.validate(); err != nil {
		return LandingSweep{}, err
	}
	var candidates []backlog.Entry
	for _, entry := range entries {
		if landsInADocument(entry.Executor) {
			candidates = append(candidates, entry)
		}
	}
	if len(candidates) == 0 {
		return LandingSweep{}, nil
	}
	set, err := artifact.StoreFor(l.Repository, l.Product).Load()
	if err != nil {
		return LandingSweep{}, fmt.Errorf("read the artifact homes the landings are in: %w", err)
	}
	var sweep LandingSweep
	for _, entry := range candidates {
		// An entry carrying a landing is one the harness closed on that revision
		// and somebody reopened. The reopen is their decision and this is not a
		// wait: what closes it again is a later revision, and nothing else. So
		// only revisions after the recorded one are read for such an entry.
		closedOn, reopened := recordedLanding(entry.Landing)
		if reopened && closedOn.IsZero() {
			sweep.Reopened = append(sweep.Reopened, entry.ID)
			continue
		}
		landing, found := findLanding(set, entry, closedOn)
		if !found {
			if reopened {
				sweep.Reopened = append(sweep.Reopened, entry.ID)
			}
			continue
		}
		// The revision is written onto the item before the close, so that an item
		// the close reaches carries what it was closed on whatever the tracker
		// does next. A record that could not be written closes nothing: a close
		// with no record behind it is exactly the close a reopen cannot hold
		// against.
		if _, err := l.Tracker.RecordLanding(ctx, entry.ID, landing.Key()); err != nil {
			sweep.Problems = append(sweep.Problems, fmt.Sprintf("%s landed as the %s revision of %s and the revision could not be recorded on it, so it was not closed and stays in the queue: %v",
				entry.ID, landing.RevisedAt.UTC().Format(time.RFC3339), landing.Document, err))
			continue
		}
		if _, err := l.Tracker.Complete(ctx, entry.ID, landing.CloseReason); err != nil {
			problem := fmt.Sprintf("%s landed as the %s revision of %s and could not be closed, so it stays in the queue until it is closed by hand: %v",
				entry.ID, landing.RevisedAt.UTC().Format(time.RFC3339), landing.Document, err)
			// The record is taken back off an item the close never reached, so the
			// next pull tries again rather than reading the item as reopened. Where
			// even that fails the item is open and marked, which the next pull reads
			// as a reopen and leaves alone — so it is said here, once, in words that
			// name the marker somebody would have to clear.
			if _, err := l.Tracker.RecordLanding(ctx, entry.ID, ""); err != nil {
				problem += fmt.Sprintf("; and the revision recorded on it ahead of the close could not be cleared, so every later pull reads the item as reopened and leaves it: clear its %s metadata to let the harness try again: %v", beads.LandingKey, err)
			}
			sweep.Problems = append(sweep.Problems, problem)
			continue
		}
		sweep.Landed = append(sweep.Landed, landing)
	}
	return sweep, nil
}

// landsInADocument reports an executor whose conversation's landing could be a
// revision at all: a conversation with a role that owns some kind of artifact.
// The development manager's conversation owns none — its work is decomposition
// in the tracker — so an item it carries is never read for one.
func landsInADocument(executor domain.WorkItemExecutor) bool {
	role := executor.Role()
	if role == "" {
		return false
	}
	for _, kind := range artifact.Kinds() {
		if owner, placed := artifact.Owner(kind); placed && owner == role {
			return true
		}
	}
	return false
}

// recordedLanding reads the time of the revision an entry was already closed
// on, from the key RecordLanding wrote, and whether it carries one at all. A key
// that does not parse is reported as carried with no time, and the caller
// leaves such an item alone: something the harness wrote is on it, and closing
// over a record nobody can read is the wrong guess.
func recordedLanding(key string) (time.Time, bool) {
	if strings.TrimSpace(key) == "" {
		return time.Time{}, false
	}
	_, stamp, split := strings.Cut(key, "@")
	if !split {
		return time.Time{}, true
	}
	at, err := time.Parse(time.RFC3339, stamp)
	if err != nil {
		return time.Time{}, true
	}
	return at, true
}

// findLanding is the revision that lands one entry, where the set holds one: in
// a document the entry's role owns, made by that role, with a reason that opens
// with the entry's identifier, and later than the revision the entry was
// already closed on where it was. The earliest such revision is the landing
// where there are several, because the item landed when the first one did.
func findLanding(set artifact.Set, entry backlog.Entry, after time.Time) (LandedConversation, bool) {
	role := entry.Executor.Role()
	var (
		landing LandedConversation
		found   bool
	)
	for _, document := range set.Artifacts {
		if owner, placed := artifact.Owner(document.Kind); !placed || owner != role {
			continue
		}
		for _, revision := range document.Revisions {
			if revision.By != role || !opensWith(revision.Reason, entry.ID) || !revision.At.After(after) {
				continue
			}
			if found && !revision.At.Before(landing.RevisedAt) {
				continue
			}
			landing = LandedConversation{
				WorkItemID: entry.ID,
				Executor:   entry.Executor,
				Document:   document.Path,
				RevisedAt:  revision.At,
				Reason:     revision.Reason,
			}
			found = true
		}
	}
	if found {
		landing.CloseReason = landingCloseReason(landing)
	}
	return landing, found
}

// opensWith reports a revision reason that begins with the item's identifier
// as a whole word: "yoyodyne-ifd.330 - side conversations designed" opens with
// yoyodyne-ifd.330, and "yoyodyne-ifd.330.1 - the record and lease" does not.
// A quote or bracket ahead of the identifier is skipped, because the reason is
// YAML and a reason with a colon in it is written quoted.
func opensWith(reason, id string) bool {
	trimmed := strings.TrimLeft(strings.TrimSpace(reason), `"'([`)
	if !strings.HasPrefix(trimmed, id) {
		return false
	}
	rest := trimmed[len(id):]
	if rest == "" {
		return true
	}
	next := rest[0]
	return !(next == '.' && len(rest) > 1 && rest[1] >= '0' && rest[1] <= '9') &&
		next != '-' && next != '_' &&
		!(next >= '0' && next <= '9') && !(next >= 'a' && next <= 'z') && !(next >= 'A' && next <= 'Z')
}

// maxLandingReasonBytes bounds how much of the revision's own reason the close
// carries. The revision is the record; the close names it.
const maxLandingReasonBytes = 400

// landingCloseReason is what the tracker records about a close made here: which
// document, which revision, the convention it was read by, and what the
// revision said, so a close somebody disagrees with is one they can read and
// reopen with a note rather than one they have to reconstruct.
func landingCloseReason(landing LandedConversation) string {
	return fmt.Sprintf("Closed by the harness: the %s revision of %s at %s opens with this item's identifier, which is the %s recording that the item's work is in the document. The revision's reason: %s",
		landing.Executor.Role(), landing.Document, landing.RevisedAt.UTC().Format(time.RFC3339), landing.Executor.Role(), singleLine(landing.Reason, maxLandingReasonBytes))
}
