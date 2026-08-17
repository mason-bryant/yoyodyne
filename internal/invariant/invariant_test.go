package invariant

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"yoyodyne/internal/domain"
)

const invariantsDirectory = "docs/decisions/invariants"

func TestOnlyTheArchitectCreatesAmendsOrRetiresAnInvariant(t *testing.T) {
	t.Parallel()

	// Ownership is an authorization boundary rather than a prompt convention, so
	// every other role is refused by the code that would have written the file,
	// and nothing lands on disk when it is.
	store := newStore(t)
	statement := "Nothing."
	for _, role := range []domain.AgentRole{
		domain.RoleDeveloper,
		domain.RoleReviewer,
		domain.RoleProductManager,
		domain.RoleDevelopmentManager,
		domain.AgentRole(""),
	} {
		if _, err := store.Create(role, draft("reserve-before-work"), moment()); !errors.Is(err, ErrUnauthorized) {
			t.Fatalf("Create(%q) error = %v, want ErrUnauthorized", role, err)
		}
		if _, err := store.Amend(role, "reserve-before-work", Amendment{Statement: &statement, Reason: "because"}, moment()); !errors.Is(err, ErrUnauthorized) {
			t.Fatalf("Amend(%q) error = %v, want ErrUnauthorized", role, err)
		}
		if _, err := store.Retire(role, "reserve-before-work", "because", moment()); !errors.Is(err, ErrUnauthorized) {
			t.Fatalf("Retire(%q) error = %v, want ErrUnauthorized", role, err)
		}
	}
	entries, err := os.ReadDir(filepath.Join(store.RepositoryRoot, invariantsDirectory))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("read invariants directory: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("a refused mutation wrote %d file(s)", len(entries))
	}
}

func TestArchitectCreatesAmendsAndRetiresWithARecordedLifecycle(t *testing.T) {
	t.Parallel()

	store := newStore(t)
	created, err := store.Create(domain.RoleArchitect, draft("reserve-before-work"), moment())
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if created.Path != invariantsDirectory+"/reserve-before-work.md" {
		t.Fatalf("created path = %q", created.Path)
	}
	// A second file for one identity would let a delivered invariant and a retired
	// one be the same constraint, so creating over an existing one is refused.
	if _, err := store.Create(domain.RoleArchitect, draft("reserve-before-work"), moment()); err == nil {
		t.Fatal("Create() replaced an invariant that already exists")
	}

	statement := "Every entry into an in-flight run takes the run's exclusive lease first."
	scope := []string{"internal/orchestrator"}
	amended, err := store.Amend(domain.RoleArchitect, "reserve-before-work", Amendment{
		Statement: &statement,
		Scope:     &scope,
		Reason:    "the second resume path made the original wording ambiguous",
	}, moment().Add(time.Hour))
	if err != nil {
		t.Fatalf("Amend() error = %v", err)
	}
	if amended.Statement != statement || len(amended.Revisions) != 2 || amended.Revisions[1].Action != ActionAmended {
		t.Fatalf("amended = %#v", amended)
	}
	if amended.Revisions[1].By != domain.RoleArchitect {
		t.Fatalf("the amendment recorded %q rather than the authority it was made under", amended.Revisions[1].By)
	}
	// An amendment that names no change would record a revision with nothing in
	// it, which is a history entry that explains nothing.
	if _, err := store.Amend(domain.RoleArchitect, "reserve-before-work", Amendment{Reason: "tidying"}, moment()); err == nil {
		t.Fatal("Amend() recorded a revision that changed nothing")
	}

	retired, err := store.Retire(domain.RoleArchitect, "reserve-before-work", "the reservation moved into the store and cannot be bypassed", moment().Add(2*time.Hour))
	if err != nil {
		t.Fatalf("Retire() error = %v", err)
	}
	retirement, recorded := retired.Retirement()
	if !recorded || !strings.Contains(retirement.Reason, "cannot be bypassed") {
		t.Fatalf("retirement = %#v, recorded = %t", retirement, recorded)
	}
	if _, err := store.Retire(domain.RoleArchitect, "reserve-before-work", "again", moment()); err == nil {
		t.Fatal("Retire() retired an invariant twice")
	}
	if _, err := store.Amend(domain.RoleArchitect, "reserve-before-work", Amendment{Statement: &statement, Reason: "revive it"}, moment()); err == nil {
		t.Fatal("Amend() amended a retired invariant back into force")
	}

	// The whole lifecycle survives a reload, and a retired invariant is recorded
	// rather than deleted: it stays readable, and it stops being delivered.
	set, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(set.Active) != 0 || len(set.Retired) != 1 || len(set.Problems) != 0 {
		t.Fatalf("set = %#v", set)
	}
	reloaded, found := set.Find("reserve-before-work")
	if !found || len(reloaded.Revisions) != 3 || reloaded.Status != StatusRetired {
		t.Fatalf("reloaded = %#v, found = %t", reloaded, found)
	}
	if delivery := set.Select("internal/orchestrator"); !delivery.Empty() {
		t.Fatalf("a retired invariant was delivered: %#v", delivery)
	}
}

func TestLoadReportsWhatCannotBeReadAsAnInvariant(t *testing.T) {
	t.Parallel()

	store := newStore(t)
	if _, err := store.Create(domain.RoleArchitect, draft("reserve-before-work"), moment()); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	base := filepath.Join(store.RepositoryRoot, invariantsDirectory)
	valid := readFile(t, filepath.Join(base, "reserve-before-work.md"))
	writeFile(t, filepath.Join(base, "no-frontmatter.md"), "## Must hold\n\nSomething.\n\n## Why\n\nBecause.\n")
	writeFile(t, filepath.Join(base, "unknown-key.md"), strings.Replace(valid, "status: active", "status: active\nseverity: high", 1))
	writeFile(t, filepath.Join(base, "wrong-name.md"), valid)
	writeFile(t, filepath.Join(base, "no-sections.md"), strings.SplitAfter(valid, "---\n\n")[0]+"It just says things.\n")
	writeFile(t, filepath.Join(base, "nested", "hidden.md"), valid)

	set, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	// The readable invariant is still delivered; each unreadable file is named,
	// and none of them is delivered as half a constraint.
	if len(set.Active) != 1 || set.Active[0].ID != "reserve-before-work" {
		t.Fatalf("active = %#v", set.Active)
	}
	reported := map[string]string{}
	for _, problem := range set.Problems {
		reported[problem.Path] = problem.Reason
	}
	for _, path := range []string{
		invariantsDirectory + "/no-frontmatter.md",
		invariantsDirectory + "/unknown-key.md",
		invariantsDirectory + "/wrong-name.md",
		invariantsDirectory + "/no-sections.md",
		invariantsDirectory + "/nested/hidden.md",
	} {
		if reported[path] == "" {
			t.Fatalf("%s was not reported as unreadable; problems = %#v", path, set.Problems)
		}
	}
}

func TestSelectDeliversRepositoryWideInvariantsAndScopeMatches(t *testing.T) {
	t.Parallel()

	set := Set{
		Directory: invariantsDirectory,
		Active: []Invariant{
			built("harness-owns-git", nil),
			built("one-writer-per-item", []string{"internal/runstate", "internal/orchestrator"}),
			built("console-renders-plain", []string{"internal/console"}),
		},
	}

	// A work item that says nothing about any package still gets everything that
	// applies everywhere, and nothing that is scoped elsewhere.
	wide := set.Select("Give the architect ownership of durable architectural invariants")
	if got := wide.IDs(); len(got) != 1 || got[0] != "harness-owns-git" {
		t.Fatalf("delivered = %v", got)
	}
	if wide.Considered != 3 {
		t.Fatalf("considered = %d", wide.Considered)
	}

	// Naming the code is what pulls a scoped invariant in, whether the work item
	// named it or the change did.
	scoped := set.Select("resume a run", "M internal/runstate/store.go\n")
	if got := scoped.IDs(); len(got) != 2 || got[0] != "harness-owns-git" || got[1] != "one-writer-per-item" {
		t.Fatalf("delivered = %v", got)
	}
	if text := scoped.Text(); !strings.Contains(text, "one-writer-per-item") || strings.Contains(text, "console-renders-plain") {
		t.Fatalf("rendered text = %q", text)
	}
}

func TestSelectNamesWhatDidNotFitRatherThanTrimmingSilently(t *testing.T) {
	t.Parallel()

	// A repository that accumulated far more than the rare, consequential
	// constraints an invariant is for still gets a bounded prompt, and is told
	// which invariants it is not seeing.
	set := Set{Directory: invariantsDirectory}
	for _, id := range []string{"aaa", "bbb", "ccc", "ddd", "eee", "fff", "ggg", "hhh", "iii", "jjj"} {
		large := built(id, nil)
		large.Statement = strings.Repeat("x", MaxStatementBytes)
		set.Active = append(set.Active, large)
	}
	delivery := set.Select("anything")
	if len(delivery.Selected) == 0 || len(delivery.Omitted) == 0 {
		t.Fatalf("selected %d, omitted %d", len(delivery.Selected), len(delivery.Omitted))
	}
	if len(delivery.Text()) > MaxDeliveredBytes+2048 {
		t.Fatalf("rendered %d bytes for a %d byte bound", len(delivery.Text()), MaxDeliveredBytes)
	}
	if !strings.Contains(delivery.Text(), "unread rather than as absent") {
		t.Fatalf("the omission was not stated: %q", delivery.Text())
	}
}

func TestDeliveredTextStatesOwnershipAndItsOwnLimits(t *testing.T) {
	t.Parallel()

	set := Set{
		Directory: invariantsDirectory,
		Active:    []Invariant{built("one-writer-per-item", nil)},
		Problems:  []Problem{{Path: invariantsDirectory + "/broken.md", Reason: "it has no `## Why` section"}},
	}
	text := set.Select("anything").Text()
	for _, expected := range []string{
		"# Architectural invariants",
		invariantsDirectory,
		"The architect owns",
		"one-writer-per-item",
		"Must hold:",
		"Why:",
		"not the whole set",
		"could not be read",
		"broken.md",
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("delivered text is missing %q:\n%s", expected, text)
		}
	}

	// A set whose only content is a file nobody could read is not an empty set,
	// and must not be delivered as one.
	unreadable := Set{
		Directory: invariantsDirectory,
		Active:    []Invariant{built("one-writer-per-item", []string{"internal/console"})},
		Problems:  []Problem{{Path: invariantsDirectory + "/broken.md", Reason: "it has no `## Why` section"}},
	}
	gap := unreadable.Select("nothing this invariant is scoped to").Text()
	if !strings.Contains(gap, "Treat the set as unknown rather than as empty") || !strings.Contains(gap, "broken.md") {
		t.Fatalf("a set that is only a gap rendered as:\n%s", gap)
	}

	// Nothing selected, nothing omitted, and nothing unreadable renders nothing at
	// all: a section announcing that there are no invariants teaches every agent
	// to skim the heading.
	if empty := (Set{Directory: invariantsDirectory}).Select("anything"); !empty.Empty() || empty.Text() != "" {
		t.Fatalf("empty delivery rendered %q", empty.Text())
	}
}

func TestValidateRefusesInvariantsNobodyCouldBeHeldTo(t *testing.T) {
	t.Parallel()

	for name, mutate := range map[string]func(*Invariant){
		"noID":            func(i *Invariant) { i.ID = "" },
		"badID":           func(i *Invariant) { i.ID = "Reserve Before Work" },
		"noTitle":         func(i *Invariant) { i.Title = " " },
		"noStatement":     func(i *Invariant) { i.Statement = "" },
		"noRationale":     func(i *Invariant) { i.Rationale = "" },
		"noOrigin":        func(i *Invariant) { i.EstablishedBy = nil },
		"noRevisions":     func(i *Invariant) { i.Revisions = nil },
		"badStatus":       func(i *Invariant) { i.Status = "proposed" },
		"escapingScope":   func(i *Invariant) { i.Scope = []string{"../elsewhere"} },
		"absoluteScope":   func(i *Invariant) { i.Scope = []string{"/etc"} },
		"unauthorizedBy":  func(i *Invariant) { i.Revisions[0].By = domain.RoleDeveloper },
		"unexplained":     func(i *Invariant) { i.Revisions[0].Reason = "" },
		"retiredSilently": func(i *Invariant) { i.Status = StatusRetired },
		"activelyRetired": func(i *Invariant) {
			i.Revisions = append(i.Revisions, Revision{Action: ActionRetired, By: domain.RoleArchitect, At: moment(), Reason: "x"})
		},
		"oversizeStatement": func(i *Invariant) { i.Statement = strings.Repeat("x", MaxStatementBytes+1) },
	} {
		candidate := built("reserve-before-work", nil)
		mutate(&candidate)
		if err := candidate.Validate(); err == nil {
			t.Fatalf("%s: Validate() accepted %#v", name, candidate)
		}
	}
	if err := built("reserve-before-work", []string{"internal/runstate"}).Validate(); err != nil {
		t.Fatalf("Validate() rejected a usable invariant: %v", err)
	}
}

func newStore(t *testing.T) Store {
	t.Helper()
	return Store{RepositoryRoot: t.TempDir(), Directory: invariantsDirectory}
}

func draft(id string) Draft {
	return Draft{
		ID:            id,
		Title:         "One process at a time acts on an in-flight work item",
		Statement:     "Every path into an in-flight run reserves it through the state store.",
		Rationale:     "The reservation is the only thing keeping two developers off one item.",
		EstablishedBy: []string{"yoyodyne-ifd.2.7"},
		Reason:        "extracted from the decision that added the reservation",
	}
}

func built(id string, scope []string) Invariant {
	return Invariant{
		ID:            id,
		Title:         "One process at a time acts on an in-flight work item",
		EstablishedBy: []string{"yoyodyne-ifd.2.7"},
		Scope:         scope,
		Status:        StatusActive,
		Statement:     "Every path into an in-flight run reserves it through the state store.",
		Rationale:     "The reservation is the only thing keeping two developers off one item.",
		Revisions: []Revision{{
			Action: ActionCreated,
			By:     domain.RoleArchitect,
			At:     moment(),
			Reason: "extracted from the decision that added the reservation",
		}},
	}
}

func moment() time.Time {
	return time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(content)
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
