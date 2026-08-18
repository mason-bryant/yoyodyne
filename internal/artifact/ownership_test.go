package artifact

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
)

func TestOnlyTheOwningRoleWritesAnArtifact(t *testing.T) {
	t.Parallel()

	store := newStore(t)
	for _, seeded := range governed() {
		write(t, store, seeded.path, document(seeded.id, string(seeded.kind), "A document", nil, "active")+"\nThe document itself.\n")
	}
	before := snapshot(t, store.RepositoryRoot)

	title := "A title somebody else chose"
	for _, seeded := range governed() {
		owner, _ := Owner(seeded.kind)
		for _, role := range []domain.AgentRole{
			domain.RoleProductManager,
			domain.RoleArchitect,
			// The development manager owns no document at all: its decomposition is
			// Beads work rather than Markdown, so no kind is ever its to write.
			domain.RoleDevelopmentManager,
			domain.RoleDeveloper,
			domain.RoleReviewer,
			domain.AgentRole(""),
		} {
			if role == owner {
				continue
			}
			draft := Draft{
				ID:        "something-" + seeded.id,
				Kind:      seeded.kind,
				Title:     title,
				Directory: filepath.ToSlash(filepath.Dir(seeded.path)),
				Body:      "Intent nobody was authorized to state.",
				Reason:    "because",
			}
			if _, err := store.Create(role, draft, moment()); !errors.Is(err, ErrUnauthorized) {
				t.Fatalf("Create(%q, %q) error = %v, want ErrUnauthorized", role, seeded.kind, err)
			}
			if _, err := store.Amend(role, seeded.id, Amendment{Title: &title, Reason: "because"}, moment()); !errors.Is(err, ErrUnauthorized) {
				t.Fatalf("Amend(%q, %q) error = %v, want ErrUnauthorized", role, seeded.kind, err)
			}
			if _, err := store.Supersede(role, seeded.id, "because", moment()); !errors.Is(err, ErrUnauthorized) {
				t.Fatalf("Supersede(%q, %q) error = %v, want ErrUnauthorized", role, seeded.kind, err)
			}
			if _, err := store.Retire(role, seeded.id, "because", moment()); !errors.Is(err, ErrUnauthorized) {
				t.Fatalf("Retire(%q, %q) error = %v, want ErrUnauthorized", role, seeded.kind, err)
			}
		}
	}
	// The refusal is the whole point only if nothing reached the filesystem: a
	// mutation that was reported as refused and landed anyway is worse than one
	// that was allowed, because nobody goes looking for it.
	assertUnchanged(t, before, snapshot(t, store.RepositoryRoot))
}

func TestTheOwningRoleRecordsTheWholeLifecycle(t *testing.T) {
	t.Parallel()

	store := newStore(t)
	created, err := store.Create(domain.RoleProductManager, Draft{
		ID:        "brief",
		Kind:      KindBrief,
		Title:     "Product brief",
		Directory: productHome,
		Body:      "# Product brief\n\nIntent in, software out.",
		Reason:    "the first statement of what this product is for",
	}, moment())
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if created.Path != productHome+"/brief.md" || !created.InForce() {
		t.Fatalf("created = %#v", created)
	}
	if len(created.Revisions) != 1 || created.Revisions[0].By != domain.RoleProductManager || created.Revisions[0].Action != ActionCreated {
		t.Fatalf("revisions = %#v", created.Revisions)
	}
	// A second file for one identity would leave whatever refers to that id
	// holding a document nobody decided on, so creating over one is refused.
	if _, err := store.Create(domain.RoleProductManager, Draft{
		ID: "brief", Kind: KindBrief, Title: "Another brief", Directory: designsHome, Body: "Something else.", Reason: "because",
	}, moment()); err == nil {
		t.Fatal("Create() replaced an artifact that already exists")
	}

	title := "V1 product brief"
	supports := []string{}
	amended, err := store.Amend(domain.RoleProductManager, "brief", Amendment{
		Title:    &title,
		Supports: &supports,
		Reason:   "named the version it states intent for",
	}, moment().Add(time.Hour))
	if err != nil {
		t.Fatalf("Amend() error = %v", err)
	}
	if amended.Title != title || len(amended.Revisions) != 2 || amended.Revisions[1].Action != ActionAmended {
		t.Fatalf("amended = %#v", amended)
	}
	// Metadata is what an amendment touched, so the document below it is exactly
	// what was there: this package identifies documents rather than rewriting
	// them.
	if body := documentBody(t, store, amended.Path); body != "\n# Product brief\n\nIntent in, software out.\n" {
		t.Fatalf("body = %q", body)
	}

	prose := "# Product brief\n\nIntent in, software out. Reviewed with the code."
	if _, err := store.Amend(domain.RoleProductManager, "brief", Amendment{
		Body:   &prose,
		Reason: "said what makes the intent checkable",
	}, moment().Add(2*time.Hour)); err != nil {
		t.Fatalf("Amend() error = %v", err)
	}
	if body := documentBody(t, store, created.Path); body != "\n"+prose+"\n" {
		t.Fatalf("body = %q", body)
	}

	superseded, err := store.Supersede(domain.RoleProductManager, "brief", "replaced by the v2 brief", moment().Add(3*time.Hour))
	if err != nil {
		t.Fatalf("Supersede() error = %v", err)
	}
	ended, hasEnding := superseded.Ended()
	if superseded.InForce() || !hasEnding || ended.Action != ActionSuperseded || ended.By != domain.RoleProductManager {
		t.Fatalf("superseded = %#v", superseded)
	}
	// Replaced intent is history, and editing it back into force is not a decision
	// anybody made. What replaces it is a later artifact.
	if _, err := store.Amend(domain.RoleProductManager, "brief", Amendment{Title: &title, Reason: "because"}, moment()); err == nil {
		t.Fatal("Amend() edited a superseded artifact back into force")
	}
	if _, err := store.Retire(domain.RoleProductManager, "brief", "because", moment()); err == nil {
		t.Fatal("Retire() ended an artifact that had already ended")
	}

	// Everything written is read back by the loader that reads a hand-written
	// document, because a generated artifact and a hand-written one are the same
	// document.
	set, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(set.Problems) != 0 {
		t.Fatalf("problems = %v", set.Problems)
	}
	reloaded, found := set.Find("brief")
	if !found || reloaded.Status != StatusSuperseded || len(reloaded.Revisions) != 4 {
		t.Fatalf("reloaded = %#v", reloaded)
	}
}

func TestARevisionByARoleThatDoesNotOwnTheArtifactIsReported(t *testing.T) {
	t.Parallel()

	// The store is not the only way a file lands in an artifact home — an agent
	// has an editor in its worktree — so a log claiming the developer amended a
	// design is named every time the set is loaded.
	store := newStore(t)
	write(t, store, productHome+"/brief.md", document("brief", "brief", "Product brief", nil, "active")+"\nIntent in, software out.\n")
	write(t, store, designsHome+"/v1-harness.md", "---\nid: v1-harness\nkind: design\ntitle: V1 harness design\nsupports:\n    - brief\n"+
		"status: active\nrevisions:\n"+
		"    - action: created\n      by: architect\n      at: 2026-08-01T12:00:00Z\n      reason: recorded when the design was written\n"+
		"    - action: amended\n      by: developer\n      at: 2026-08-17T12:00:00Z\n      reason: the developer changed the design to match its change\n---\n\nThe design.\n")

	set, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	// Reported, not refused. The document still loads and still governs what is
	// downstream of it: dropping it would cascade into the orphan and dangling
	// reports for everything that referred to it, over a record nobody can now
	// lawfully correct, because the revision log is append-only.
	recorded, found := set.Find("v1-harness")
	if !found || !recorded.InForce() || len(set.Problems) != 0 {
		t.Fatalf("set = %#v", set)
	}
	problem, reported := problemOfKind(set.ReferenceProblems, ProblemUnauthorizedRevision)
	if !reported || problem.ID != "v1-harness" || problem.Path != designsHome+"/v1-harness.md" {
		t.Fatalf("reference problems = %#v", set.ReferenceProblems)
	}
	if !strings.Contains(problem.Reason, "revisions[1] records the developer") {
		t.Fatalf("problem = %#v", problem)
	}
	// Only the entry that crossed the boundary is named, and only once for the
	// document, because opening it and deciding is one job.
	if strings.Contains(problem.Reason, "revisions[0]") || len(set.ReferenceProblems) != 1 {
		t.Fatalf("reference problems = %#v", set.ReferenceProblems)
	}

	// The owner can still amend it, which is what makes the report something
	// somebody can act on rather than a document stuck outside every lawful path.
	title := "V1 harness design, as it now stands"
	amended, err := store.Amend(domain.RoleArchitect, "v1-harness", Amendment{Title: &title, Reason: "restated after the unauthorized edit was found"}, moment())
	if err != nil {
		t.Fatalf("Amend() error = %v", err)
	}
	if len(amended.Revisions) != 3 || amended.Revisions[2].By != domain.RoleArchitect {
		t.Fatalf("amended = %#v", amended)
	}
	// And the crossing stays reported afterwards: the log records what happened,
	// and an amendment by the owner does not unsay it.
	set, err = store.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if _, reported := problemOfKind(set.ReferenceProblems, ProblemUnauthorizedRevision); !reported {
		t.Fatalf("reference problems = %#v", set.ReferenceProblems)
	}
}

func TestAnArtifactIsOnlyWrittenWhereTheHarnessReadsOne(t *testing.T) {
	t.Parallel()

	store := newStore(t)
	before := snapshot(t, store.RepositoryRoot)
	for _, directory := range []string{
		// Outside every home: a document nothing reads is a worse outcome than a
		// refused mutation, because nobody finds out it was never governed.
		"docs/notes",
		// Inside the decisions home, but in the directory that carries the
		// invariants' own identity scheme.
		invariantsHome,
		"../elsewhere",
		"/etc",
		"",
	} {
		if _, err := store.Create(domain.RoleArchitect, Draft{
			ID: "stray-design", Kind: KindDesign, Title: "A design filed nowhere",
			Directory: directory, Body: "Something.", Reason: "because",
		}, moment()); err == nil {
			t.Fatalf("Create() wrote an artifact into %q", directory)
		}
	}
	// `README.md` is a directory index rather than intent anything refers to, and
	// the loader skips it, so an artifact that claimed the name would be one
	// nothing could ever read back.
	if _, err := store.Create(domain.RoleArchitect, Draft{
		ID: "readme", Kind: KindDesign, Title: "Not an artifact", Directory: designsHome, Body: "Something.", Reason: "because",
	}, moment()); err == nil {
		t.Fatal("Create() wrote an artifact into the directory index")
	}
	assertUnchanged(t, before, snapshot(t, store.RepositoryRoot))
}

// governed is one artifact of every kind the harness knows, in the home its
// owner keeps it in, so a test about ownership covers every governed artifact
// rather than the one kind somebody thought of.
func governed() []struct {
	id   string
	kind Kind
	path string
} {
	return []struct {
		id   string
		kind Kind
		path string
	}{
		{"brief", KindBrief, productHome + "/brief.md"},
		{"v1-goals", KindGoals, productHome + "/goals/v1-goals.md"},
		{"v1-non-goals", KindNonGoals, productHome + "/goals/v1-non-goals.md"},
		{"v1-harness", KindDesign, designsHome + "/v1-harness.md"},
		{"worktree-lifecycle", KindSpecification, designsHome + "/worktree-lifecycle.md"},
		{"markdown-source-of-truth", KindDecision, decisionsHome + "/markdown-source-of-truth.md"},
	}
}

func moment() time.Time {
	return time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
}

// documentBody returns what one artifact file holds below its frontmatter.
func documentBody(t *testing.T, store Store, relative string) string {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(store.RepositoryRoot, filepath.FromSlash(relative)))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	_, body, err := splitDocument(string(content))
	if err != nil {
		t.Fatalf("splitDocument() error = %v", err)
	}
	return body
}

// snapshot is every file in the repository and what it holds, which is how a
// refused mutation is shown to have written nothing rather than merely to have
// reported an error.
func snapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	found := map[string]string{}
	err := filepath.WalkDir(root, func(candidate string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(root, candidate)
		if err != nil {
			return err
		}
		content, err := os.ReadFile(candidate)
		if err != nil {
			return err
		}
		found[filepath.ToSlash(relative)] = string(content)
		return nil
	})
	if err != nil {
		t.Fatalf("walk the repository: %v", err)
	}
	return found
}

func assertUnchanged(t *testing.T, before, after map[string]string) {
	t.Helper()
	for path, content := range after {
		original, existed := before[path]
		if !existed {
			t.Fatalf("a refused mutation wrote %s", path)
		}
		if original != content {
			t.Fatalf("a refused mutation changed %s", path)
		}
	}
	for path := range before {
		if _, survived := after[path]; !survived {
			t.Fatalf("a refused mutation removed %s", path)
		}
	}
}
