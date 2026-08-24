package orchestrator

// What a run can read of the work tracker.
//
// The tracker's store is a database outside the worktree that takes a lock even
// to read, so a run cannot open it and reads the passive export beside it
// instead. Until this, that export was whatever the last commit carrying one
// held: on yoyodyne-ifd.117 it was four days old and did not contain the item
// the run was executing, and the run swept it for the work items citing a
// documentation anchor and got a confident wrong answer with nothing to say it
// was one.
//
// Both halves of the fix are pinned here, because either alone still produces
// that answer: the export is asked for after the item is claimed, and what the
// run is then told is the evidence rather than the intention — where the dump is
// and when it was last written, or that there is no current view at all.
//
// The age is not decoration. `bd export` completing proves nothing: beads keeps
// the JSONL dump only where a project turns it on, so an export can succeed and
// leave a four-day-old file exactly where it was. A prompt naming that file with
// no age beside it would be the ifd.117 answer again, with the harness vouching
// for it.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
)

func TestARunReadsATrackerExportRefreshedAfterItsOwnClaim(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	tracker := &fakeTracker{item: beads.WorkItem{
		ID: "yoyodyne-ifd.117", Title: "Split the configuration guide", Status: "open",
	}}
	export := filepath.Join(repository, beads.ExportPath)
	tracker.onExport = func() error {
		if err := os.MkdirAll(filepath.Dir(export), 0o700); err != nil {
			return err
		}
		return os.WriteFile(export, []byte(`{"id":"`+tracker.item.ID+`","status":"`+tracker.item.Status+`"}`+"\n"), 0o600)
	}

	prompt := developerPromptFrom(t, repository, tracker)
	if tracker.exports != 1 {
		t.Fatalf("exports = %d, want the dump refreshed once for this run", tracker.exports)
	}
	if !strings.Contains(prompt, "Read it at "+export+".") {
		t.Fatalf("the developer was not told where the current export is:\n%s", prompt)
	}
	// How old the dump is travels with the path, and it is the half that matters:
	// a clean export is not evidence of freshness, because beads keeps this file
	// only where a project asks it to. A path with no age beside it is exactly the
	// confident wrong answer this work item exists to stop.
	if !strings.Contains(prompt, "It was last written less than a minute ago (") {
		t.Fatalf("the developer was given a path with no age beside it:\n%s", prompt)
	}
	if !strings.Contains(prompt, "this run asked for it at ") {
		t.Fatalf("the developer cannot weigh the dump's age against this run:\n%s", prompt)
	}
	// The worktree's own copy is named as the one not to sweep, because it is the
	// one a developer finds by looking and the one this repository's documentation
	// cites by path.
	if !strings.Contains(prompt, beads.ExportPath+" inside your worktree") {
		t.Fatalf("the stale copy in the worktree was not distinguished from the current one:\n%s", prompt)
	}
	// The section names a path this machine has, so it must stay behind the work
	// item: everything in front of the item is the prefix a provider charges the
	// cached rate for, and no two checkouts share an absolute path.
	item, view := strings.Index(prompt, "# Assigned work item"), strings.Index(prompt, "# The work tracker as this run started")
	if item < 0 || view < 0 || view < item {
		t.Fatalf("the tracker view did not follow the work item: item = %d, view = %d\n%s", item, view, prompt)
	}
	// The export was written after the claim, so it holds the item this run is
	// executing in the state the claim left it. That is the whole of the ifd.117
	// case: the item that was running is in what the run can read.
	written, err := os.ReadFile(export)
	if err != nil {
		t.Fatalf("read the refreshed export: %v", err)
	}
	if !strings.Contains(string(written), `"id":"yoyodyne-ifd.117","status":"in_progress"`) {
		t.Fatalf("the export was refreshed before the claim rather than after it: %s", written)
	}
}

func TestARunWithNoCurrentTrackerExportIsToldWhyRatherThanLeftToTrustAStaleOne(t *testing.T) {
	t.Parallel()

	for name, test := range map[string]struct {
		onExport func() error
		cause    string
	}{
		// bd itself refused. A locked store is the ordinary shape of this: another
		// process is holding it, which is exactly why a run cannot read it either.
		"the export refused": {
			onExport: func() error { return errors.New("bd export failed: the tracker database is locked") },
			cause:    "the tracker database is locked",
		},
		// The export succeeded and wrote nothing where the harness looks. This is
		// not a corner: beads keeps the JSONL dump only where a project turns it on,
		// so `bd export` completing and leaving no file is the shipped default —
		// TestExportConformance in internal/beads holds that against bd itself.
		// Naming a file that is not there would be the same confident wrong answer
		// in a different costume.
		"the project keeps no dump": {onExport: nil, cause: "keeps no readable tracker dump"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			repository := pipelineRepository(t)
			tracker := &fakeTracker{item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
			tracker.onExport = test.onExport

			prompt := developerPromptFrom(t, repository, tracker)
			if strings.Contains(prompt, "Read it at ") {
				t.Fatalf("a run with no current export was pointed at one anyway:\n%s", prompt)
			}
			for _, want := range []string{
				"This run has no current view of the work tracker",
				test.cause,
				"Treat anything you take from it as unverified",
			} {
				if !strings.Contains(prompt, want) {
					t.Fatalf("the tracker section is missing %q:\n%s", want, prompt)
				}
			}
		})
	}
}

// developerPromptFrom runs one work item through a pipeline over the given
// repository and returns the single prompt the developer was handed.
func developerPromptFrom(t *testing.T, repository string, tracker *fakeTracker) string {
	t.Helper()
	provider := roleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	pipeline, _ := newPipeline(t, repository, tracker, provider, []string{"exit 0"})
	if _, err := pipeline.Run(context.Background(), tracker.item.ID); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	requests := provider.requestsForRole(domain.RoleDeveloper)
	if len(requests) != 1 {
		t.Fatalf("developer invocations = %d, want 1", len(requests))
	}
	return requests[0].Prompt
}
