package protectedpath

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/config"
)

// defaultHomes is the homes a project on the recommended layout gets, owning
// the two documents the incidents behind this gate were about.
func defaultHomes() Homes {
	return ArtifactHomes(config.Config{Product: config.Product{
		Specifications: config.DefaultSpecifications,
		Designs:        config.DefaultDesigns,
		Decisions:      config.DefaultDecisions,
		Invariants:     config.DefaultInvariants,
	}},
		Document{ID: "slack-reporting-design", Path: "docs/designs/slack-reporting-design.md"},
		Document{ID: "observability-and-dashboard", Path: "docs/designs/observability-and-dashboard.md"},
		Document{ID: "brief", Path: "docs/product/brief.md"},
	)
}

// The three incidents, as their done-conditions were written. Each names a
// document the run may not write in the clause that says what done means, and
// each is refused with that clause quoted and the fix named.
func TestADoneConditionNamingAnOwnedDocumentIsRefusedWithTheClauseQuoted(t *testing.T) {
	t.Parallel()

	for _, incident := range []struct {
		item        string
		description string
		wantPath    string
		wantClause  string
	}{
		{
			item:        "yoyodyne-ifd.68.25",
			description: "Operator-requested capability. The boundary stays as-is.\n\nDone means her ruling is recorded on the slack-reporting design, the sink renders role prose per it, and the token never reaches an agent process.",
			wantPath:    "docs/designs/slack-reporting-design.md",
			wantClause:  "Done means her ruling is recorded on the slack-reporting design, the sink renders role prose per it, and the token never reaches an agent process.",
		},
		{
			item:        "yoyodyne-ifd.63",
			description: "Fold the script into the verb.\n\nDone means: yoyo status covers what bin/yoyo-status does today; documentation reconciles, including docs/designs/v1-harness-design.md's yoyo status entry, which this finally builds.\n\nRelation: none.",
			wantPath:    "docs/designs/v1-harness-design.md",
			wantClause:  "documentation reconciles, including docs/designs/v1-harness-design.md's yoyo status entry, which this finally builds.",
		},
		{
			item:        "yoyodyne-ifd.141.1",
			description: "First of three children. Done means: the read model exposes that state; and the observability-and-dashboard design's query list marks the query as existing.",
			wantPath:    "docs/designs/observability-and-dashboard.md",
			wantClause:  "and the observability-and-dashboard design's query list marks the query as existing.",
		},
	} {
		conditions := defaultHomes().Ungranted(incident.description, "", nil)
		if len(conditions) != 1 {
			t.Fatalf("%s: Ungranted() = %#v, want exactly the one clause", incident.item, conditions)
		}
		if conditions[0].Path != incident.wantPath {
			t.Fatalf("%s: Ungranted() reached %q, want %q", incident.item, conditions[0].Path, incident.wantPath)
		}
		if conditions[0].Clause != incident.wantClause {
			t.Fatalf("%s: Ungranted() quoted %q, want %q", incident.item, conditions[0].Clause, incident.wantClause)
		}
		// The refusal has three jobs: quote the clause, name the document, and say
		// what to do instead — both fixes, because a role told only "no" writes the
		// same clause again with the document named differently.
		refusal := conditions[0].Refusal()
		for _, want := range []string{incident.wantClause, incident.wantPath, "owner", "architect", GrantMarker, "governed path"} {
			if !strings.Contains(refusal, want) {
				t.Fatalf("%s: refusal %q never says %q", incident.item, refusal, want)
			}
		}
	}
}

// A grant covering the path is what admits the condition, exactly as it admits
// the diff: one naming the document, or one naming the home it sits in.
func TestAGrantAdmitsTheConditionItCovers(t *testing.T) {
	t.Parallel()

	description := "Done means the ruling is recorded on the slack-reporting design as the architect's revision, which the operator approved on 2026-09-02."
	if conditions := defaultHomes().Ungranted(description, "", []string{"docs/designs/slack-reporting-design.md"}); len(conditions) != 0 {
		t.Fatalf("Ungranted() with the document granted = %#v, want nothing", conditions)
	}
	if conditions := defaultHomes().Ungranted(description, "", []string{"docs/designs"}); len(conditions) != 0 {
		t.Fatalf("Ungranted() with the home granted = %#v, want nothing", conditions)
	}
	// A grant of a different document admits nothing here.
	if conditions := defaultHomes().Ungranted(description, "", []string{"docs/designs/v1-harness-design.md"}); len(conditions) != 1 {
		t.Fatalf("Ungranted() with another document granted = %#v, want the condition refused", conditions)
	}
	// And the grant is read as the item writes it, through Grants, so the two
	// halves of the item agree about what was admitted.
	granted := description + "\n\n" + GrantMarker + " docs/designs/slack-reporting-design.md\n"
	if problems := defaultHomes().ConditionProblems(granted, "", Grants(granted)); len(problems) != 0 {
		t.Fatalf("ConditionProblems() on a granted item = %v, want nothing", problems)
	}
}

// Only the done-conditions are read. An item cites the design it builds
// against, the decision that rules something out, and the invariant it is held
// to, in nearly every description, and a citation is not a condition.
func TestProseOutsideTheDoneConditionsIsNotACondition(t *testing.T) {
	t.Parallel()

	description := "The observability-and-dashboard design requires one read-model query that does not exist, per docs/designs/observability-and-dashboard.md and the ruling in docs/decisions/entanglement-and-merge-affinity.md.\n\n" +
		"Done means: the read model exposes that state through the same server-side projection every operator surface reads, and a test pins the shape.\n\n" +
		"The design's query list is the architect's document and is not touched by this run; she amends it through the governed path, and the run's summary names the query so she can. See docs/designs/observability-and-dashboard.md for the list."
	if conditions := defaultHomes().Ungranted(description, "", nil); len(conditions) != 0 {
		t.Fatalf("Ungranted() on citations outside the done-means = %#v, want nothing", conditions)
	}
	// The whole of the acceptance criteria is a condition by construction.
	if conditions := defaultHomes().Ungranted("", "The query is exposed. docs/designs/observability-and-dashboard.md lists it as existing.", nil); len(conditions) != 1 {
		t.Fatalf("Ungranted() on acceptance criteria = %#v, want the one clause", conditions)
	}
	// The done-means paragraph ends at the blank line, so a citation in the
	// paragraph after it is a citation.
	description = "Done means the query exists.\n\nSee docs/designs/observability-and-dashboard.md."
	if conditions := defaultHomes().Ungranted(description, "", nil); len(conditions) != 0 {
		t.Fatalf("Ungranted() past the done-means paragraph = %#v, want nothing", conditions)
	}
	// And the other two phrasings this backlog writes done-conditions in are read.
	for _, phrasing := range []string{"DONE: docs/product/goals/v1-goals.md states the goal.", "The item is done when docs/product/goals/v1-goals.md states the goal."} {
		if conditions := defaultHomes().Ungranted(phrasing, "", nil); len(conditions) != 1 {
			t.Fatalf("Ungranted() on %q = %#v, want the one clause", phrasing, conditions)
		}
	}
}

// A path outside the artifact homes is a path the run may write, however it is
// named, and a document is named by its id only where the id could not be an
// ordinary word.
func TestOnlyTheArtifactHomesAndTheirDocumentsAreConditions(t *testing.T) {
	t.Parallel()

	homes := defaultHomes()
	// docs/work.md is documentation the developer updates as part of the work,
	// and the configuration directory is protected in a diff but not here: a
	// condition naming it is about the harness's settings, not another role's
	// document.
	description := "Done means docs/work.md states the rule, .yoyodyne/config.yaml names the check, and internal/protectedpath/condition.go carries it; the brief's acceptance criteria hold."
	if conditions := homes.Ungranted(description, "", nil); len(conditions) != 0 {
		t.Fatalf("Ungranted() on ordinary paths and a one-word id = %#v, want nothing", conditions)
	}
	// The separator is required, so a sibling of a home is not inside it.
	if conditions := homes.Ungranted("Done means docs/products/index.md lists it.", "", nil); len(conditions) != 0 {
		t.Fatalf("Ungranted() on a sibling of the home = %#v, want nothing", conditions)
	}
	// A home named bare, with or without its trailing slash, is the home.
	for _, named := range []string{"docs/designs", "docs/designs/"} {
		conditions := homes.Ungranted("Done means a design under "+named+" records it.", "", nil)
		if len(conditions) != 1 || conditions[0].Path != "docs/designs" {
			t.Fatalf("Ungranted() on %q = %#v, want the home", named, conditions)
		}
	}
	// An id inside a longer identifier is not the id, and the id is found however
	// the prose joined its words.
	if conditions := homes.Ungranted("Done means slack-reporting-design-v2 is superseded.", "", nil); len(conditions) != 0 {
		t.Fatalf("Ungranted() on a longer identifier = %#v, want nothing", conditions)
	}
	for _, named := range []string{"the Slack-Reporting design", "slack_reporting_design", "`slack-reporting-design`"} {
		if conditions := homes.Ungranted("Done means "+named+" records the rule.", "", nil); len(conditions) != 1 {
			t.Fatalf("Ungranted() on %q = %#v, want the document named", named, conditions)
		}
	}
	// A project that keeps its designs somewhere else has them checked there.
	moved := ArtifactHomes(config.Config{Product: config.Product{Designs: "architecture/designs"}})
	if conditions := moved.Ungranted("Done means architecture/designs/v1.md records it and docs/designs/v1.md does not exist.", "", nil); len(conditions) != 1 || conditions[0].Path != "architecture/designs/v1.md" {
		t.Fatalf("Ungranted() on a moved home = %#v, want the moved design alone", conditions)
	}
	// Homes nobody configured refuse nothing, which is what a caller wired none
	// gets.
	if conditions := (Homes{}).Ungranted("Done means docs/designs/v1.md records it.", "", nil); len(conditions) != 0 || !(Homes{}).Empty() {
		t.Fatalf("Ungranted() on empty homes = %#v, want nothing", conditions)
	}
}

// A clause is quoted once however many times it names a document, and a
// reading is bounded so the refusal stays readable.
func TestConditionsAreDeduplicatedAndBounded(t *testing.T) {
	t.Parallel()

	description := "Done means docs/designs/a.md, docs/designs/a.md, and docs/designs/b.md each record it; docs/designs/c.md does; docs/designs/d.md does; docs/designs/e.md does; docs/designs/f.md does; docs/designs/g.md does."
	conditions := defaultHomes().Ungranted(description, "", nil)
	if len(conditions) != maxConditions {
		t.Fatalf("Ungranted() = %d conditions, want the bound of %d", len(conditions), maxConditions)
	}
	if conditions[0].Path != "docs/designs/a.md" || conditions[1].Path != "docs/designs/b.md" {
		t.Fatalf("Ungranted() = %#v, want a.md once and then b.md", conditions[:2])
	}
	long := "Done means " + strings.Repeat("the design under docs/designs/ records it and ", 20) + "so on."
	conditions = defaultHomes().Ungranted(long, "", nil)
	if len(conditions) != 1 || len(conditions[0].Clause) > maxClauseBytes+len("...") || !strings.HasSuffix(conditions[0].Clause, "...") {
		t.Fatalf("Ungranted() on a long clause = %#v, want it folded to %d bytes", conditions, maxClauseBytes)
	}
}

// The documents are read as the artifact store identifies them, so what a
// condition is checked against is what every other reader of the homes sees: a
// file the store refused is still a document the home owns, an index is not,
// and an invariant is left to its path.
func TestOwnedDocumentsAreTheArtifactStores(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	write := func(relative, content string) {
		t.Helper()
		path := filepath.Join(root, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("docs/designs/README.md", "# Designs\n")
	write("docs/designs/slack-reporting-design.md", "---\nid: slack-reporting-design\nkind: design\nstatus: in-force\nowner: architect\nsupports: [v1-goals]\nrevisions: []\n---\n# Slack\n")
	write("docs/designs/malformed-design.md", "no frontmatter here\n")
	write("docs/decisions/invariants/one-promotion-per-target-branch.md", "---\nid: x\n---\n")
	product := config.Product{
		Specifications: config.DefaultSpecifications,
		Designs:        config.DefaultDesigns,
		Decisions:      config.DefaultDecisions,
		Invariants:     config.DefaultInvariants,
	}
	documents, err := OwnedDocuments(root, product)
	if err != nil {
		t.Fatalf("OwnedDocuments() error = %v", err)
	}
	var paths []string
	for _, document := range documents {
		paths = append(paths, document.ID+"="+document.Path)
	}
	slices.Sort(paths)
	want := []string{"malformed-design=docs/designs/malformed-design.md", "slack-reporting-design=docs/designs/slack-reporting-design.md"}
	if !slices.Equal(paths, want) {
		t.Fatalf("OwnedDocuments() = %v, want %v", paths, want)
	}
	// And a done-condition naming the malformed one by id is refused like any
	// other: its frontmatter being wrong makes it no more a run's to write.
	homes := ArtifactHomes(config.Config{Product: product}, documents...)
	if conditions := homes.Ungranted("Done means the malformed design records the rule.", "", nil); len(conditions) != 1 {
		t.Fatalf("Ungranted() naming a refused document = %#v, want it refused", conditions)
	}
}
