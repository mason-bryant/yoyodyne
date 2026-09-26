package orchestrator

// A run asks for the model the work's own labels chose.
//
// The mapping itself is tested where it lives, in internal/config: which entry
// an item's labels match, what an unmapped item takes, and what the file refuses
// at load. None of that says the wiring holds. What makes spend follow the work
// is that the mapping is read once when a run starts, written onto the run, and
// then asked for by the invocation the provider actually receives — so this
// replays two items, one mapped and one not, and reads the model off the
// requests the fake provider was handed.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
)

// configurationGuideModelsHeading opens the section the documented mapping sits
// in. The helper below reads the first fenced YAML block under it as data, so
// renaming the heading or moving the block is a change to the test as much as to
// the guide.
const configurationGuideModelsHeading = "### A developer model chosen by the item's label"

// guideDeveloperModels reads the guide's docs-on-sonnet example and loads it as
// a project configuration, so what these replays drive is the block the operator
// is told to paste rather than a copy of it kept here. The block states only the
// execution section; the rest of a loadable project is wrapped around it.
func guideDeveloperModels(t *testing.T) []config.DeveloperModelRule {
	t.Helper()
	guide, err := os.ReadFile(configurationGuide)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", configurationGuide, err)
	}
	_, section, found := strings.Cut(string(guide), configurationGuideModelsHeading+"\n")
	if !found {
		t.Fatalf("%s has no %q section", configurationGuide, configurationGuideModelsHeading)
	}
	_, fenced, found := strings.Cut(section, "```yaml\n")
	if !found {
		t.Fatalf("the %q section of %s has no yaml block", configurationGuideModelsHeading, configurationGuide)
	}
	block, _, found := strings.Cut(fenced, "```")
	if !found {
		t.Fatalf("the yaml block under %q in %s is not closed", configurationGuideModelsHeading, configurationGuide)
	}
	if !strings.HasPrefix(block, "execution:\n") {
		t.Fatalf("the block under %q is not an execution section:\n%s", configurationGuideModelsHeading, block)
	}

	project := t.TempDir()
	directory := filepath.Join(project, config.DirectoryName)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	contents := "version: 1\nextends: builtin:v1\nproduct:\n  id: example\n  repository: .\n" + block
	if err := os.WriteFile(filepath.Join(directory, config.FileName), []byte(contents), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	cfg, err := config.Load(filepath.Join(directory, config.FileName))
	if err != nil {
		t.Fatalf("the guide's developer-model block does not load as a configuration: %v\n%s", err, block)
	}
	return cfg.Execution.DeveloperModels
}

func TestTheModelARunStartsOnIsChosenByTheItemsLabels(t *testing.T) {
	t.Parallel()

	mapping := guideDeveloperModels(t)
	if len(mapping) == 0 || mapping[0].Label != "docs" || mapping[0].Model != "sonnet" {
		t.Fatalf("the guide's mapping is %+v, want it to open with docs on sonnet", mapping)
	}

	for _, replay := range []struct {
		name   string
		labels []string
		want   string
		reason string
	}{
		{
			name:   "a mapped item takes the mapping's model",
			labels: []string{"docs", "reliability"},
			want:   "sonnet",
			reason: `the item's "docs" label is mapped to sonnet`,
		},
		{
			name:   "an unmapped item takes the developer's configured model",
			labels: []string{"reliability"},
			want:   testDeveloperModel,
			reason: "the item carries no label execution.developer_models names",
		},
	} {
		t.Run(replay.name, func(t *testing.T) {
			t.Parallel()

			repository := pipelineRepository(t)
			tracker := &orchestratortest.Tracker{Item: beads.WorkItem{
				ID: "yoyodyne-task", Title: "Work", Status: "open", Labels: replay.labels,
			}}
			provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
				return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
			}, repairVerdict, approveVerdict)
			pipeline, store := newAutomaticPipeline(t, repository, tracker, provider, []string{"exit 0"})
			pipeline.Config.Execution.DeveloperModels = mapping

			outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
			if err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			state, err := store.Load(outcome.RunID)
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			if state.DeveloperModel != replay.want {
				t.Fatalf("recorded developer model = %q, want %q", state.DeveloperModel, replay.want)
			}
			if !strings.Contains(state.DeveloperModelReason, replay.reason) {
				t.Fatalf("recorded reason = %q, want it to say %q", state.DeveloperModelReason, replay.reason)
			}
			// And the selector the run reports is the one it asked for, which is
			// what every spend surface reads the model per run from.
			if state.ProviderModel != replay.want || outcome.ProviderModel != replay.want {
				t.Fatalf("reported selector = %q/%q, want %q", state.ProviderModel, outcome.ProviderModel, replay.want)
			}

			// The first verdict sends the change back, so this run made two
			// developer attempts. Both matter: a repair is where a run that
			// resolved the mapping again per invocation would be visible.
			attempts := provider.RequestsForRole(domain.RoleDeveloper)
			if len(attempts) < 2 {
				t.Fatalf("the run made %d developer attempt(s), want the first and its repair", len(attempts))
			}
			for index, request := range attempts {
				if request.Model != replay.want {
					t.Fatalf("developer attempt %d asked for %q, want %q", index, request.Model, replay.want)
				}
			}
			// The reviewer's model is not the mapping's to move: its posture is a
			// safety property, and the mapping has no key that could name it.
			reviews := provider.RequestsForRole(domain.RoleReviewer)
			if len(reviews) == 0 {
				t.Fatal("the run obtained no verdict")
			}
			for index, request := range reviews {
				if request.Model != testReviewerModel {
					t.Fatalf("verdict %d was asked of %q, want the configured reviewer's %q", index, request.Model, testReviewerModel)
				}
			}
		})
	}
}

// A project that configured no mapping records nothing about one and runs on the
// developer's configured model, which is what every run did before the mapping
// existed.
func TestARunUnderNoMappingRecordsNoModelChoice(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Work", Status: "open", Labels: []string{"docs"}}}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	pipeline, store := newAutomaticPipeline(t, repository, tracker, provider, []string{"exit 0"})

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if state.DeveloperModel != "" || state.DeveloperModelReason != "" {
		t.Fatalf("recorded %q for %q, want nothing recorded where no mapping was read",
			state.DeveloperModel, state.DeveloperModelReason)
	}
	for _, request := range provider.RequestsForRole(domain.RoleDeveloper) {
		if request.Model != testDeveloperModel {
			t.Fatalf("developer attempt asked for %q, want the configured %q", request.Model, testDeveloperModel)
		}
	}
}
