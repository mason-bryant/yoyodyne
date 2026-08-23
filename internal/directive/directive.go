// Package directive is what the operator told an agent to do, recorded where
// every agent has to meet it.
//
// The design has said from the beginning that a directive applies regardless of
// which agent received it, that it stays discoverable until it is superseded or
// retired, and that one which changes a governed artifact or which nobody can
// act on unambiguously pauses the work it affects until the operator settles it.
// What existed was only the first half of the first sentence: direction could be
// written down beside one work item, and a directive that changed what an
// in-flight item was supposed to be reached nothing. The work carried on against
// the intent it was given.
//
// A directive that is durably recorded and enforces nothing is the failure this
// package exists to end, so a record here carries the two things enforcement
// needs and nothing decorative: what work it affects, and what is unresolved
// about it. The run pipeline reads both — before it starts a run, before it
// resumes one, and before it puts a change through the gate — and a directive
// that pauses work is why a run stops short of finishing rather than something
// noted after it finished anyway.
//
// Nothing here is attached to whoever received the directive. The receiving role
// is recorded, because "the reviewer was told this" is worth knowing, but it is
// an attribute of the record rather than where the record lives: a directive
// kept inside the conversation that heard it, or inside the run that was told
// it, would reach exactly that conversation or that run.
package directive

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// SchemaVersion is versioned independently of run, conversation, and report
// state. A directive is none of those: it outlives every run it pauses, it is
// revised exactly once when it is resolved, and it belongs to the product rather
// than to any invocation.
const SchemaVersion = 1

const (
	// MaxTextBytes bounds what the operator said, MaxUnresolvedBytes bounds what
	// has to be settled about it, and MaxResolutionBytes bounds how it was
	// settled. All three are generous for the paragraph each actually is and
	// small enough that nothing can push a document into the record.
	MaxTextBytes       = 4 << 10
	MaxUnresolvedBytes = 4 << 10
	MaxResolutionBytes = 4 << 10
	// maxLineBytes bounds the values that are read as one line: the artifact a
	// directive changes, and each work item in its scope.
	maxLineBytes = 200
	// maxScopeItems bounds how much work one directive may name. A directive
	// that has to list more work than this is one whose scope is really "all of
	// it", which is what an unscoped directive already says.
	maxScopeItems = 50
)

// Kind is what sort of directive this is, which is the whole of what decides
// whether work stops for it. The three are kept apart rather than flattened into
// a single "important" flag because they are settled by different acts: an
// operational directive is settled by being carried out, an artifact-changing
// one by somebody deciding the change to the canonical document, and an
// ambiguous one by the operator saying what they meant.
type Kind string

const (
	// KindOperational is a directive that takes effect immediately. It changes
	// how work is done rather than what the work is, so nothing waits for it and
	// nothing about it is unresolved.
	KindOperational Kind = "operational"
	// KindArtifact changes a governed artifact: the brief, a goal, a design, a
	// specification. Work derived from that artifact cannot be judged against
	// intent that is being rewritten underneath it, so it pauses until the change
	// is decided and the canonical chain is consistent again.
	KindArtifact Kind = "artifact"
	// KindAmbiguous is a directive nobody can act on without deciding something
	// the operator did not. Work that would be done differently depending on the
	// answer must not be done while nobody has it, so it pauses until the
	// operator answers.
	KindAmbiguous Kind = "ambiguous"
)

// kinds lists the kinds in the order the contract states them, so a refusal
// names exactly what was available.
var kinds = []Kind{KindOperational, KindArtifact, KindAmbiguous}

func (k Kind) Valid() bool {
	for _, kind := range kinds {
		if k == kind {
			return true
		}
	}
	return false
}

// Pauses reports the kinds that stop work. It is a property of the kind rather
// than of one directive because it is the definition of the kind: an operational
// directive that paused work, or an ambiguous one that did not, would be
// misfiled rather than unusual.
func (k Kind) Pauses() bool {
	return k == KindArtifact || k == KindAmbiguous
}

// Headline is what a paused run and an operator listing are both told this kind
// is. Each says something different, because what settles them differs.
func (k Kind) Headline() string {
	switch k {
	case KindOperational:
		return "an operational directive, in effect from the moment it was recorded"
	case KindArtifact:
		return "a directive that changes a governed artifact, so work derived from it waits until the change is decided"
	case KindAmbiguous:
		return "a directive nobody can act on unambiguously, so work it affects waits until the operator answers"
	default:
		return "a directive the harness does not recognize"
	}
}

// Directive is one durable user directive.
type Directive struct {
	SchemaVersion int              `json:"schema_version"`
	ID            string           `json:"id"`
	ProductID     domain.ProductID `json:"product_id"`
	Kind          Kind             `json:"kind"`
	// ReceivedBy is the role that was told this. It is recorded rather than acted
	// on: which agent heard a directive decides nothing about where it reaches,
	// and this is here so an operator can ask who they said it to.
	ReceivedBy domain.AgentRole `json:"received_by"`
	ReceivedAt time.Time        `json:"received_at"`
	// Text is what the operator said, in their words.
	Text string `json:"text"`
	// Artifact names the governed artifact this changes. It is required on an
	// artifact-changing directive and refused on the others: a directive that
	// names a document it is not changing would pause work for a reason that is
	// not the one recorded.
	Artifact string `json:"artifact,omitempty"`
	// Unresolved is what has to be settled before the work this affects can carry
	// on: the question the operator has to answer, or the artifact change
	// somebody has to decide. It is required on every directive that pauses work,
	// because a pause that cannot say what it is waiting for is one nobody can
	// lift.
	Unresolved string `json:"unresolved,omitempty"`
	// Scope names the work items this affects. An empty scope means every item,
	// which is the safe reading rather than a lazy one: a directive that rewrites
	// the brief and names nothing affects whatever was derived from the brief,
	// and the harness cannot yet say which work that is. An operator who knows
	// better narrows it.
	Scope []string `json:"scope,omitempty"`
	// Resolution is how the directive was settled, and ResolvedAt is when. They
	// are written together and only once: a resolved directive stops pausing
	// work, and what it was resolved with is the record of why the work resumed.
	Resolution string     `json:"resolution,omitempty"`
	ResolvedAt *time.Time `json:"resolved_at,omitempty"`
}

var idPattern = regexp.MustCompile(`^directive-[a-f0-9]{32}$`)

func NewID() (string, error) {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("generate directive id: %w", err)
	}
	return "directive-" + hex.EncodeToString(bytes), nil
}

// ValidID reports an identifier of the shape this package issues. A store names
// a file after it, so it is checked before anything built from outside is used
// as a path.
func ValidID(id string) bool { return idPattern.MatchString(id) }

// Validate reports every contract violation in the directive at once.
func (d Directive) Validate() error {
	var problems []error
	if d.SchemaVersion != SchemaVersion {
		problems = append(problems, fmt.Errorf("schema_version must be %d", SchemaVersion))
	}
	if !idPattern.MatchString(d.ID) {
		problems = append(problems, errors.New("id is invalid"))
	}
	if err := domain.ValidateIdentifier("product id", string(d.ProductID)); err != nil {
		problems = append(problems, err)
	}
	if !d.Kind.Valid() {
		problems = append(problems, fmt.Errorf("kind %q is not a directive kind; the kinds are %s", d.Kind, strings.Join(kindNames(), ", ")))
	}
	if err := domain.ValidateIdentifier("received by", string(d.ReceivedBy)); err != nil {
		problems = append(problems, err)
	}
	if d.ReceivedAt.IsZero() {
		problems = append(problems, errors.New("received_at is required"))
	}
	problems = append(problems, boundedText("text", d.Text, MaxTextBytes, true))
	// The artifact is what says which case an artifact-changing directive is
	// about, so it is required there and refused everywhere else.
	if d.Kind == KindArtifact {
		problems = append(problems, boundedLine("artifact", d.Artifact, true))
	} else if strings.TrimSpace(d.Artifact) != "" {
		problems = append(problems, fmt.Errorf("only an %s directive names an artifact", KindArtifact))
	}
	// A pause that cannot say what it is waiting for is a pause nobody can lift,
	// which is the same dead end as a directive that enforces nothing.
	if d.Kind.Pauses() {
		problems = append(problems, boundedText("unresolved", d.Unresolved, MaxUnresolvedBytes, true))
	} else if strings.TrimSpace(d.Unresolved) != "" {
		problems = append(problems, fmt.Errorf("an %s directive has nothing unresolved; it is in effect already", KindOperational))
	}
	if len(d.Scope) > maxScopeItems {
		problems = append(problems, fmt.Errorf("scope names %d work items, limit is %d; a directive that affects more than that is unscoped", len(d.Scope), maxScopeItems))
	}
	for index, scoped := range d.Scope {
		problems = append(problems, boundedLine(fmt.Sprintf("scope[%d]", index), scoped, true))
	}
	// A resolution and the time it was made are one fact. Either alone describes
	// a directive that was half settled, which is not a state anything can act on.
	switch {
	case d.ResolvedAt != nil && d.ResolvedAt.IsZero():
		problems = append(problems, errors.New("resolved_at cannot be the zero time"))
	case d.ResolvedAt != nil:
		problems = append(problems, boundedText("resolution", d.Resolution, MaxResolutionBytes, true))
	case strings.TrimSpace(d.Resolution) != "":
		problems = append(problems, errors.New("resolution requires the time it was resolved"))
	}
	if err := errors.Join(problems...); err != nil {
		return fmt.Errorf("invalid directive: %w", err)
	}
	return nil
}

func kindNames() []string {
	names := make([]string, 0, len(kinds))
	for _, kind := range kinds {
		names = append(names, string(kind))
	}
	return names
}

// Resolved reports a directive somebody has settled: the ambiguity answered, or
// the artifact change decided.
func (d Directive) Resolved() bool {
	return d.ResolvedAt != nil
}

// Pauses reports a directive that stops the work it affects. Only an unresolved
// one does: settling it is exactly what lets the work resume, so a resolved
// directive is a record of something that happened rather than a live
// constraint.
func (d Directive) Pauses() bool {
	return d.Kind.Pauses() && !d.Resolved()
}

// Affects reports whether this directive reaches one work item. An unscoped
// directive reaches all of them.
func (d Directive) Affects(workItemID string) bool {
	if len(d.Scope) == 0 {
		return true
	}
	wanted := strings.TrimSpace(workItemID)
	for _, scoped := range d.Scope {
		if strings.TrimSpace(scoped) == wanted {
			return true
		}
	}
	return false
}

// Resolve settles a directive, returning the resolved copy. It refuses to
// re-resolve one: a second resolution would overwrite the account of why the
// work resumed the first time, and the work has already resumed.
func (d Directive) Resolve(resolution string, at time.Time) (Directive, error) {
	if d.Resolved() {
		return Directive{}, fmt.Errorf("%s was already resolved at %s", d.ID, d.ResolvedAt.UTC().Format(time.RFC3339))
	}
	if !d.Kind.Pauses() {
		return Directive{}, fmt.Errorf("%s is %s and has nothing to resolve; it took effect when it was recorded", d.ID, d.Kind)
	}
	trimmed := strings.TrimSpace(resolution)
	if trimmed == "" {
		return Directive{}, errors.New("say how the directive was settled; work resumes on the answer rather than on the act of answering")
	}
	resolvedAt := at.UTC()
	resolved := d
	resolved.Resolution = trimmed
	resolved.ResolvedAt = &resolvedAt
	if err := resolved.Validate(); err != nil {
		return Directive{}, err
	}
	return resolved, nil
}

// DecisionPrefix opens the text of a directive that answers a question the
// harness put to the operator with lettered options, rather than an instruction
// the operator typed.
//
// It is the record's contract rather than one surface's formatting, and it is
// here for the reason a work item's goal line is one constant: the harness writes
// it and reads it back, and two spellings that drifted apart would leave a
// recorded decision reading as ordinary prose. Ordinarily there is nothing in a
// directive to parse — it is what somebody said, in their words. Where the
// harness asked with options, though, what has to survive is which option they
// chose rather than the sentence around it, so the option is stated first, in a
// fixed shape, by Decision below and read back out by Decided.
const DecisionPrefix = "chose "

// Decision is the text of a directive that records one answer to a lettered ask:
// the letter, the option it named as the ask offered it, and whatever was said
// beside it.
//
// It writes the recorded text of a decision and the resolution of a directive
// that one settles, so the two read the same way whichever the answer turned out
// to be — and a reader that wants to know what was decided asks this package
// rather than matching prose.
func Decision(letter, option, said string) string {
	decided := DecisionPrefix + "(" + strings.TrimSpace(letter) + ") " + strings.TrimSpace(option)
	if extra := strings.TrimSpace(said); extra != "" {
		decided += " — " + extra
	}
	return decided
}

// Decided reads one back: the letter that was chosen, and whether the text
// records a decision at all.
//
// The letter is what identifies the choice, because the ask that offered it is
// what gives the letter its meaning; the option's own words follow it in the text
// for whoever is reading rather than matching. Text that Decision did not write
// is an ordinary directive somebody typed, which chose nothing — that is an
// answer rather than a failure to read one.
func Decided(text string) (string, bool) {
	rest, found := strings.CutPrefix(strings.TrimSpace(text), DecisionPrefix+"(")
	if !found {
		return "", false
	}
	letter, _, closed := strings.Cut(rest, ")")
	if !closed || strings.TrimSpace(letter) == "" {
		return "", false
	}
	return strings.TrimSpace(letter), true
}

// Pausing selects the unresolved directives that pause one work item. It is the
// question the run pipeline asks, in one place, so a run that starts, a run that
// resumes, and a run at its gate are all held to the same reading.
func Pausing(directives []Directive, workItemID string) []Directive {
	var pausing []Directive
	for _, candidate := range directives {
		if candidate.Pauses() && candidate.Affects(workItemID) {
			pausing = append(pausing, candidate)
		}
	}
	return pausing
}

// Sort puts directives in the order they were received, and settles ties by
// identifier so a listing is stable across processes.
func Sort(directives []Directive) {
	sort.SliceStable(directives, func(i, j int) bool {
		if directives[i].ReceivedAt.Equal(directives[j].ReceivedAt) {
			return directives[i].ID < directives[j].ID
		}
		return directives[i].ReceivedAt.Before(directives[j].ReceivedAt)
	})
}

// Render describes one directive for whoever has to act on it. The operator's
// own words are indented under the harness's line, as provider text is
// everywhere else, so nothing inside a directive can dress itself up as the
// harness speaking.
func (d Directive) Render() string {
	var rendered strings.Builder
	fmt.Fprintf(&rendered, "[%s] %s\n", d.ID, d.Kind.Headline())
	fmt.Fprintf(&rendered, "  received %s by the %s\n", d.ReceivedAt.UTC().Format(time.RFC3339), d.ReceivedBy)
	if d.Artifact != "" {
		fmt.Fprintf(&rendered, "  artifact: %s\n", d.Artifact)
	}
	fmt.Fprintf(&rendered, "  said: %s\n", indented(d.Text))
	if d.Unresolved != "" {
		if d.Resolved() {
			fmt.Fprintf(&rendered, "  was unresolved: %s\n", indented(d.Unresolved))
		} else {
			fmt.Fprintf(&rendered, "  unresolved: %s\n", indented(d.Unresolved))
		}
	}
	rendered.WriteString("  affects: " + d.scope() + "\n")
	if d.Resolved() {
		fmt.Fprintf(&rendered, "  resolved %s: %s\n", d.ResolvedAt.UTC().Format(time.RFC3339), indented(d.Resolution))
	}
	return rendered.String()
}

// Summary states in one line what a paused run is waiting for, which is what
// goes onto the work item and into what an operator reads first.
func (d Directive) Summary() string {
	return fmt.Sprintf("%s (%s): %s — unresolved: %s",
		d.ID, d.Kind, oneLine(d.Text), oneLine(d.Unresolved))
}

// scope says what the directive reaches, naming the unscoped case rather than
// printing an empty list that reads like nothing.
func (d Directive) scope() string {
	if len(d.Scope) == 0 {
		return "every work item, because the directive named none"
	}
	return strings.Join(d.Scope, ", ")
}

// indented keeps a multi-line value inside the block it was printed in, so
// operator prose with newlines in it cannot start a line at the margin.
func indented(value string) string {
	lines := strings.Split(strings.TrimSpace(value), "\n")
	for index := 1; index < len(lines); index++ {
		lines[index] = "    " + strings.TrimSpace(lines[index])
	}
	return strings.Join(lines, "\n")
}

// oneLine folds a value into a single bounded line, so a directive stays one
// entry of a listing or one note on a work item whatever it contains.
func oneLine(value string) string {
	folded := strings.Join(strings.Fields(value), " ")
	if len(folded) <= maxLineBytes {
		return folded
	}
	cut := maxLineBytes
	for cut > 0 && !utf8.RuneStart(folded[cut]) {
		cut--
	}
	return strings.TrimSpace(folded[:cut]) + "..."
}

func boundedText(field, value string, limit int, required bool) error {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		if required {
			return fmt.Errorf("%s is required", field)
		}
		return nil
	}
	if len(trimmed) > limit {
		return fmt.Errorf("%s is %d bytes, limit is %d", field, len(trimmed), limit)
	}
	return nil
}

func boundedLine(field, value string, required bool) error {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		if required {
			return fmt.Errorf("%s is required", field)
		}
		return nil
	}
	if len(trimmed) > maxLineBytes {
		return fmt.Errorf("%s is %d bytes, limit is %d", field, len(trimmed), maxLineBytes)
	}
	if strings.ContainsAny(trimmed, "\r\n") {
		return fmt.Errorf("%s cannot span lines", field)
	}
	return nil
}
