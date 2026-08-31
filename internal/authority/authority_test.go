package authority

// The checker held against fabricated repositories, so each way it can fail is
// exercised by a case that actually fails rather than by the real inventory
// happening to be right today.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// gateSource is a fabricated authority check: a function that names a role and
// refuses, which is the sweep's first signal, plus a declared name carrying the
// vocabulary, which is its second.
const gateSource = `package gate

import (
	"fmt"

	"github.com/mason-bryant/yoyodyne/internal/domain"
)

var ErrUnauthorized = fmt.Errorf("only the architect may")

type Gate struct{}

func (g Gate) Authorize(role domain.AgentRole) error {
	if role == domain.RoleArchitect {
		return nil
	}
	return fmt.Errorf("%w; %q may propose one instead", ErrUnauthorized, role)
}

func Untouched() string { return "nothing here refuses anything" }
`

// inventoryDocument is a whole inventory, written the way the real one is.
func inventoryDocument(rows, exemptions string) string {
	return "# The authority inventory\n\nProse above the tables.\n\n" +
		InventoryHeading + "\n\n" +
		"| Check | Binds | File | Declaration | Refuses |\n| --- | --- | --- | --- | --- |\n" +
		rows + "\n" +
		ExemptionsHeading + "\n\n" +
		"| File | Declaration | Why it is not one |\n| --- | --- | --- |\n" +
		exemptions
}

const listedRows = "| gate.authorize | the architect | `internal/gate/gate.go` | `(Gate).Authorize` | Any role but the architect. |\n" +
	"| gate.unauthorized-error | the architect | `internal/gate/gate.go` | `ErrUnauthorized` | What a refusal returns. |\n"

func fabricate(t *testing.T, rows, exemptions string) string {
	t.Helper()

	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "internal", "gate"), 0o755); err != nil {
		t.Fatalf("make the fabricated package: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "internal", "gate", "gate.go"), []byte(gateSource), 0o644); err != nil {
		t.Fatalf("write the fabricated package: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatalf("make the fabricated docs home: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(InventoryPath)), []byte(inventoryDocument(rows, exemptions)), 0o644); err != nil {
		t.Fatalf("write the fabricated inventory: %v", err)
	}
	return root
}

func checkFabricated(t *testing.T, rows, exemptions string) []Problem {
	t.Helper()

	problems, err := Check(fabricate(t, rows, exemptions))
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	return problems
}

func onlyProblem(t *testing.T, problems []Problem) string {
	t.Helper()

	if len(problems) != 1 {
		t.Fatalf("Check() reported %d problems, want 1: %v", len(problems), problems)
	}
	return problems[0].Reason
}

// joinedReasons is every reason at once, for the cases that report more than
// one: a row pointed at a declaration that is not there also unlists the
// declaration that is, and both halves are true.
func joinedReasons(problems []Problem) string {
	reasons := make([]string, 0, len(problems))
	for _, problem := range problems {
		reasons = append(reasons, problem.Reason)
	}
	return strings.Join(reasons, "\n")
}

func TestInventoryReadsBothTables(t *testing.T) {
	t.Parallel()

	root := fabricate(t, listedRows, "| `internal/gate/gate.go` | `Untouched` | It refuses nothing. |\n")
	entries, exemptions, err := Inventory(root)
	if err != nil {
		t.Fatalf("Inventory() error = %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("Inventory() read %d entries, want 2: %v", len(entries), entries)
	}
	if entries[0].Check != "gate.authorize" || entries[0].File != "internal/gate/gate.go" || entries[0].Declaration != "(Gate).Authorize" {
		t.Errorf("the first entry read as %+v", entries[0])
	}
	if entries[0].Binds != "the architect" || !strings.HasPrefix(entries[0].Refuses, "Any role") {
		t.Errorf("the first entry lost what it binds or what it refuses: %+v", entries[0])
	}
	if len(exemptions) != 1 || exemptions[0].Declaration != "Untouched" {
		t.Fatalf("Inventory() read exemptions %v", exemptions)
	}
}

// TestAnInventoryThatMatchesTheCodeReportsNothing is the case every other test
// here is a departure from: an empty result has to be reachable, or a checker
// that always fails would pass all of them.
func TestAnInventoryThatMatchesTheCodeReportsNothing(t *testing.T) {
	t.Parallel()

	if problems := checkFabricated(t, listedRows, ""); len(problems) != 0 {
		t.Errorf("Check() reported %v on an inventory that matches the code", problems)
	}
}

func TestAListedCheckThatMovedFails(t *testing.T) {
	t.Parallel()

	moved := strings.Replace(listedRows, "`(Gate).Authorize`", "`(Gate).AuthorizeRole`", 1)
	reasons := joinedReasons(checkFabricated(t, moved, ""))
	if !strings.Contains(reasons, "(Gate).AuthorizeRole") || !strings.Contains(reasons, "moved or was renamed") {
		t.Errorf("Check() reported %q, and it does not name the check that moved", reasons)
	}
}

func TestAListedCheckInAFileThatIsNotThereFails(t *testing.T) {
	t.Parallel()

	gone := strings.Replace(listedRows, "`internal/gate/gate.go`", "`internal/gate/elsewhere.go`", 1)
	reasons := joinedReasons(checkFabricated(t, gone, ""))
	if !strings.Contains(reasons, "internal/gate/elsewhere.go") {
		t.Errorf("Check() reported %q, and it does not name the file that is not there", reasons)
	}
}

func TestAnAuthorizationSiteOutsideTheListFails(t *testing.T) {
	t.Parallel()

	unlisted := "| gate.authorize | the architect | `internal/gate/gate.go` | `(Gate).Authorize` | Any role but the architect. |\n"
	reason := onlyProblem(t, checkFabricated(t, unlisted, ""))
	if !strings.Contains(reason, "ErrUnauthorized") || !strings.Contains(reason, "lists it nowhere") {
		t.Errorf("Check() reported %q, and it does not name the site nothing lists", reason)
	}
}

func TestAnExcusedSiteIsNotReported(t *testing.T) {
	t.Parallel()

	listed := "| gate.authorize | the architect | `internal/gate/gate.go` | `(Gate).Authorize` | Any role but the architect. |\n"
	excused := "| `internal/gate/gate.go` | `ErrUnauthorized` | It is what the refusal above returns. |\n"
	if problems := checkFabricated(t, listed, excused); len(problems) != 0 {
		t.Errorf("Check() reported %v on a site the second table excuses", problems)
	}
}

func TestAStaleExemptionFails(t *testing.T) {
	t.Parallel()

	stale := "| `internal/gate/gate.go` | `Untouched` | It refuses nothing. |\n"
	reason := onlyProblem(t, checkFabricated(t, listedRows, stale))
	if !strings.Contains(reason, "Untouched") || !strings.Contains(reason, "remove the exemption") {
		t.Errorf("Check() reported %q, and it does not name the exemption that has stopped being true", reason)
	}
}

func TestADeclarationBothListedAndExcusedFails(t *testing.T) {
	t.Parallel()

	both := "| `internal/gate/gate.go` | `ErrUnauthorized` | It is what the refusal returns. |\n"
	reason := onlyProblem(t, checkFabricated(t, listedRows, both))
	if !strings.Contains(reason, "two answers to one question") {
		t.Errorf("Check() reported %q, and it does not name the declaration answered twice", reason)
	}
}

func TestADuplicateCheckNameFails(t *testing.T) {
	t.Parallel()

	twice := listedRows + "| gate.authorize | the architect | `internal/gate/gate.go` | `Untouched` | Nothing. |\n"
	reasons := joinedReasons(checkFabricated(t, twice, ""))
	if !strings.Contains(reasons, "is listed again") {
		t.Errorf("Check() reported %q, and it does not name the check listed twice", reasons)
	}
}

// TestOneDeclarationMayCarryTwoChecks is the case the duplicate rule must not
// catch: one function that makes two separate refusals is two rows worth
// reviewing apart, and the sweep sees one site either way.
func TestOneDeclarationMayCarryTwoChecks(t *testing.T) {
	t.Parallel()

	twoChecks := listedRows +
		"| gate.authorize.unnamed-role | the architect | `internal/gate/gate.go` | `(Gate).Authorize` | A mutation naming no role at all. |\n"
	if problems := checkFabricated(t, twoChecks, ""); len(problems) != 0 {
		t.Errorf("Check() reported %v on one declaration listed as two checks", problems)
	}
}

func TestAnEmptyInventoryIsRefused(t *testing.T) {
	t.Parallel()

	_, err := Check(fabricate(t, "", ""))
	if err == nil {
		t.Fatal("Check() accepted an inventory listing nothing")
	}
	if !strings.Contains(err.Error(), InventoryHeading) {
		t.Errorf("Check() error = %v, and it does not say which table was empty", err)
	}
}

func TestSitesReadsNeitherTestFilesNorOtherTrees(t *testing.T) {
	t.Parallel()

	root := fabricate(t, listedRows, "")
	if err := os.WriteFile(filepath.Join(root, "internal", "gate", "gate_test.go"), []byte(strings.Replace(gateSource, "package gate", "package gate\n", 1)), 0o644); err != nil {
		t.Fatalf("write the fabricated test file: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "scripts"), 0o755); err != nil {
		t.Fatalf("make the fabricated script home: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "scripts", "gate.go"), []byte(gateSource), 0o644); err != nil {
		t.Fatalf("write the fabricated out-of-tree file: %v", err)
	}
	sites, err := Sites(root)
	if err != nil {
		t.Fatalf("Sites() error = %v", err)
	}
	for _, site := range sites {
		if strings.HasSuffix(site.File, "_test.go") || !strings.HasPrefix(site.File, "internal/") {
			t.Errorf("Sites() read %s, which is not a non-test file in a swept tree", site.File)
		}
	}
	if len(sites) != 2 {
		t.Errorf("Sites() found %d sites in the fabricated package, want 2: %v", len(sites), sites)
	}
}

func TestALeaseNamedForItsReleaseIsNotASite(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"Release", "releasePath", "IntakeReleased"} {
		if stem, matched := matchStem(name); matched {
			t.Errorf("matchStem(%q) = %q, and releasing is not leasing", name, stem)
		}
	}
	for _, name := range []string{"LeasePromotion", "Authorize", "authorities", "gateProtectedPaths", "validateIndependentInvocations"} {
		if _, matched := matchStem(name); !matched {
			t.Errorf("matchStem(%q) found nothing, and it names a boundary the sweep exists to find", name)
		}
	}
}
