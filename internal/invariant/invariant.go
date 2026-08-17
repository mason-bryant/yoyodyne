// Package invariant holds the durable architectural constraints the architect
// owns: the cross-cutting rules a work item must not break even when its own
// change looks correct.
//
// An invariant is deliberately not prose in a work item. It is an artifact with
// a stable identity, the constraint it asserts, why it holds, what established
// it, and a recorded lifecycle — because a constraint transcribed by hand into
// one bead constrains that bead alone, and an invariant that no longer holds is
// worse than none at all. Two invariants of this repository's own were learned
// that way: the first attempt at yoyodyne-ifd.2.7 bypassed the reservation that
// was the only thing keeping two processes off one in-flight item, and the first
// attempt at yoyodyne-ifd.2.8 built a second resume path beside the one ifd.2.7
// had just established. Both were caught late, and both were prevented on the
// retry only because somebody remembered to write the constraint into the bead.
//
// Ownership follows the model the design already sets out for upstream
// artifacts: only the architect creates, amends, or retires one, and every other
// role proposes. Authorize is where that is enforced, and it bounds every
// authorized way to write an invariant, because Store is the only thing that
// writes one. What it does not bound is a developer with a shell in its
// worktree, which is the same gap the design records for pushing and merging:
// that role is held by its contract and caught by the reviewer rather than
// stopped here.
//
// The failure to design against is delivery rather than authorship. An invariant
// nobody puts in front of a developer constrains nothing, so a Set selects the
// invariants relevant to one work item and renders them for a prompt, and the
// harness delivers them to the developer's context and the reviewer's evidence
// without depending on whoever wrote the bead. Selection is what keeps that
// affordable: every invariant in every prompt is both expensive and ignorable.
package invariant

import (
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"
	"time"

	"yoyodyne/internal/domain"
)

// Status is where an invariant is in its lifecycle. Retirement is explicit
// because the alternative — deleting the file — leaves nothing saying the
// constraint stopped applying, and a developer who read it last month has no way
// to find out.
type Status string

const (
	StatusActive  Status = "active"
	StatusRetired Status = "retired"
)

func (s Status) Valid() bool {
	return s == StatusActive || s == StatusRetired
}

// Action is what one recorded revision did. An invariant is amended in place as
// the system changes, unlike the decision record it was extracted from, so the
// revisions are what say when it changed and why.
type Action string

const (
	ActionCreated Action = "created"
	ActionAmended Action = "amended"
	ActionRetired Action = "retired"
)

func (a Action) Valid() bool {
	return a == ActionCreated || a == ActionAmended || a == ActionRetired
}

// Bounds on one invariant. They are generous for the tightly stated constraint
// an invariant is and small enough that no single file can crowd a delivered set
// out of a prompt.
const (
	MaxTitleBytes     = 200
	MaxStatementBytes = 4 << 10
	MaxRationaleBytes = 4 << 10
	MaxReasonBytes    = 1 << 10
	MaxScopeEntries   = 20
)

// Revision is one recorded change to an invariant: what happened, which role's
// authority it happened under, when, and why. The role is recorded rather than
// assumed because the authorization boundary is only auditable if the artifact
// says who exercised it.
type Revision struct {
	Action Action           `yaml:"action" json:"action"`
	By     domain.AgentRole `yaml:"by" json:"by"`
	At     time.Time        `yaml:"at" json:"at"`
	Reason string           `yaml:"reason" json:"reason"`
}

// Invariant is one durable architectural constraint.
type Invariant struct {
	// ID is the stable identity. It is also the file's name, so the identity and
	// the location can never disagree and two files cannot claim one invariant.
	ID    string `yaml:"id" json:"id"`
	Title string `yaml:"title" json:"title"`
	// EstablishedBy names the work items or designs that produced this
	// constraint. It is required: a constraint with no origin cannot be judged
	// still relevant, which is what makes retirement possible at all.
	EstablishedBy []string `yaml:"established_by" json:"established_by"`
	// Scope names the repository paths this invariant constrains. An empty scope
	// is repository-wide and reaches every work item; a scoped invariant reaches
	// the items and changes that touch what it names. See Set.Select.
	Scope  []string `yaml:"scope,omitempty" json:"scope,omitempty"`
	Status Status   `yaml:"status" json:"status"`
	// Statement is what must hold, and Rationale is why. They are separate
	// because a developer is held to the first and persuaded by the second, and a
	// statement diluted with its own reasoning is one nobody can be held to.
	Statement string     `yaml:"-" json:"statement"`
	Rationale string     `yaml:"-" json:"rationale"`
	Revisions []Revision `yaml:"revisions" json:"revisions"`
	// Path is the repository-relative file this was read from, absent for an
	// invariant that has not been written yet.
	Path string `yaml:"-" json:"path,omitempty"`
}

// Active reports an invariant that still constrains work. Only active
// invariants are ever delivered: a retired one is kept so the record says the
// constraint was lifted, not so it keeps being enforced.
func (i Invariant) Active() bool {
	return i.Status == StatusActive
}

// Retirement returns the revision that retired this invariant, and whether it
// has one. A retired invariant with no such revision is rejected by Validate, so
// this is how a reader gets the reason without re-deriving it.
func (i Invariant) Retirement() (Revision, bool) {
	for index := len(i.Revisions) - 1; index >= 0; index-- {
		if i.Revisions[index].Action == ActionRetired {
			return i.Revisions[index], true
		}
	}
	return Revision{}, false
}

// Validate reports every contract violation in the invariant at once.
func (i Invariant) Validate() error {
	var problems []error
	if err := domain.ValidateIdentifier("invariant id", i.ID); err != nil {
		problems = append(problems, err)
	}
	switch title := strings.TrimSpace(i.Title); {
	case title == "":
		problems = append(problems, errors.New("title is required"))
	case len(title) > MaxTitleBytes:
		problems = append(problems, fmt.Errorf("title is %d bytes, limit is %d", len(title), MaxTitleBytes))
	}
	if !i.Status.Valid() {
		problems = append(problems, fmt.Errorf("status %q must be %q or %q", i.Status, StatusActive, StatusRetired))
	}
	switch statement := strings.TrimSpace(i.Statement); {
	case statement == "":
		problems = append(problems, errors.New("the statement of what must hold is required"))
	case len(statement) > MaxStatementBytes:
		problems = append(problems, fmt.Errorf("statement is %d bytes, limit is %d", len(statement), MaxStatementBytes))
	}
	switch rationale := strings.TrimSpace(i.Rationale); {
	case rationale == "":
		problems = append(problems, errors.New("the rationale is required; an invariant nobody can weigh is one nobody can retire"))
	case len(rationale) > MaxRationaleBytes:
		problems = append(problems, fmt.Errorf("rationale is %d bytes, limit is %d", len(rationale), MaxRationaleBytes))
	}
	if len(i.EstablishedBy) == 0 {
		problems = append(problems, errors.New("established_by must name the work item or design this constraint came from"))
	}
	for index, reference := range i.EstablishedBy {
		if strings.TrimSpace(reference) == "" {
			problems = append(problems, fmt.Errorf("established_by[%d] is empty", index))
		}
	}
	problems = append(problems, i.scopeProblems()...)
	if len(i.Revisions) == 0 {
		problems = append(problems, errors.New("revisions must record at least the creation of this invariant"))
	}
	for index, revision := range i.Revisions {
		if err := revision.Validate(); err != nil {
			problems = append(problems, fmt.Errorf("revisions[%d]: %w", index, err))
		}
	}
	// Retirement is explicit and recorded, so the two halves of it have to agree:
	// a file that says retired without the revision that retired it records no
	// decision, and a recorded retirement on an active invariant describes a
	// constraint that was lifted and is somehow still enforced.
	_, retired := i.Retirement()
	if i.Status == StatusRetired && !retired {
		problems = append(problems, errors.New("a retired invariant must record the revision that retired it"))
	}
	if i.Status == StatusActive && retired {
		problems = append(problems, errors.New("an active invariant cannot record a retirement"))
	}
	if err := errors.Join(problems...); err != nil {
		return fmt.Errorf("invalid invariant %q: %w", i.ID, err)
	}
	return nil
}

// scopeProblems reports what makes a declared scope unusable. Scope entries are
// repository-relative paths, held to that here rather than at delivery time: a
// scope that could name something outside the repository would be matched
// against text and quietly never fire.
func (i Invariant) scopeProblems() []error {
	var problems []error
	if len(i.Scope) > MaxScopeEntries {
		problems = append(problems, fmt.Errorf("scope names %d paths, limit is %d; an invariant this broad is repository-wide", len(i.Scope), MaxScopeEntries))
	}
	for index, entry := range i.Scope {
		trimmed := strings.TrimSpace(entry)
		clean := path.Clean(trimmed)
		switch {
		case trimmed == "":
			problems = append(problems, fmt.Errorf("scope[%d] is empty", index))
		case strings.HasPrefix(trimmed, "/"), clean == ".", clean == "..", strings.HasPrefix(clean, "../"):
			problems = append(problems, fmt.Errorf("scope[%d] %q must be a repository-relative path", index, entry))
		}
	}
	return problems
}

// Validate reports every contract violation in the revision at once.
func (r Revision) Validate() error {
	var problems []error
	if !r.Action.Valid() {
		problems = append(problems, fmt.Errorf("action %q must be %q, %q, or %q", r.Action, ActionCreated, ActionAmended, ActionRetired))
	}
	// Every revision was made under the architect's authority, because no other
	// role has any. Recording the role is what makes that checkable afterwards
	// rather than merely intended.
	if err := Authorize(r.By); err != nil {
		problems = append(problems, err)
	}
	if r.At.IsZero() {
		problems = append(problems, errors.New("at is required"))
	}
	switch reason := strings.TrimSpace(r.Reason); {
	case reason == "":
		problems = append(problems, errors.New("reason is required"))
	case len(reason) > MaxReasonBytes:
		problems = append(problems, fmt.Errorf("reason is %d bytes, limit is %d", len(reason), MaxReasonBytes))
	}
	return errors.Join(problems...)
}

// ErrUnauthorized is what every unauthorized mutation returns, so a caller can
// tell a refused authority from a malformed invariant.
var ErrUnauthorized = errors.New("only the architect may create, amend, or retire an architectural invariant")

// Authorize reports whether a role may create, amend, or retire an invariant.
// The architect owns them and nobody else does: a developer that found a
// constraint reports it and a reviewer that found a violated one files a
// finding, and both propose rather than write. Being in Go rather than only in a
// prompt is what makes this a rule about the mutation instead of an instruction
// a well-behaved model happens to respect — for every mutation that comes
// through this package, which is every authorized one there is.
func Authorize(role domain.AgentRole) error {
	if role == domain.RoleArchitect {
		return nil
	}
	if strings.TrimSpace(string(role)) == "" {
		return fmt.Errorf("%w; no role was named", ErrUnauthorized)
	}
	return fmt.Errorf("%w; %q may propose one instead", ErrUnauthorized, role)
}

// Problem names one file in the invariants directory that could not be read as
// an invariant, and says why. Unlike a product specification, which is loaded as
// written and reported alongside, an invariant that cannot be understood is not
// delivered at all: half a constraint is not something a developer can be held
// to. It is reported everywhere the set is used so the gap is visible rather
// than silent.
type Problem struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

func (p Problem) String() string {
	return p.Path + ": " + p.Reason
}

// Set is every invariant the repository records, split by lifecycle, plus the
// files that could not be read as one.
type Set struct {
	// Directory is the repository-relative directory this was loaded from,
	// carried so what is delivered can say where it came from.
	Directory string
	Active    []Invariant
	Retired   []Invariant
	Problems  []Problem
}

// Find returns the invariant with an ID, active or retired.
func (s Set) Find(id string) (Invariant, bool) {
	for _, group := range [][]Invariant{s.Active, s.Retired} {
		for _, candidate := range group {
			if candidate.ID == id {
				return candidate, true
			}
		}
	}
	return Invariant{}, false
}

// All returns every recorded invariant in one stable order, active first.
func (s Set) All() []Invariant {
	all := make([]Invariant, 0, len(s.Active)+len(s.Retired))
	all = append(all, s.Active...)
	all = append(all, s.Retired...)
	return all
}

// MaxDeliveredBytes bounds the rendered invariants one prompt carries. It is
// reached only by a repository that has accumulated far more than the rare,
// consequential constraints an invariant is meant to be, and reaching it is
// reported rather than silently trimmed.
const MaxDeliveredBytes = 32 << 10

// Delivery is the invariants selected for one work item, ready to be rendered
// into a prompt. It carries what it left out as well as what it selected,
// because a reader that cannot tell a narrow set from a truncated one will treat
// both as complete.
type Delivery struct {
	Directory string      `json:"directory"`
	Selected  []Invariant `json:"selected,omitempty"`
	// Considered is how many active invariants the repository holds, so a
	// delivery of two out of three says so.
	Considered int `json:"considered"`
	// Omitted names invariants that matched but did not fit the byte bound.
	Omitted  []string  `json:"omitted,omitempty"`
	Problems []Problem `json:"problems,omitempty"`
}

// IDs lists the delivered invariants, which is the audit record of what
// constrained a change.
func (d Delivery) IDs() []string {
	ids := make([]string, 0, len(d.Selected))
	for _, delivered := range d.Selected {
		ids = append(ids, delivered.ID)
	}
	return ids
}

// Empty reports a delivery with nothing to say: no invariant selected, none
// omitted, and no unreadable file to warn about. Nothing is rendered for one,
// because a prompt section announcing that a repository has no invariants
// teaches every agent to skim the heading.
func (d Delivery) Empty() bool {
	return len(d.Selected) == 0 && len(d.Omitted) == 0 && len(d.Problems) == 0
}

// Select picks the invariants relevant to one work item out of the set. The
// evidence is whatever text says what the work touches — the work item's own
// prose, and for a reviewer the change as well.
//
// Two rules decide relevance, and the split is the point. An invariant with no
// declared scope is repository-wide and always delivered, which is what the rare
// and consequential ones are. A scoped invariant is delivered when the evidence
// names a path it constrains, so a work item about one package is not handed
// every constraint the repository has ever recorded.
//
// The limit of that is real and is stated wherever the delivery is rendered: an
// invariant scoped to code a work item never mentions does not reach the
// developer up front. It reaches the reviewer, whose evidence includes what the
// change actually touched, which is the later of the two gates rather than
// neither.
func (s Set) Select(evidence ...string) Delivery {
	delivery := Delivery{Directory: s.Directory, Considered: len(s.Active), Problems: s.Problems}
	haystack := strings.ToLower(strings.Join(evidence, "\n"))
	matched := make([]Invariant, 0, len(s.Active))
	for _, candidate := range s.Active {
		if len(candidate.Scope) == 0 || scopeMatches(candidate.Scope, haystack) {
			matched = append(matched, candidate)
		}
	}
	// Repository-wide invariants come first: they apply to every change, so they
	// are the ones that must survive a bound the rest may not fit inside.
	sort.SliceStable(matched, func(first, second int) bool {
		wideFirst, wideSecond := len(matched[first].Scope) == 0, len(matched[second].Scope) == 0
		if wideFirst != wideSecond {
			return wideFirst
		}
		return matched[first].ID < matched[second].ID
	})
	bytes := 0
	for _, selected := range matched {
		rendered := len(renderInvariant(selected))
		if bytes+rendered > MaxDeliveredBytes {
			delivery.Omitted = append(delivery.Omitted, selected.ID)
			continue
		}
		bytes += rendered
		delivery.Selected = append(delivery.Selected, selected)
	}
	return delivery
}

// scopeMatches reports whether evidence names anything an invariant constrains.
// The match is textual on purpose: what the harness has before a change exists
// is prose, and a work item that names `internal/runstate` is saying which code
// it is about whether or not it says so in a machine-readable field. The
// comparison is over slash paths, so scope written for this repository matches
// evidence produced on any platform.
func scopeMatches(scope []string, loweredEvidence string) bool {
	for _, entry := range scope {
		clean := strings.ToLower(path.Clean(strings.TrimSpace(strings.ReplaceAll(entry, "\\", "/"))))
		if clean == "" || clean == "." {
			continue
		}
		if strings.Contains(loweredEvidence, clean) {
			return true
		}
	}
	return false
}

// Text renders a delivery as the prompt section every role receives. The framing
// is shared deliberately: what an invariant is, who owns it, and how complete
// the set is are the same facts for a developer and a reviewer, and what each of
// them must do about one belongs in that role's own immutable contract rather
// than in evidence.
func (d Delivery) Text() string {
	if d.Empty() {
		return ""
	}
	var rendered strings.Builder
	rendered.WriteString("# Architectural invariants\n\n")
	fmt.Fprintf(&rendered, `These are durable constraints this repository holds itself to, supplied by the
harness from %s rather than by whoever wrote the work item. The architect owns
them: no other role creates, amends, or retires one, and work that needs one
changed proposes the change instead of editing it.

They are here because a change whose own work is correct can still break
something outside its scope. Each one is a constraint on this change, not
advice, and it holds even where the work item says nothing about it.

`, d.Directory)
	if len(d.Selected) == 0 {
		// A delivery reaches here only because something is missing from it, so it
		// says that rather than presenting an empty set as the constraints in force.
		fmt.Fprintf(&rendered, "None of the %d active invariant(s) recorded here matched, and what is named\nbelow was not read at all. Treat the set as unknown rather than as empty.\n\n", d.Considered)
	} else {
		fmt.Fprintf(&rendered, "%d of the %d active invariant(s) recorded here were selected as relevant.\n", len(d.Selected), d.Considered)
		rendered.WriteString(`Only the ones that matched are below, so this is not the whole set: an
invariant scoped to code nothing here named still holds, and is not shown.

`)
	}
	for _, delivered := range d.Selected {
		rendered.WriteString(renderInvariant(delivered))
	}
	if len(d.Omitted) > 0 {
		rendered.WriteString("\n## Invariants omitted for size\n\n")
		for _, id := range d.Omitted {
			rendered.WriteString("- " + id + "\n")
		}
		rendered.WriteString("\nThese matched and did not fit. Treat each one as unread rather than as absent.\n")
	}
	if len(d.Problems) > 0 {
		rendered.WriteString("\n## Invariants that could not be read\n\n")
		for _, problem := range d.Problems {
			rendered.WriteString("- " + problem.String() + "\n")
		}
		rendered.WriteString(`
An invariant that cannot be read is not shown above, because half a constraint
is not one anybody can be held to. The set is incomplete by exactly these, and
they need the architect.
`)
	}
	return rendered.String()
}

// renderInvariant renders one invariant for a prompt. The scope is stated even
// when it is the whole repository, because "this applies everywhere" and "this
// applies to the package you are in" are different instructions.
func renderInvariant(delivered Invariant) string {
	var rendered strings.Builder
	fmt.Fprintf(&rendered, "## %s: %s\n\n", delivered.ID, strings.TrimSpace(delivered.Title))
	fmt.Fprintf(&rendered, "Scope: %s\n", renderScope(delivered.Scope))
	fmt.Fprintf(&rendered, "Established by: %s\n\n", strings.Join(delivered.EstablishedBy, ", "))
	rendered.WriteString("Must hold:\n\n")
	rendered.WriteString(strings.TrimSpace(delivered.Statement))
	rendered.WriteString("\n\nWhy:\n\n")
	rendered.WriteString(strings.TrimSpace(delivered.Rationale))
	rendered.WriteString("\n\n")
	return rendered.String()
}

func renderScope(scope []string) string {
	if len(scope) == 0 {
		return "the whole repository"
	}
	return strings.Join(scope, ", ")
}
