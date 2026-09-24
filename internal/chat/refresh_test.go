package chat

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// maxTurnAfterRefreshBytes is what a turn after a refresh may carry in these
// tests, against a picture of about a megabyte. What moved in them is a line or
// two, so anything near the picture's size is the whole picture sent again.
const maxTurnAfterRefreshBytes = 32 << 10

// syntheticPicture is an assembled product context of about a megabyte, shaped
// the way AssembleProduct shapes one: a header, specifications, shipped
// documentation, and the work items. The brief and the work items are what the
// tests move.
func syntheticPicture(brief string, items []string) string {
	var picture strings.Builder
	picture.WriteString("# Product context\n\nThe sections below are the product as this repository records it today.\n")
	fmt.Fprintf(&picture, "\n## Specification: docs/product/brief.md\n\n# Brief\n\n## Goals\n\n%s\n", brief)
	for document := 0; document < 40; document++ {
		fmt.Fprintf(&picture, "\n### Shipped documentation: docs/shipped-%02d.md\n\n# Shipped %d\n\n", document, document)
		for line := 0; line < 380; line++ {
			fmt.Fprintf(&picture, "Line %d of shipped document %d, which says what the product does today.\n", line, document)
		}
	}
	picture.WriteString("\n## Beads work items\n\n")
	for _, item := range items {
		picture.WriteString("- " + item + "\n")
	}
	return picture.String()
}

func productManagerIdentity() runstate.ConversationIdentity {
	return runstate.ConversationIdentity{Agent: string(domain.RoleProductManager), Role: domain.RoleProductManager}
}

// The development manager's conversation of 2026-09-23 replayed in small: a
// conversation briefed once, re-read past the threshold, a turn that fails and
// is asked again, and a second re-read. Every turn after the first carries what
// moved rather than the picture, the recorded picture advances with each
// re-read whether or not the turn delivering it lands, and the second re-read's
// changes are measured from what the first one delivered.
func TestTurnsAcrossTwoRefreshesCarryOnlyWhatMoved(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	first := syntheticPicture("Run development nearly autonomously.", []string{
		"yoyodyne-ifd.1 [open, p1] Carry what moved",
		"yoyodyne-ifd.2 [open, p2] Keep the picture current",
	})
	second := syntheticPicture("Run development nearly autonomously.", []string{
		"yoyodyne-ifd.1 [closed, p1] Carry what moved",
		"yoyodyne-ifd.2 [open, p2] Keep the picture current",
	})
	third := syntheticPicture("Run development autonomously, with the operator reading the product manager.", []string{
		"yoyodyne-ifd.1 [closed, p1] Carry what moved",
		"yoyodyne-ifd.2 [open, p2] Keep the picture current",
	})
	if len(first) < 1<<20 {
		t.Fatalf("the synthetic picture is %d bytes, want about a megabyte", len(first))
	}
	provider := &fakeBackend{
		results: []backendapi.RunResult{
			{SessionID: "session-40", ResolvedModel: "claude-opus-5", FinalText: "Briefed."},
			{},
			{},
			{SessionID: "session-40", ResolvedModel: "claude-opus-5", FinalText: "ifd.1 closed."},
			{SessionID: "session-40", ResolvedModel: "claude-opus-5", FinalText: "The brief moved."},
			{SessionID: "session-40", ResolvedModel: "claude-opus-5", FinalText: "Nothing new."},
		},
		errs: []error{nil, errors.New("the turn was refused"), errors.New("the turn was refused again")},
	}
	ground := &fakeGround{}
	options := testOptions(t, provider)
	options.Store = newTestStore(t, root)
	options.Ground = ground
	options.Briefing = Briefing{Text: first, GatheredAt: gatheredAt, Commit: "a1a1a1a1a1a1a1a1"}
	session := openTestSession(t, options)

	if _, err := session.Send(context.Background(), "first"); err != nil {
		t.Fatalf("first Send() error = %v", err)
	}
	if len(provider.requests[0].Prompt) < len(first) {
		t.Fatalf("the first turn carried %d bytes, want the whole picture", len(provider.requests[0].Prompt))
	}

	// The branch moves past the threshold and the turn that would deliver the
	// re-read fails, twice.
	ground.movement = Movement{Commits: 30, TrackerChanges: 1}
	ground.briefing = Briefing{Text: second, GatheredAt: fixedClock{}.Now(), Commit: "b2b2b2b2b2b2b2b2"}
	for _, message := range []string{"second", "second again"} {
		if _, err := session.Send(context.Background(), message); err == nil {
			t.Fatalf("Send(%q) was expected to fail", message)
		}
	}
	if ground.gathers != 1 {
		t.Fatalf("the repository was read %d time(s) for one stale picture, want once", ground.gathers)
	}
	recorded, err := newTestStore(t, root).Load(productManagerIdentity())
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if recorded.ContextCommit != "b2b2b2b2b2b2b2b2" || !recorded.ContextGatheredAt.Equal(fixedClock{}.Now()) {
		t.Fatalf("the recorded picture = %s at %s, want it advanced by the re-read though no turn landed",
			recorded.ContextGatheredAt, recorded.ContextCommit)
	}

	ground.movement = Movement{Commits: 2}
	if _, err := session.Send(context.Background(), "third"); err != nil {
		t.Fatalf("third Send() error = %v", err)
	}
	for index := 1; index <= 3; index++ {
		prompt := provider.requests[index].Prompt
		if len(prompt) > maxTurnAfterRefreshBytes {
			t.Fatalf("turn %d after the first carried %d bytes, want what moved and not the %d-byte picture", index+1, len(prompt), len(second))
		}
		for _, required := range []string{
			"# Refreshed product context",
			"30 landings behind the target branch",
			"## Changed: Beads work items",
			"-- yoyodyne-ifd.1 [open, p1] Carry what moved",
			"+- yoyodyne-ifd.1 [closed, p1] Carry what moved",
		} {
			if !strings.Contains(prompt, required) {
				t.Fatalf("turn %d = %q, want it to carry %q", index+1, prompt, required)
			}
		}
		if strings.Contains(prompt, "Shipped documentation") {
			t.Fatalf("turn %d carried a section nothing moved in: %q", index+1, prompt)
		}
	}

	// A second re-read is measured from what the first one delivered: the work
	// item it already reported is not reported again.
	ground.movement = Movement{Commits: 25}
	ground.briefing = Briefing{Text: third, GatheredAt: fixedClock{}.Now(), Commit: "c3c3c3c3c3c3c3c3"}
	if _, err := session.Send(context.Background(), "fourth"); err != nil {
		t.Fatalf("fourth Send() error = %v", err)
	}
	prompt := provider.requests[4].Prompt
	if len(prompt) > maxTurnAfterRefreshBytes {
		t.Fatalf("the turn after the second refresh carried %d bytes", len(prompt))
	}
	for _, required := range []string{
		"## Changed: Specification: docs/product/brief.md",
		"@@ ## Goals",
		"-Run development nearly autonomously.",
		"+Run development autonomously, with the operator reading the product manager.",
	} {
		if !strings.Contains(prompt, required) {
			t.Fatalf("the turn after the second refresh = %q, want it to carry %q", prompt, required)
		}
	}
	if strings.Contains(prompt, "yoyodyne-ifd.1") {
		t.Fatalf("the second refresh repeated what the first delivered: %q", prompt)
	}

	// And the turn after that carries no picture at all.
	ground.movement = Movement{}
	if _, err := session.Send(context.Background(), "fifth"); err != nil {
		t.Fatalf("fifth Send() error = %v", err)
	}
	if strings.Contains(provider.requests[5].Prompt, "# Refreshed product context") {
		t.Fatalf("the turn after a delivered refresh carried it again: %q", provider.requests[5].Prompt)
	}
	recorded, err = newTestStore(t, root).Load(productManagerIdentity())
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if recorded.ContextCommit != "c3c3c3c3c3c3c3c3" || recorded.PendingPicture != nil {
		t.Fatalf("the recorded picture = %q with %#v waiting, want the second re-read delivered", recorded.ContextCommit, recorded.PendingPicture)
	}
}

// A re-read carried out of a process whose turn failed can be any age by the
// time something delivers it. One that has itself fallen past the threshold is
// read again, and the role is told everything that moved since the picture it
// last received rather than only since the carried one.
func TestACarriedReReadPastTheThresholdIsReadAgainAndOwesEverythingSince(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	first := syntheticPicture("Brief.", []string{"yoyodyne-ifd.1 [open, p1] One", "yoyodyne-ifd.2 [open, p1] Two"})
	second := syntheticPicture("Brief.", []string{"yoyodyne-ifd.1 [closed, p1] One", "yoyodyne-ifd.2 [open, p1] Two"})
	third := syntheticPicture("Brief.", []string{"yoyodyne-ifd.1 [closed, p1] One", "yoyodyne-ifd.2 [closed, p1] Two"})

	provider := &fakeBackend{
		results: []backendapi.RunResult{{SessionID: "session-41", ResolvedModel: "claude-opus-5", FinalText: "Briefed."}},
		errs:    []error{nil, errors.New("the turn was refused")},
	}
	ground := &fakeGround{}
	options := testOptions(t, provider)
	options.Store = newTestStore(t, root)
	options.Ground = ground
	options.Briefing = Briefing{Text: first, GatheredAt: gatheredAt, Commit: "a1a1a1a1a1a1a1a1"}
	session := openTestSession(t, options)
	if _, err := session.Send(context.Background(), "first"); err != nil {
		t.Fatalf("first Send() error = %v", err)
	}
	ground.movement = Movement{Commits: 30}
	ground.briefing = Briefing{Text: second, GatheredAt: fixedClock{}.Now(), Commit: "b2b2b2b2b2b2b2b2"}
	if _, err := session.Send(context.Background(), "second"); err == nil {
		t.Fatal("the second turn was expected to fail")
	}

	resumedProvider := &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-41", ResolvedModel: "claude-opus-5", FinalText: "Both closed."},
	}}
	resumedGround := &fakeGround{
		movement: Movement{Commits: 25},
		briefing: Briefing{Text: third, GatheredAt: fixedClock{}.Now(), Commit: "c3c3c3c3c3c3c3c3"},
	}
	resumedOptions := testOptions(t, resumedProvider)
	resumedOptions.Store = newTestStore(t, root)
	resumedOptions.Ground = resumedGround
	resumed := openTestSession(t, resumedOptions)
	reply, err := resumed.Send(context.Background(), "third")
	if err != nil {
		t.Fatalf("Send() in the next process error = %v", err)
	}
	if resumedGround.gathers != 1 {
		t.Fatalf("the stale carried picture was read %d time(s), want once", resumedGround.gathers)
	}
	prompt := resumedProvider.requests[0].Prompt
	if len(prompt) > maxTurnAfterRefreshBytes {
		t.Fatalf("the turn carried %d bytes, want what moved", len(prompt))
	}
	for _, required := range []string{
		"55 landings behind the target branch",
		"+- yoyodyne-ifd.1 [closed, p1] One",
		"+- yoyodyne-ifd.2 [closed, p1] Two",
	} {
		if !strings.Contains(prompt, required) {
			t.Fatalf("the turn = %q, want it to carry %q", prompt, required)
		}
	}
	if reply.Picture == nil || reply.Picture.Commit != "a1a1a1a1a1a1a1a1" || reply.Picture.RefreshedTo != "c3c3c3c3c3c3c3c3" || reply.Picture.Landings != 55 {
		t.Fatalf("reply.Picture = %#v, want the move from the picture the role held to the new read", reply.Picture)
	}
}

// A conversation whose last delivered picture was never kept — one recorded
// before it was — has nothing to compare a refresh against, and is given the
// whole picture once. The one after that is carried as changes.
func TestARefreshWithNothingToCompareAgainstCarriesTheWholePicture(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	first := syntheticPicture("Brief.", []string{"yoyodyne-ifd.1 [open, p1] One"})
	second := syntheticPicture("Brief.", []string{"yoyodyne-ifd.1 [closed, p1] One"})
	provider := &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-42", ResolvedModel: "claude-opus-5", FinalText: "Briefed."},
		{SessionID: "session-42", ResolvedModel: "claude-opus-5", FinalText: "Re-briefed."},
	}}
	ground := &fakeGround{}
	options := testOptions(t, provider)
	options.Store = newTestStore(t, root)
	options.Ground = ground
	options.Briefing = Briefing{Text: first, GatheredAt: gatheredAt, Commit: "a1a1a1a1a1a1a1a1"}
	session := openTestSession(t, options)
	if _, err := session.Send(context.Background(), "first"); err != nil {
		t.Fatalf("first Send() error = %v", err)
	}
	if err := newTestStore(t, root).ClearDeliveredPictureText(productManagerIdentity()); err != nil {
		t.Fatalf("ClearDeliveredPictureText() error = %v", err)
	}
	ground.movement = Movement{Commits: 30}
	ground.briefing = Briefing{Text: second, GatheredAt: fixedClock{}.Now(), Commit: "b2b2b2b2b2b2b2b2"}
	if _, err := session.Send(context.Background(), "second"); err != nil {
		t.Fatalf("second Send() error = %v", err)
	}
	if prompt := provider.requests[1].Prompt; !strings.Contains(prompt, second) {
		t.Fatalf("the refresh with nothing to compare against carried %d bytes, want the whole picture", len(prompt))
	}
	kept, err := newTestStore(t, root).DeliveredPictureText(productManagerIdentity())
	if err != nil {
		t.Fatalf("DeliveredPictureText() error = %v", err)
	}
	if kept != second {
		t.Fatalf("the delivered picture kept = %d bytes, want the picture the turn delivered", len(kept))
	}
}

// A turn carried as changes that has to be rebuilt for a provider holding no
// session is given the whole picture in front of them: what moved means nothing
// to a provider that never held the picture it moved from.
func TestARebuiltTurnCarriesTheWholePictureAChangeRefersTo(t *testing.T) {
	t.Parallel()

	first := syntheticPicture("Brief.", []string{"yoyodyne-ifd.1 [open, p1] One"})
	second := syntheticPicture("Brief.", []string{"yoyodyne-ifd.1 [closed, p1] One"})
	provider := &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-43", ResolvedModel: "claude-opus-5", FinalText: "Briefed."},
	}}
	ground := &fakeGround{}
	options := testOptions(t, provider)
	options.Ground = ground
	options.Briefing = Briefing{Text: first, GatheredAt: gatheredAt, Commit: "a1a1a1a1a1a1a1a1"}
	session := openTestSession(t, options)
	if _, err := session.Send(context.Background(), "first"); err != nil {
		t.Fatalf("first Send() error = %v", err)
	}
	ground.movement = Movement{Commits: 30}
	ground.briefing = Briefing{Text: second, GatheredAt: fixedClock{}.Now(), Commit: "b2b2b2b2b2b2b2b2"}
	if _, err := session.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}
	prompt := session.turnPrompt("second", nil)
	if !session.carriedChanges || strings.Contains(prompt, "Line 0 of shipped document 0") {
		t.Fatalf("the turn = %d bytes, want the refresh carried as changes", len(prompt))
	}
	if got := session.workingBriefing(); got != strings.TrimSpace(second) {
		t.Fatalf("the rebuilt briefing = %d bytes, want the whole of the new picture", len(got))
	}
}

func TestPictureChangesNameWhatMovedAndNothingElse(t *testing.T) {
	t.Parallel()

	was := "# Product context\n\nHeader.\n\n## Specification: docs/a.md\n\n# A\n\n## Goals\n\nOne.\nTwo.\nThree.\n\n## Specification: docs/gone.md\n\nGone.\n\n## Beads work items\n\n- x [open]\n"
	now := "# Product context\n\nHeader.\n\n## Specification: docs/a.md\n\n# A\n\n## Goals\n\nOne.\nTwo, amended.\nThree.\n\n## Specification: docs/new.md\n\nNew.\n\n## Beads work items\n\n- x [open]\n"
	changes := pictureChanges(was, now)
	for _, required := range []string{
		"## Changed: Specification: docs/a.md\n\n@@ ## Goals\n \n One.\n-Two.\n+Two, amended.\n Three.\n",
		"## New: Specification: docs/new.md\n\n\nNew.\n",
		"## Gone: Specification: docs/gone.md\n",
	} {
		if !strings.Contains(changes, required) {
			t.Fatalf("pictureChanges() = %q, want it to carry %q", changes, required)
		}
	}
	for _, unchanged := range []string{"Header.", "Beads work items"} {
		if strings.Contains(changes, unchanged) {
			t.Fatalf("pictureChanges() = %q, named %q, which did not move", changes, unchanged)
		}
	}
	if got := pictureChanges(was, was); !strings.Contains(got, "Nothing in the picture differs") {
		t.Fatalf("pictureChanges() of one picture = %q, want it to say nothing differs", got)
	}
}

// A section rewritten past what a line comparison is worth is given whole
// rather than as a comparison that would cost more than the section.
func TestASectionRewrittenPastTheBoundIsGivenWhole(t *testing.T) {
	t.Parallel()

	var was, now strings.Builder
	was.WriteString("## Beads work items\n\n")
	now.WriteString("## Beads work items\n\n")
	for item := 0; item < maxSectionEdits; item++ {
		fmt.Fprintf(&was, "- item-%d [open]\n", item)
		fmt.Fprintf(&now, "- item-%d [closed]\n", item)
	}
	changes := pictureChanges(was.String(), now.String())
	if !strings.HasPrefix(changes, "## Changed, given whole: Beads work items\n") || !strings.Contains(changes, "- item-7 [closed]") {
		t.Fatalf("pictureChanges() = %.300q, want the section given whole", changes)
	}
}

// The comparison is exact: every line of the older section and every line of the
// newer one is accounted for, in order, whatever moved.
func TestDiffLinesAccountsForEveryLine(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct{ was, now string }{
		{"", "a\nb"},
		{"a\nb", ""},
		{"a\nb\nc", "a\nc"},
		{"a\nb\nc", "x\na\nb\nc\ny"},
		{"a\nb\nc\nd\ne", "e\nd\nc\nb\na"},
		{"one\ntwo\nthree\nfour", "one\n2\nthree\n4\nfive"},
	} {
		was, now := splitNonEmpty(testCase.was), splitNonEmpty(testCase.now)
		ops, ok := diffLines(was, now, 100)
		if !ok {
			t.Fatalf("diffLines(%q, %q) gave up", testCase.was, testCase.now)
		}
		var older, newer []string
		for _, op := range ops {
			if op.kind != '+' {
				older = append(older, op.line)
			}
			if op.kind != '-' {
				newer = append(newer, op.line)
			}
		}
		if !equalLines(older, was) || !equalLines(newer, now) {
			t.Fatalf("diffLines(%q, %q) = %#v, which does not rebuild both sides", testCase.was, testCase.now, ops)
		}
	}
}

func splitNonEmpty(text string) []string {
	if text == "" {
		return nil
	}
	return strings.Split(text, "\n")
}
