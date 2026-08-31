package supervision

// The three readiness judgments, and what makes one stale.
//
// Each judgment belongs to exactly one role and says whether the work it names
// is ready from that role's standpoint. What makes them useful — and what makes
// them safe to hold advisory — is that each one records the revision of
// everything it was judged against. A judgment is stale when something it read
// has moved since, which is a comparison anybody can make from the records at
// any time rather than a flag somebody has to remember to set.
//
// Staleness is derived here for the same reason it is derived in the staleness
// package: a stored mark has to be written by whoever notices and cleared by
// whoever reconciles, so a process that dies between the two leaves a judgment
// that is stale and unmarked, or a mark that is now itself wrong. A comparison
// over the records cannot be missed and cannot disagree with what is on disk.
//
// Stale is not blocked. A goal reworded is frequently not a goal changed, and a
// loop that stopped every judgment on every edit would teach an operator not to
// edit. What a stale judgment produces here is a reason to wake the role that
// owns it. Whether the judgment actually changes is that role's to say.

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// MaxEvidenceBytes bounds what a judgment says it rests on. It is room for the
// reasoning and not for the documents themselves, which the references name.
const MaxEvidenceBytes = 8 << 10

// Judgment names one of the three readiness records. Each is owned by one role
// and judged only by that role, which Validate holds every record to: three
// independent judgments that any role could write would be one judgment with
// three names.
type Judgment string

const (
	// JudgmentProduct is the product manager's: intended outcome, approved goal,
	// priority, and product acceptance.
	JudgmentProduct Judgment = "product-readiness"
	// JudgmentArchitecture is the architect's: which designs, decisions,
	// invariants, and interfaces the work touches.
	JudgmentArchitecture Judgment = "architecture-readiness"
	// JudgmentDelivery is the development manager's: decomposition, dependencies,
	// conflict surfaces, complexity, and the execution profile.
	JudgmentDelivery Judgment = "delivery-readiness"
)

// Judgments are the three, in the order the loop reaches them: what the work is
// for, what it touches, and how it is to be delivered.
func Judgments() []Judgment {
	return []Judgment{JudgmentProduct, JudgmentArchitecture, JudgmentDelivery}
}

func (j Judgment) Valid() bool {
	switch j {
	case JudgmentProduct, JudgmentArchitecture, JudgmentDelivery:
		return true
	default:
		return false
	}
}

// Owner is the role whose judgment this is, and the role woken when it goes
// stale. It answers the empty role for anything that is not one of the three,
// which Validate reports as the invalid judgment it is.
func (j Judgment) Owner() domain.AgentRole {
	switch j {
	case JudgmentProduct:
		return domain.RoleProductManager
	case JudgmentArchitecture:
		return domain.RoleArchitect
	case JudgmentDelivery:
		return domain.RoleDevelopmentManager
	default:
		return ""
	}
}

// Disposition is what a judgment came to. The four values are the architecture
// disposition the management-loop-protocol design names, and the other two
// judgments are held to the same four rather than each inventing a vocabulary:
// one set of words is what lets a reader compare three judgments about one item
// without translating between them.
type Disposition string

const (
	// DispositionClear is ready from this role's standpoint.
	DispositionClear Disposition = "clear"
	// DispositionDesignNeeded is work that cannot proceed until something is
	// designed.
	DispositionDesignNeeded Disposition = "design-needed"
	// DispositionCrossCutting is work that reaches past what this item names, so
	// it is not ready alone.
	DispositionCrossCutting Disposition = "cross-cutting"
	// DispositionInvestigationNeeded is a judgment that needs evidence first.
	// Investigation is commissioned as bounded, read-only work: needing evidence
	// gains no role a write shell.
	DispositionInvestigationNeeded Disposition = "investigation-needed"
)

func (d Disposition) Valid() bool {
	switch d {
	case DispositionClear, DispositionDesignNeeded, DispositionCrossCutting, DispositionInvestigationNeeded:
		return true
	default:
		return false
	}
}

// Clear reports the one disposition that says the work is ready.
func (d Disposition) Clear() bool { return d == DispositionClear }

// Readiness is one role's judgment of one work item, at the revisions it read.
type Readiness struct {
	SchemaVersion int              `json:"schema_version"`
	ID            string           `json:"id"`
	ProductID     domain.ProductID `json:"product_id"`
	// Item is the admitted work this judges. A judgment is about one item: the
	// horizon a role plans over is bounded, and a judgment covering a whole
	// backlog is one nobody can act on item by item.
	Item        string      `json:"item"`
	Judgment    Judgment    `json:"judgment"`
	Disposition Disposition `json:"disposition"`
	// Evidence is what the judgment rests on, in the owning role's own words. It
	// is required: a disposition with nothing behind it is an assertion, and this
	// is advisory precisely because somebody downstream reads the reasoning.
	Evidence string `json:"evidence"`
	// Against are the durable things this was judged against, each at the
	// revision that was read. It is required and it is the whole of what makes
	// the judgment revision-aware.
	Against  []Reference      `json:"against"`
	JudgedBy domain.AgentRole `json:"judged_by"`
	JudgedAt time.Time        `json:"judged_at"`
}

var readinessIDPattern = regexp.MustCompile(`^readiness-[a-f0-9]{32}$`)

// NewReadinessID issues an identifier of the shape a store will name a file
// after.
func NewReadinessID() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate readiness id: %w", err)
	}
	return "readiness-" + hex.EncodeToString(raw), nil
}

// ValidReadinessID reports an identifier of the shape this package issues.
func ValidReadinessID(id string) bool { return readinessIDPattern.MatchString(id) }

// Moved is one thing a judgment or a request was read against that has changed
// since.
type Moved struct {
	Reference Reference `json:"reference"`
	// Now is what that thing is at currently, which with the reference's own
	// revision is the whole of what somebody needs to decide whether the change
	// matters.
	Now string `json:"now"`
}

// Moved reports what this judgment was made against that has since changed,
// given the current revision of everything by reference key.
func (r Readiness) Moved(current map[string]string) []Moved { return moved(r.Against, current) }

// Unknown reports the references this judgment was made against that the caller
// could say nothing current about. They are answered separately from the moved
// ones because they mean something different: not "this changed" but "nothing
// here can tell whether it did".
func (r Readiness) Unknown(current map[string]string) []Reference {
	return unknown(r.Against, current)
}

// Stale reports a judgment something under it has moved.
func (r Readiness) Stale(current map[string]string) bool { return len(r.Moved(current)) > 0 }

// Key names the judgment this is: one item, judged by one role. Two records
// sharing it are the same judgment made twice, and the later one is current.
func (r Readiness) Key() string { return r.Item + "/" + string(r.Judgment) }

// Validate reports every contract violation in the record at once.
func (r Readiness) Validate() error {
	var problems []error
	if r.SchemaVersion != SchemaVersion {
		problems = append(problems, fmt.Errorf("readiness schema version %d is not supported", r.SchemaVersion))
	}
	if !ValidReadinessID(r.ID) {
		problems = append(problems, fmt.Errorf("readiness id %q is invalid", r.ID))
	}
	if err := domain.ValidateIdentifier("product id", string(r.ProductID)); err != nil {
		problems = append(problems, err)
	}
	problems = append(problems,
		boundedText("item", r.Item, MaxReferenceBytes, true),
		boundedText("evidence", r.Evidence, MaxEvidenceBytes, true),
	)
	if !r.Judgment.Valid() {
		problems = append(problems, fmt.Errorf("judgment %q is not one of the three readiness judgments", r.Judgment))
	}
	if !r.Disposition.Valid() {
		problems = append(problems, fmt.Errorf("disposition %q is not one a judgment comes to", r.Disposition))
	}
	// The judgment is owned, and the record says who made it. A product judgment
	// signed by the architect is either a role acting outside its authority or a
	// record written wrong, and neither should load.
	if r.Judgment.Valid() && r.JudgedBy != r.Judgment.Owner() {
		problems = append(problems, fmt.Errorf("%s is the %s's judgment, and this one is signed by %q",
			r.Judgment, r.Judgment.Owner(), r.JudgedBy))
	}
	if len(r.Against) == 0 {
		problems = append(problems, errors.New("a judgment records what it was judged against; one against nothing can never be told from a current one"))
	}
	if len(r.Against) > MaxReferences {
		problems = append(problems, fmt.Errorf("%d references are recorded, limit is %d", len(r.Against), MaxReferences))
	}
	for i, reference := range r.Against {
		problems = append(problems, reference.validate(fmt.Sprintf("against[%d]", i)))
	}
	if r.JudgedAt.IsZero() {
		problems = append(problems, errors.New("judged at is required"))
	}
	if err := errors.Join(problems...); err != nil {
		return fmt.Errorf("invalid readiness: %w", err)
	}
	return nil
}

// Current reduces a set of judgments to the one that stands for each item and
// judgment: the latest by the moment it was made, and by identifier where two
// were made at the same instant, so two readings of one store agree. Records
// that are superseded are kept on disk and left out here — what was judged when
// is worth reading, and what is current is a different question.
func Current(records []Readiness) []Readiness {
	latest := make(map[string]Readiness, len(records))
	for _, record := range records {
		standing, seen := latest[record.Key()]
		if !seen || record.JudgedAt.After(standing.JudgedAt) ||
			(record.JudgedAt.Equal(standing.JudgedAt) && record.ID > standing.ID) {
			latest[record.Key()] = record
		}
	}
	current := make([]Readiness, 0, len(latest))
	for _, record := range latest {
		current = append(current, record)
	}
	SortReadiness(current)
	return current
}

// SortReadiness orders judgments the way they are read: by the item, then by
// the order the loop reaches the three judgments, then by identifier.
func SortReadiness(records []Readiness) {
	order := make(map[Judgment]int, len(Judgments()))
	for index, judgment := range Judgments() {
		order[judgment] = index
	}
	sort.SliceStable(records, func(first, second int) bool {
		left, right := records[first], records[second]
		if left.Item != right.Item {
			return left.Item < right.Item
		}
		if left.Judgment != right.Judgment {
			return order[left.Judgment] < order[right.Judgment]
		}
		return left.ID < right.ID
	})
}

// moved compares what was read against what is current now. A reference the
// caller knows nothing about is not a change: silence is not evidence that
// something held still, and reporting it as movement would fill the loop with
// staleness nobody can act on.
func moved(references []Reference, current map[string]string) []Moved {
	var changed []Moved
	for _, reference := range references {
		now, known := current[reference.Key()]
		if !known || now == reference.Revision {
			continue
		}
		changed = append(changed, Moved{Reference: reference, Now: now})
	}
	return changed
}

func unknown(references []Reference, current map[string]string) []Reference {
	var unread []Reference
	for _, reference := range references {
		if _, known := current[reference.Key()]; !known {
			unread = append(unread, reference)
		}
	}
	return unread
}
