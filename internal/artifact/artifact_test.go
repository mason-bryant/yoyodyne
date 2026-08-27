package artifact

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	productHome    = "docs/product"
	designsHome    = "docs/designs"
	decisionsHome  = "docs/decisions"
	invariantsHome = "docs/decisions/invariants"
)

func TestArtifactsLoadWithTheirIdentityAndMetadata(t *testing.T) {
	t.Parallel()

	store := newStore(t)
	write(t, store, productHome+"/brief.md", document("brief", "brief", "Product brief", nil, "active")+"\nIntent in, software out.\n")
	write(t, store, productHome+"/goals/v1-goals.md", document("v1-goals", "goals", "V1 goals", []string{"brief"}, "active")+"\n## Goals\n\n- Something.\n")
	write(t, store, designsHome+"/v1-harness.md", document("v1-harness", "design", "V1 harness design", []string{"v1-goals"}, "active"))
	write(t, store, decisionsHome+"/markdown-source-of-truth.md", document("markdown-source-of-truth", "decision", "Markdown is the source of truth", []string{"v1-harness"}, "active"))

	set, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(set.Problems) != 0 {
		t.Fatalf("problems = %v", set.Problems)
	}
	// The set is in one stable order whatever order the homes were walked in, so
	// anything reading it twice reads the same thing twice.
	if got := ids(set.Artifacts); strings.Join(got, ",") != "brief,markdown-source-of-truth,v1-goals,v1-harness" {
		t.Fatalf("ids = %v", got)
	}
	goals, found := set.Find("v1-goals")
	if !found {
		t.Fatal("the goals are not in the set")
	}
	// The identity is the file name, and where it was read from is recorded, so a
	// reference to an id resolves to exactly one document a reviewer can open.
	if goals.Path != productHome+"/goals/v1-goals.md" || goals.Kind != KindGoals || !goals.InForce() {
		t.Fatalf("goals = %#v", goals)
	}
	if strings.Join(goals.Supports, ",") != "brief" {
		t.Fatalf("goals support %v", goals.Supports)
	}
	if kinds := set.OfKind(KindDecision); len(kinds) != 1 || kinds[0].ID != "markdown-source-of-truth" {
		t.Fatalf("decisions = %#v", kinds)
	}
}

func TestAnIdThatDisagreesWithItsFileNameIsRefused(t *testing.T) {
	t.Parallel()

	store := newStore(t)
	write(t, store, productHome+"/brief.md", document("product-brief", "brief", "Product brief", nil, "active"))

	set, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	// Two names for one document is how something downstream ends up referring to
	// a file nobody edited, so neither name is accepted.
	if len(set.Artifacts) != 0 || len(set.Problems) != 1 {
		t.Fatalf("set = %#v", set)
	}
	problem := set.Problems[0]
	if problem.Path != productHome+"/brief.md" || !strings.Contains(problem.Reason, `"product-brief"`) {
		t.Fatalf("problem = %#v", problem)
	}
}

func TestOneIdClaimedTwiceRefusesEveryClaimant(t *testing.T) {
	t.Parallel()

	store := newStore(t)
	write(t, store, productHome+"/goals/roadmap.md", document("roadmap", "goals", "The roadmap", []string{"brief"}, "active"))
	write(t, store, designsHome+"/roadmap.md", document("roadmap", "design", "The roadmap design", []string{"brief"}, "active"))

	set, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	// Choosing between them would hand whatever refers to "roadmap" a document
	// nobody decided on, so both are refused and both problems name the other
	// file, which is what somebody has to open to fix it.
	if len(set.Artifacts) != 0 || len(set.Problems) != 2 {
		t.Fatalf("set = %#v", set)
	}
	if set.Problems[0].Path != designsHome+"/roadmap.md" || !strings.Contains(set.Problems[0].Reason, productHome+"/goals/roadmap.md") {
		t.Fatalf("problem = %#v", set.Problems[0])
	}
	if set.Problems[1].Path != productHome+"/goals/roadmap.md" || !strings.Contains(set.Problems[1].Reason, designsHome+"/roadmap.md") {
		t.Fatalf("problem = %#v", set.Problems[1])
	}
}

func TestMalformedMetadataIsRefusedAndNamesTheFile(t *testing.T) {
	t.Parallel()

	store := newStore(t)
	cases := map[string]struct {
		content string
		reason  string
	}{
		"no-frontmatter.md": {
			content: "# A document nobody gave an identity\n\nIntent nothing can refer to.\n",
			reason:  "does not open with `---` frontmatter",
		},
		"unclosed.md": {
			content: "---\nid: unclosed\nkind: design\n",
			reason:  "not closed by a `---` line",
		},
		"mistyped-key.md": {
			content: document("mistyped-key", "design", "A mistyped field", []string{"v1-goals"}, "active"),
			reason:  "frontmatter could not be read",
		},
		"unknown-kind.md": {
			content: document("unknown-kind", "manifesto", "Not a kind the harness knows", nil, "active"),
			reason:  "kind \"manifesto\"",
		},
		"unknown-status.md": {
			content: document("unknown-status", "design", "Not a status the harness knows", nil, "published"),
			reason:  "status \"published\"",
		},
		"no-revisions.md": {
			content: "---\nid: no-revisions\nkind: design\ntitle: Nothing recorded it\nstatus: active\nrevisions: []\n---\n",
			reason:  "revisions must record at least the creation",
		},
		"unknown-role.md": {
			content: "---\nid: unknown-role\nkind: design\ntitle: Recorded by nobody\nstatus: active\nrevisions:\n" +
				"    - action: created\n      by: operator\n      at: 2026-08-17T12:00:00Z\n      reason: because\n---\n",
			reason: "by \"operator\"",
		},
		"self-supporting.md": {
			content: document("self-supporting", "design", "Upstream of itself", []string{"self-supporting"}, "active"),
			reason:  "nothing is upstream of itself",
		},
		"ended-quietly.md": {
			content: document("ended-quietly", "design", "Superseded with nothing saying so", nil, "superseded"),
			reason:  "must record the revision that superseded it",
		},
	}
	// A mistyped frontmatter key fails visibly rather than leaving an artifact
	// quietly missing half of what its author wrote.
	mistyped := cases["mistyped-key.md"]
	mistyped.content = strings.Replace(mistyped.content, "supports:", "support:", 1)
	cases["mistyped-key.md"] = mistyped

	for name, testCase := range cases {
		write(t, store, designsHome+"/"+name, testCase.content)
	}

	set, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(set.Artifacts) != 0 {
		t.Fatalf("a malformed artifact was admitted: %#v", set.Artifacts)
	}
	if len(set.Problems) != len(cases) {
		t.Fatalf("problems = %v", set.Problems)
	}
	for _, problem := range set.Problems {
		name := filepath.Base(problem.Path)
		if !strings.HasPrefix(problem.Path, designsHome+"/") {
			t.Fatalf("problem %#v does not name the file it came from", problem)
		}
		if want := cases[name].reason; !strings.Contains(problem.Reason, want) {
			t.Fatalf("problem for %s = %q, want it to name %q", name, problem.Reason, want)
		}
	}
}

func TestAnEndedArtifactRecordsWhatEndedIt(t *testing.T) {
	t.Parallel()

	store := newStore(t)
	write(t, store, decisionsHome+"/superseded-decision.md", "---\nid: superseded-decision\nkind: decision\n"+
		"title: A decision a later one replaced\nstatus: superseded\nrevisions:\n"+
		"    - action: created\n      by: architect\n      at: 2026-08-01T12:00:00Z\n      reason: recorded when it was taken\n"+
		"    - action: superseded\n      by: architect\n      at: 2026-08-17T12:00:00Z\n      reason: replaced by the decision that split the homes\n---\n")

	set, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	recorded, found := set.Find("superseded-decision")
	if !found || len(set.Problems) != 0 {
		t.Fatalf("set = %#v", set)
	}
	// A superseded artifact stays readable and stops being current: the record of
	// what was intended is what makes a later change traceable at all.
	if recorded.InForce() || len(set.InForce()) != 0 {
		t.Fatalf("a superseded artifact is still in force: %#v", recorded)
	}
	ended, hasEnding := recorded.Ended()
	if !hasEnding || ended.Action != ActionSuperseded || !strings.Contains(ended.Reason, "split the homes") {
		t.Fatalf("ending = %#v, recorded = %t", ended, hasEnding)
	}
}

func TestTheInvariantsDirectoryKeepsItsOwnIdentityScheme(t *testing.T) {
	t.Parallel()

	// An invariant is already identified by its own file name under its own
	// scheme. Reading it a second time as an artifact would report every
	// invariant in the repository as a document with no identity, which is the
	// two-schemes confusion this model exists to avoid.
	store := newStore(t)
	write(t, store, decisionsHome+"/markdown-source-of-truth.md", document("markdown-source-of-truth", "decision", "Markdown is the source of truth", nil, "active"))
	write(t, store, invariantsHome+"/one-writer-per-item.md", "---\nid: one-writer-per-item\ntitle: One process at a time\nstatus: active\n---\n\n## Must hold\n\nNothing.\n")
	// A directory index says what is filed beside it rather than stating intent
	// of its own, and `README` is not a usable id in the first place.
	write(t, store, decisionsHome+"/README.md", "# Decisions\n\nWhat is filed here.\n")

	set, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(set.Artifacts) != 1 || set.Artifacts[0].ID != "markdown-source-of-truth" {
		t.Fatalf("artifacts = %#v", set.Artifacts)
	}
	if len(set.Problems) != 0 {
		t.Fatalf("problems = %v", set.Problems)
	}
}

func TestHomesThatAreMissingOrOverlappingAreNotAProblem(t *testing.T) {
	t.Parallel()

	// A project that has not written its designs down yet records no design
	// artifacts, rather than failing every command over an absent directory.
	store := newStore(t)
	write(t, store, productHome+"/brief.md", document("brief", "brief", "Product brief", nil, "active"))
	store.Homes = append(store.Homes, productHome)

	set, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	// One file read twice through two homes is one artifact, not an id claimed by
	// itself.
	if len(set.Artifacts) != 1 || len(set.Problems) != 0 {
		t.Fatalf("set = %#v", set)
	}
}

func TestAHomeOutsideTheRepositoryIsRefused(t *testing.T) {
	t.Parallel()

	// The confinement is repeated here rather than left to the configuration,
	// because this is what reads the filesystem: a home that escaped the
	// repository would put documents nobody reviewed with the code into the set
	// that says what the product intends.
	store := newStore(t)
	for _, home := range []string{"../elsewhere", "/etc", ""} {
		store.Homes = []string{home}
		if _, err := store.Load(); err == nil {
			t.Fatalf("Load() accepted the home %q", home)
		}
	}
}

func newStore(t *testing.T) Store {
	t.Helper()
	return Store{
		RepositoryRoot: temporaryRepository(t),
		Homes:          []string{productHome, designsHome, decisionsHome},
		Excluded:       []string{invariantsHome},
	}
}

// temporaryRepository is an empty repository root a store can be pointed at.
// The path is resolved through symlinks, and a temporary directory on macOS is
// one, so the store is given what it will resolve to.
func temporaryRepository(t *testing.T) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("EvalSymlinks() error = %v", err)
	}
	return resolved
}

func write(t *testing.T, store Store, relative, content string) {
	t.Helper()
	path := filepath.Join(store.RepositoryRoot, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
}

// document renders a well-formed artifact's frontmatter, so a test that is
// about one field says only that field. The creating role is the one that owns
// the kind, because any other is an artifact the loader refuses.
func document(id, kind, title string, supports []string, status string) string {
	owner, known := Owner(Kind(kind))
	if !known {
		// A kind with no owner is what some of these tests are about, and the role
		// is not what they are checking.
		owner = "architect"
	}
	var rendered strings.Builder
	rendered.WriteString("---\nid: " + id + "\nkind: " + kind + "\ntitle: " + title + "\n")
	rendered.WriteString("supports:\n")
	for _, reference := range supports {
		rendered.WriteString("    - " + reference + "\n")
	}
	rendered.WriteString("status: " + status + "\nrevisions:\n")
	rendered.WriteString("    - action: created\n      by: " + string(owner) + "\n      at: 2026-08-17T12:00:00Z\n      reason: recorded when identity arrived\n")
	rendered.WriteString("---\n")
	return rendered.String()
}

func ids(artifacts []Artifact) []string {
	found := make([]string, 0, len(artifacts))
	for _, recorded := range artifacts {
		found = append(found, recorded.ID)
	}
	return found
}
