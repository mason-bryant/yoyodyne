package admission

import (
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/beads"
)

// backlog is a slice of this repository's own tracker, taken as it stood when
// the two incidents happened. The weighting this judges scope by is measured
// over whatever titles it is given, so a corpus of invented sentences would
// calibrate the guard against a vocabulary no backlog has: the words below are
// the real ones, and "run", "change", "harness", and "operator" recur in them
// exactly as often as they recur in the thing being judged.
func backlog() []beads.WorkItem {
	titles := []struct{ id, title, parent, status string }{
		{"yoyodyne-ifd.229", "The refreshed export cannot become a run's own change: the skip-worktree guard is enforced, not assumed", "", "closed"},
		{"yoyodyne-ifd.241", "Bundle-improvement notices speak unprompted, and each new one DMs the operator once", "", "blocked"},
		{"yoyodyne-ifd.241.1", "The architect rules whether the DM tier widens to advisory drift notices", "yoyodyne-ifd.241", "closed"},
		{"yoyodyne-ifd.241.2", "Each newly-available bundle improvement DMs the operator once, per the architect's ruling", "yoyodyne-ifd.241", "open"},
		{"yoyodyne-ifd.183", "Developers probe execution at minute zero, and evidence what the checks would judge", "", "closed"},
		{"yoyodyne-ifd.206", "The coined-terms sweep names every term the shipped surfaces use", "", "closed"},
		{"yoyodyne-ifd.238", "A developer run writes its scratch files to the directory the harness cut for it", "", "closed"},
		{"yoyodyne-ifd.247", "Every run is given a scratch directory nobody has to remember to name", "", "closed"},
		{"yoyodyne-ifd.276", "The attribution audit reads closed work as well as open", "", "closed"},
		{"yoyodyne-ifd.281", "Work is not admitted twice from one source, and a satisfied item is caught before dispatch", "", "open"},
		{"yoyodyne-ifd.292", "The operator's pause reaches a run that has already started", "", "closed"},
		{"yoyodyne-ifd.295", "Stall detection runs without Slack", "", "open"},
		{"yoyodyne-ifd.299", "The briefing says what the tracker could not be read for", "", "open"},
		{"yoyodyne-ifd.130.2", "The product manager is a headless service and the chat client is thin", "yoyodyne-ifd.130", "open"},
		{"yoyodyne-ifd.130.3", "The recovery surface projects the same read model as the dashboard", "yoyodyne-ifd.130", "open"},
		{"yoyodyne-ifd.209.14", "The reviewer's evidence carries the invariants the work item was given", "yoyodyne-ifd.209", "closed"},
		{"yoyodyne-ifd.209.8", "Integration refuses a candidate whose review was minted for another revision", "yoyodyne-ifd.209", "closed"},
	}
	items := make([]beads.WorkItem, 0, len(titles))
	for _, entry := range titles {
		items = append(items, beads.WorkItem{
			ID:     entry.id,
			Title:  entry.title,
			Parent: entry.parent,
			Status: entry.status,
		})
	}
	return items
}

// admittedFrom is one item whose record says which report or directive the work
// came from, written the way the harness writes it onto an item.
func admittedFrom(id, title, source string) beads.WorkItem {
	return beads.WorkItem{
		ID:     id,
		Title:  title,
		Status: "closed",
		Notes:  "Admitted to the backlog by the product manager in conversation chat-91253e0e, after turn 408.\n\nAdmitted from report " + source + ", filed at \"warning\" by the developer.",
	}
}

func ids(matches []Match) []string {
	found := make([]string, 0, len(matches))
	for _, match := range matches {
		found = append(found, match.ID)
	}
	return found
}

// The 274/229 shape: two admissions from one developer report, the second after
// the first had landed. The wording moved between them, so what has to catch it
// is the source both cite rather than anything they have in common to read.
func TestWorkAdmittedFromASourceIsNotAdmittedFromItTwice(t *testing.T) {
	t.Parallel()

	const source = "report-3f2ac1904e6b48d0b5e7c2a10d9f4a77"
	admitted := append(backlog(), admittedFrom("yoyodyne-ifd.229",
		"The refreshed export cannot become a run's own change: the skip-worktree guard is enforced, not assumed", source))

	matches := Resembling(Candidate{
		Title:   "The tracker export cannot be smuggled into a run's committed change",
		Sources: []string{source},
	}, admitted)

	if got := ids(matches); len(got) != 1 || got[0] != "yoyodyne-ifd.229" {
		t.Fatalf("Resembling() = %v, want the item already admitted from that report", got)
	}
	if !strings.Contains(matches[0].Because, source) {
		t.Fatalf("the match does not name the source it was found by: %q", matches[0].Because)
	}
	// The source travels apart from the sentence, because what to do about a
	// source match is not what to do about a scope one: one record can genuinely
	// prompt a second piece of work, admitted without the citation.
	if matches[0].Source != source {
		t.Fatalf("match source = %q, want %q", matches[0].Source, source)
	}
	// The duplicate is of work that has already landed, which is the whole reason
	// it costs a run: whoever is told has to be able to read that it is closed.
	if matches[0].Status != "closed" {
		t.Fatalf("match status = %q, want the state the tracker holds it in", matches[0].Status)
	}
}

// The same shape with nothing to connect the two. A candidate citing no source
// is not compared against every closed item in the backlog by its wording: that
// is the judgement this deliberately does not make outside a decomposition.
func TestWorkCitingNoSourceIsNotMatchedAgainstTheWholeBacklog(t *testing.T) {
	t.Parallel()

	matches := Resembling(Candidate{
		Title: "The tracker export cannot be smuggled into a run's committed change",
	}, backlog())

	if len(matches) != 0 {
		t.Fatalf("Resembling() = %v, want nothing without a source to match on", ids(matches))
	}
}

// The 241.2/241.4 shape: one parent decomposed a second time into children
// carrying the scope its existing children already carry.
func TestASecondDecompositionOfOneParentIsCaught(t *testing.T) {
	t.Parallel()

	for _, replayed := range []struct {
		name  string
		title string
		want  string
	}{
		{
			name:  "241.4 replays 241.2",
			title: "Each newly-available improvement DMs the operator once, deduplicated, per the ruling",
			want:  "yoyodyne-ifd.241.2",
		},
		{
			name:  "241.3 replays 241.1",
			title: "The architect rules whether the Slack DM tier widens to advisory drift notices",
			want:  "yoyodyne-ifd.241.1",
		},
	} {
		t.Run(replayed.name, func(t *testing.T) {
			t.Parallel()

			matches := Resembling(Candidate{
				Title:  replayed.title,
				Parent: "yoyodyne-ifd.241",
			}, backlog())

			if got := ids(matches); len(got) != 1 || got[0] != replayed.want {
				t.Fatalf("Resembling() = %v, want %s", got, replayed.want)
			}
			if !strings.Contains(matches[0].Because, "yoyodyne-ifd.241") {
				t.Fatalf("the match does not name the parent it was found under: %q", matches[0].Because)
			}
			// A scope match names no source, which is what says the work is already
			// carved out rather than that one record prompted it twice.
			if matches[0].Source != "" {
				t.Fatalf("match source = %q, want none on a match found by scope", matches[0].Source)
			}
		})
	}
}

// A child that is genuinely a different piece of the same parent is admitted.
// A guard that fired on every sibling would be one whoever decomposes reads past,
// which is the failure mode it exists to avoid rather than a cost to accept.
func TestADifferentChildOfTheSameParentIsNotAMatch(t *testing.T) {
	t.Parallel()

	matches := Resembling(Candidate{
		Title:  "A conversation resumed in another process decides the proposals the first one recorded",
		Parent: "yoyodyne-ifd.130",
	}, backlog())

	if len(matches) != 0 {
		t.Fatalf("Resembling() = %v, want nothing", ids(matches))
	}
}

// The scope question is asked among the children of one parent and nowhere else.
// Two items worded alike in different families are two items, and a backlog-wide
// resemblance is what would make that a refusal.
func TestScopeIsJudgedOnlyAmongTheChildrenOfOneParent(t *testing.T) {
	t.Parallel()

	sameWording := "Each newly-available bundle improvement DMs the operator once, per the architect's ruling"
	for _, parent := range []string{"", "yoyodyne-ifd.130"} {
		matches := Resembling(Candidate{Title: sameWording, Parent: parent}, backlog())
		if len(matches) != 0 {
			t.Fatalf("Resembling() under parent %q = %v, want nothing", parent, ids(matches))
		}
	}
}

// A title too short to have a scope is not compared by one. Two three-word
// titles share all of the little they say whenever they overlap at all.
func TestAShortTitleIsNotJudgedByItsScope(t *testing.T) {
	t.Parallel()

	admitted := append(backlog(), beads.WorkItem{
		ID:     "yoyodyne-ifd.241.9",
		Title:  "Slack notices",
		Parent: "yoyodyne-ifd.241",
		Status: "open",
	})

	matches := Resembling(Candidate{Title: "Slack notices", Parent: "yoyodyne-ifd.241"}, admitted)
	if len(matches) != 0 {
		t.Fatalf("Resembling() = %v, want nothing from two titles with no scope to compare", ids(matches))
	}
}

// A source identifier that is the beginning of another one cites the item it
// names and not the item whose identifier it is a prefix of.
func TestASourceCitationIsNotAPrefixMatch(t *testing.T) {
	t.Parallel()

	admitted := []beads.WorkItem{
		admittedFrom("yoyodyne-ifd.100", "Work admitted from a longer identifier", "report-abc123def456"),
	}
	if matches := Resembling(Candidate{Title: "Something else", Sources: []string{"report-abc123"}}, admitted); len(matches) != 0 {
		t.Fatalf("Resembling() = %v, want nothing: report-abc123 is a prefix rather than the citation", ids(matches))
	}
	if matches := Resembling(Candidate{Title: "Something else", Sources: []string{"report-abc123def456"}}, admitted); len(matches) != 1 {
		t.Fatalf("Resembling() = %v, want the item that cites exactly that report", ids(matches))
	}
}

// An item's record is prose, so a citation at the end of a sentence has a full
// stop after it. Reading that as part of the identifier is a guard that finds no
// citation anybody wrote a sentence around.
func TestACitationIsFoundWhereverThePunctuationPutsIt(t *testing.T) {
	t.Parallel()

	const source = "report-abc123def456abc123def456abcd"
	for _, notes := range []string{
		"Admitted from report " + source + ".",
		"Admitted from report " + source,
		"Admitted from report " + source + ", filed at \"warning\".",
		"(admitted from " + source + ")",
		"In answer to " + source + "; nothing else.",
	} {
		admitted := []beads.WorkItem{{ID: "yoyodyne-ifd.100", Title: "Some work", Notes: notes}}
		if matches := Resembling(Candidate{Title: "Something else", Sources: []string{source}}, admitted); len(matches) != 1 {
			t.Fatalf("Resembling() over notes %q = %v, want the citation found", notes, ids(matches))
		}
	}
}

// Both questions are asked of one candidate, and the source answer comes first
// because it is the one whoever is told can check without a judgement.
func TestSourceMatchesAreReportedBeforeScopeMatches(t *testing.T) {
	t.Parallel()

	const source = "directive-0123456789abcdef0123456789abcdef"
	admitted := append(backlog(), beads.WorkItem{
		ID:     "yoyodyne-ifd.290",
		Title:  "Something the operator asked for",
		Status: "open",
		Notes:  "In answer to directive " + source + ", received by the product manager.",
	})

	matches := Resembling(Candidate{
		Title:   "The architect rules whether the Slack DM tier widens to advisory drift notices",
		Parent:  "yoyodyne-ifd.241",
		Sources: []string{source},
	}, admitted)

	if got := ids(matches); len(got) != 2 || got[0] != "yoyodyne-ifd.290" || got[1] != "yoyodyne-ifd.241.1" {
		t.Fatalf("Resembling() = %v, want the source match first", got)
	}
}

func TestDescribeNamesTheMatchesAndSaysHowManyItLeftOut(t *testing.T) {
	t.Parallel()

	if described := Describe(nil); described != "" {
		t.Fatalf("Describe(nil) = %q, want nothing", described)
	}
	matches := []Match{
		{ID: "a", Title: "first", Status: "closed", Because: "it was admitted from report-1, which this admission cites too"},
		{ID: "b", Title: "second", Because: "it is already a child of p carrying this scope"},
		{ID: "c", Title: "third", Status: "open", Because: "because"},
		{ID: "d", Title: "fourth", Status: "open", Because: "because"},
	}
	described := Describe(matches)
	for _, want := range []string{"a (closed)", `b (state unrecorded) "second"`, "c (open)", "1 further item(s) match"} {
		if !strings.Contains(described, want) {
			t.Fatalf("Describe() = %q, want it to contain %q", described, want)
		}
	}
	if strings.Contains(described, `"fourth"`) {
		t.Fatalf("Describe() = %q, want the fourth match counted rather than named", described)
	}
}

// A tracker holding almost nothing weights every word it has never seen as the
// rarest there is, and must not come out negative: a negative weight would score
// two titles as less alike the more they agreed. The ordinary inverse document
// frequency does go negative on a corpus this small, which is why it is not the
// one used here.
func TestWordWeightsStayPositiveOnASmallBacklog(t *testing.T) {
	t.Parallel()

	for _, titles := range []int{1, 2, 3, 400} {
		for _, count := range []int{0, 1, titles} {
			if weight := wordWeight(titles, count); weight <= 0 {
				t.Fatalf("wordWeight(%d, %d) = %v, want a positive weight", titles, count, weight)
			}
		}
	}
}

// Nothing to compare against is nothing matched, rather than a division nobody
// meant to do.
func TestAnEmptyTrackerMatchesNothing(t *testing.T) {
	t.Parallel()

	if matches := Resembling(Candidate{Title: "anything at all", Parent: "p", Sources: []string{"report-1"}}, nil); len(matches) != 0 {
		t.Fatalf("Resembling() = %v, want nothing", ids(matches))
	}
}
