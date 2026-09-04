// Package humangate is a step machinery cannot take: something a named person
// has to do, and record having done, before the work behind it proceeds.
//
// It exists because the tracker cannot say it. A work item can depend on another
// item, and that is the whole of what a dependency means — so a condition that
// really reads "a person has to sign this off first" has only one encoding
// available to it, which is an item somebody has to close. Closing an item is
// something machinery does. On 2026-09-04 that is exactly what happened: the
// declarative executor's parity soak reserved the operator's own reading of the
// soak before the default flipped, the reservation was written into an item's
// text, the item was closed by the run that finished the work in it, and the
// flip's gate read satisfied. Nobody decided to skip the operator's step; the
// encoding had no way to hold it.
//
// A gate here is that missing shape. It is declared on the work it holds — a
// work item, or a state in a workflow definition — and the only thing that ever
// passes it is a durable record of a person's act, written by a person at the
// command line. Nothing derives one, no closure implies one, and no action a
// workflow can select writes one: `internal/runstate` holds the record and
// `yoyo gate record` is the one door into it. So a gate that nobody has
// discharged holds forever, which is the point — the failure it replaces is a
// gate that quietly stopped holding.
//
// # Declared, never inferred
//
// A gate is only ever what an author wrote after the marker. Nothing is read out
// of prose, and the asymmetry that makes `internal/surface` infer is the
// opposite one here: a conflict surface missed costs a race the harness already
// handles, while a gate invented from a sentence stops work until somebody
// notices they have to type a command about a gate nobody meant to declare. So
// the marker is the whole of it, and a malformed declaration is a refusal rather
// than a gate quietly dropped — a dropped gate is the original failure again,
// with the harness's own parser as the thing that skipped the person's step.
//
// The fields read are the ones somebody authored. The notes are not among them,
// for the reason a protected-path grant is not read from them either: the
// harness appends each run's own record there, so a summary that discussed a
// gate would declare one on the item after the fact, from a run rather than from
// the person whose gate it is.
package humangate

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/beads"
)

// DeclareMarker is how a work item declares a gate. It is an unlovely token on
// purpose, and for the same reason the protected-path grant marker is one: item
// text discusses gates in prose — the item that introduced this machinery uses
// the word throughout — and a marker ordinary prose can produce is a marker that
// declares by accident.
const DeclareMarker = "human-gate:"

// DeclareInstruction is what an author is told about the marker, wherever the
// harness has to explain it. It names the marker rather than describing one, so
// a reader can copy the line rather than reconstruct it.
const DeclareInstruction = "A step only a person can take is declared by naming it after `" + DeclareMarker +
	"` on its own line, as `" + DeclareMarker + " <name> — <what the person has to do>`. Nothing else declares one, " +
	"and nothing but `yoyo gate record <name>` ever passes one: closing an item does not, and no run does."

// MaxStatementBytes bounds what a gate says the person has to do. It is generous
// for the sentence it actually is and small enough that nothing can push a
// document into a work item's gate.
const MaxStatementBytes = 480

// MaxGates is how many gates one item or one workflow is expected to declare
// before somebody should be told they have written a checklist. It is a
// guideline Problems reports and never a cut: a gate dropped for being over the
// line is a step nobody is ever asked to take, which is the whole failure here.
const MaxGates = 10

// namePattern is what a gate may be called. It is narrow because the name is
// three things at once: what an author writes, what an operator types into
// `yoyo gate record`, and the file the recorded act is stored under. Anything
// that is awkward as any one of the three is refused where it is declared.
var namePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}[a-z0-9]$`)

// Gate is one step only a person can take: what it is called, and what the
// person has to do.
//
// Both halves are required. A gate with no name is one nobody could record an
// act against, and a gate that does not say what the act is, is one somebody
// discharges without knowing what they attested to — which is a signature on a
// blank page rather than the operator's step.
type Gate struct {
	// Name is what the declaration calls it, and what `yoyo gate record` names.
	Name string `json:"name"`
	// Statement is what the person has to do before the work behind this
	// proceeds, in the author's own words.
	Statement string `json:"statement"`
}

// ValidName reports whether a name is one a gate may be declared or recorded
// under. It is exported because the workflow schema declares gates too, and a
// name that a definition may use and an item may not would be two rules for one
// vocabulary.
func ValidName(name string) bool { return namePattern.MatchString(name) }

// NameProblem is what is wrong with a gate name, for a caller that has to say
// so rather than only refuse.
func NameProblem(name string) error {
	switch {
	case strings.TrimSpace(name) == "":
		return fmt.Errorf("a gate declared after %q has no name; the name is what an operator records the act against", DeclareMarker)
	case !ValidName(name):
		return fmt.Errorf("%q is not a gate name; a name is lower-case letters, digits, and hyphens, between two and sixty-four characters, because it is also what somebody types and what the recorded act is stored under", name)
	default:
		return nil
	}
}

// Of is every gate one work item declares, from the fields its author wrote.
// The notes are deliberately not among them; see the package comment.
func Of(item beads.WorkItem) []Gate {
	return Declared(item.Title, item.Description, item.Design, item.AcceptanceCriteria)
}

// Declared reads the gates a text declares after the marker, from whichever
// fields the caller passes. Which fields those are is the caller's decision,
// because it is the caller that knows which of them the harness itself writes
// into.
//
// A declaration this cannot read is left out here and reported by Problems. The
// two are apart so that a reader assembling a queue is never handed half a gate,
// while the place that can tell an author about it still can.
func Declared(texts ...string) []Gate {
	var declared []Gate
	for _, gate := range parse(texts...) {
		if gate.problem != nil {
			continue
		}
		declared = appendUnique(declared, gate.Gate)
	}
	sort.Slice(declared, func(i, j int) bool { return declared[i].Name < declared[j].Name })
	// Nothing is cut here, and MaxGates deliberately does not apply. A gate
	// dropped for being the eleventh is a step nobody is ever asked to take,
	// which is the failure this package exists to end — so the bound is something
	// Problems tells an author about rather than something this quietly enforces.
	return declared
}

// Problems is everything wrong with the gate declarations in a text: a
// declaration with no name, a name nothing could record, a gate that does not
// say what the person has to do, one name declared twice as two different
// things, and more gates than a listing can carry.
//
// It is reported rather than swallowed because a gate this cannot read is a gate
// nobody will ever be asked to discharge, and work proceeding past one is
// exactly the failure the package exists to end.
func Problems(texts ...string) []error {
	var problems []error
	statements := make(map[string]string)
	for _, parsed := range parse(texts...) {
		if parsed.problem != nil {
			problems = append(problems, parsed.problem)
			continue
		}
		if existing, declared := statements[parsed.Name]; declared && existing != parsed.Statement {
			problems = append(problems, fmt.Errorf("the gate %q is declared twice and says two different things; one name is one act, and an operator recording it would not say which they did", parsed.Name))
			continue
		}
		statements[parsed.Name] = parsed.Statement
	}
	if len(statements) > MaxGates {
		problems = append(problems, fmt.Errorf("%d gates are declared against a guideline of %d; a list this long is a checklist rather than a step somebody takes, and every one of them holds the work until it is recorded", len(statements), MaxGates))
	}
	return problems
}

// parsed is one declaration as the reader found it: the gate, or what is wrong
// with the line.
type parsed struct {
	Gate
	problem error
}

// parse reads every declaration in order, sound or not.
func parse(texts ...string) []parsed {
	var found []parsed
	for _, text := range texts {
		for _, line := range strings.Split(text, "\n") {
			remainder, declared := cutMarker(line)
			if !declared {
				continue
			}
			found = append(found, readDeclaration(remainder))
		}
	}
	return found
}

// separators are what an author may put between a gate's name and what the
// person has to do. One of them is required rather than optional, so that the
// name is what the author marked off as the name: taking the first word instead
// would read `Soak Reviewed` as the gate "soak", which is a gate somebody would
// go looking for and never find.
var separators = []string{"—", "–", "--", ":", " - "}

// readDeclaration splits one declaration into its name and its statement.
func readDeclaration(remainder string) parsed {
	declaration := strings.TrimSpace(remainder)
	// A marker with nothing after it is answered as the missing name rather than
	// as the missing sentence, because the name is what somebody left out.
	if undecorate(declaration) == "" {
		return parsed{problem: NameProblem("")}
	}
	name, statement, separated := cutAtSeparator(declaration)
	if !separated {
		return parsed{problem: fmt.Errorf("the declaration %q does not say what the person has to do; a gate is written as %s <name> — <what the person has to do>, and an act nobody described is one somebody records without knowing what they attested to",
			declaration, DeclareMarker)}
	}
	name = strings.ToLower(undecorate(strings.TrimSpace(name)))
	if problem := NameProblem(name); problem != nil {
		return parsed{problem: problem}
	}
	statement = strings.Join(strings.Fields(statement), " ")
	if statement == "" {
		return parsed{problem: fmt.Errorf("the gate %q does not say what the person has to do; an act nobody described is one somebody records without knowing what they attested to", name)}
	}
	if len(statement) > MaxStatementBytes {
		return parsed{problem: fmt.Errorf("the gate %q says what the person has to do in %d bytes against a limit of %d; a gate is a sentence rather than a document", name, len(statement), MaxStatementBytes)}
	}
	return parsed{Gate: Gate{Name: name, Statement: statement}}
}

// cutAtSeparator splits a declaration at the earliest separator in it, so an
// author who wrote a dash inside their sentence has still marked the name off
// with whichever one came first.
func cutAtSeparator(declaration string) (string, string, bool) {
	earliest := -1
	width := 0
	for _, separator := range separators {
		at := strings.Index(declaration, separator)
		if at < 0 || (earliest >= 0 && at >= earliest) {
			continue
		}
		earliest, width = at, len(separator)
	}
	if earliest < 0 {
		return "", "", false
	}
	return declaration[:earliest], strings.TrimSpace(declaration[earliest+width:]), true
}

// What an author's Markdown put around a name that is not part of it. The
// marker itself may be written in a code span, which leaves its closing
// backtick at the head of what follows.
const (
	leadingDecoration  = "`'\"*_(["
	trailingDecoration = "`'\"*_)]"
)

func undecorate(value string) string {
	return strings.TrimSpace(strings.TrimRight(strings.TrimLeft(strings.TrimSpace(value), leadingDecoration), trailingDecoration))
}

// Pending is the gates in a set that no recorded act has passed. It takes the
// discharged names rather than reading them, because what has been recorded is a
// durable fact this package deliberately does not own: the record lives in the
// harness's state store, and a package that could both declare a gate and decide
// it was satisfied would be the two halves this separates.
func Pending(gates []Gate, discharged []string) []Gate {
	recorded := make(map[string]struct{}, len(discharged))
	for _, name := range discharged {
		recorded[name] = struct{}{}
	}
	pending := make([]Gate, 0, len(gates))
	for _, gate := range gates {
		if _, passed := recorded[gate.Name]; passed {
			continue
		}
		pending = append(pending, gate)
	}
	return pending
}

// Describe says what an undischarged gate is waiting for, in the one line every
// surface prints it as. It is here rather than in each surface because the same
// gate read two places has to read the same way, and because the sentence has
// one job: to say that no machinery clears this and to name the command that
// does.
func Describe(gates []Gate) string {
	if len(gates) == 0 {
		return ""
	}
	described := make([]string, 0, len(gates))
	for _, gate := range gates {
		described = append(described, fmt.Sprintf("%s (%s)", gate.Name, gate.Statement))
	}
	return "waiting on a person: " + strings.Join(described, "; ") +
		". Nothing machinery does passes this, closing an item included; `yoyo gate record` is what records the act"
}

// cutMarker finds the marker at the start of one line and returns what follows
// it. The marker has to start the line — after whatever a Markdown bullet,
// quote, or emphasis put in front of it — so a sentence discussing the marker
// declares nothing.
func cutMarker(line string) (string, bool) {
	trimmed := strings.TrimLeft(strings.TrimSpace(line), "-*>#` \t")
	if len(trimmed) < len(DeclareMarker) || !strings.EqualFold(trimmed[:len(DeclareMarker)], DeclareMarker) {
		return "", false
	}
	return trimmed[len(DeclareMarker):], true
}

func appendUnique(gates []Gate, gate Gate) []Gate {
	for _, existing := range gates {
		if existing.Name == gate.Name {
			return gates
		}
	}
	return append(gates, gate)
}

// Names is the names of a set of gates, in the order the set is in. It is what a
// record, a listing, or a status line carries where the statement would be
// noise.
func Names(gates []Gate) []string {
	names := make([]string, 0, len(gates))
	for _, gate := range gates {
		names = append(names, gate.Name)
	}
	return names
}
