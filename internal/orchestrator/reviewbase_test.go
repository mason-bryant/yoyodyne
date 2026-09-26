package orchestrator

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
)

// A reviewer judging an extraction is given the source file as the change's own
// base holds it, labelled with that commit, however far the checkout has moved
// on by the time the review is asked for. yoyodyne-ifd.117.3 spent three repair
// rounds on the other arrangement: guides extracted correctly from an older
// docs/configuration.md, judged against the file as main had it later, read as
// divergent over passages that were never wrong.
func TestReviewerJudgesAnExtractionAgainstTheSourceAtTheChangesBase(t *testing.T) {
	t.Parallel()

	const (
		baseGuide  = "# Guide\n\n## Settings\n\nThe setting is spelled `alpha` at the base.\n"
		laterGuide = "# Guide\n\n## Settings\n\nThe setting was renamed `omega` after the branch was cut.\n"
		extracted  = "# Settings\n\nThe setting is spelled `alpha` at the base.\n"
	)
	repository := pipelineRepository(t)
	if err := os.WriteFile(filepath.Join(repository, "docs", "guide.md"), []byte(baseGuide), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	runPipelineGit(t, repository, "add", ".")
	runPipelineGit(t, repository, "commit", "-m", "the guide as the branch will be cut from it")
	base := gitLine(t, repository, "rev-parse", "HEAD")

	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{
		ID:                 "yoyodyne-task",
		Title:              "Extract the settings section",
		Description:        "Move the Settings section of docs/guide.md into docs/settings.md.",
		AcceptanceCriteria: "docs/settings.md carries the Settings section as docs/guide.md states it",
		Status:             "open",
	}}
	var reviewerPrompts []string
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		if err := os.WriteFile(filepath.Join(request.WorkingDirectory, "docs", "settings.md"), []byte(extracted), 0o600); err != nil {
			return err
		}
		// Something else is promoted while this change is being written: the
		// checkout the harness reads from now holds a later revision of the
		// source than the one the branch was cut from.
		if err := os.WriteFile(filepath.Join(repository, "docs", "guide.md"), []byte(laterGuide), 0o600); err != nil {
			return err
		}
		runPipelineGit(t, repository, "commit", "-am", "an unrelated promotion rewrites the guide")
		return nil
	}, approveVerdict)
	develop := provider.Respond
	provider.Respond = func(request backend.RunRequest) (backend.RunResult, error) {
		if request.Role == domain.RoleReviewer {
			reviewerPrompts = append(reviewerPrompts, request.Prompt)
		}
		return develop(request)
	}
	pipeline, _ := newAutomaticPipeline(t, repository, tracker, provider, []string{"exit 0"})

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(reviewerPrompts) == 0 {
		t.Fatalf("the change was never reviewed: %#v", outcome)
	}
	prompt := reviewerPrompts[0]
	for _, want := range []string{
		"This change is measured against base commit " + base,
		"## Referenced file: docs/guide.md (at base commit " + base + ")",
		"docs/guide.md as it stands at base commit " + base + ", which may differ from the file as it stands now.",
		"The setting is spelled `alpha` at the base.",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("reviewer evidence omitted %q:\n%s", want, prompt)
		}
	}
	if strings.Contains(prompt, "omega") {
		t.Fatalf("reviewer evidence carried the source as the checkout holds it now rather than at the base:\n%s", prompt)
	}
}
