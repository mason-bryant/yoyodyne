package orchestrator

// A project's own delivery definition: that a run executes the file the project
// wrote, that a project which wrote none executes the one this build ships, and
// that a file which is wrong stops the run before it has claimed anything.
//
// The runs are real ones, driven through the same fixture the baseline scenarios
// use, and what is read back is the instance's own pin. That is deliberate: the
// question is which file a run actually executed, and the digest recorded on the
// instance is the only answer that is not this test believing itself.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/separation"
)

// projectConfigPath is a project with a configuration directory and nothing in
// it, and the path a pipeline is given to find that directory by.
//
// The configuration file is written because that is the layout a project has,
// not because anything here opens it: what reads the path is the search for the
// project's own workflow definitions, which resolves the directory the
// configuration lives in and looks under it.
func projectConfigPath(t *testing.T) string {
	t.Helper()
	directory := filepath.Join(t.TempDir(), ".yoyodyne")
	if err := os.MkdirAll(directory, 0o750); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	configPath := filepath.Join(directory, "config.yaml")
	if err := os.WriteFile(configPath, []byte("version: 1\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	return configPath
}

// writeProjectDefinition puts one definition where this project keeps its own,
// and answers with the path it was written to — which is what a refusal has to
// name for somebody to know which file to open.
func writeProjectDefinition(t *testing.T, configPath, id, definition string) string {
	t.Helper()
	path := filepath.Join(filepath.Dir(configPath), filepath.FromSlash(ProjectDefinitionPath(id)))
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(path, []byte(definition), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	return path
}

// projectsOwnDelivery is the built-in definition as a project's own copy of it:
// the same sequence with its summary rewritten.
//
// That is the smallest edit that changes the content digest without changing a
// transition, which is exactly what these tests need — what they measure is
// which file was read, so the copy deliberately sends the run nowhere the
// built-in would not have.
func projectsOwnDelivery() string {
	return strings.Replace(string(deliveryDefinition),
		"summary: claim a work item", "summary: this project's own delivery loop", 1)
}

// TestAProjectsOwnDeliveryDefinitionIsWhatANewRunExecutes is the project-owned
// path itself: a copy under the project's configuration directory is what a new
// run compiles, pins itself to, and is stepped through.
func TestAProjectsOwnDeliveryDefinitionIsWhatANewRunExecutes(t *testing.T) {
	t.Parallel()

	fixture := newBaselineFixture(t, baselineItem())
	configPath := projectConfigPath(t)
	writeProjectDefinition(t, configPath, DeliveryWorkflowID, projectsOwnDelivery())

	provider := roleBackend(baselineImplements, approveVerdict)
	pipeline := fixture.automatic(t, provider, []string{"test -f feature.txt"})
	pipeline.ConfigPath = configPath
	if outcome := fixture.invoke(t, "run", pipeline); outcome.Status != runstate.StatusSucceeded {
		t.Fatalf("the run ended in %s (%s); a project's own definition of the same sequence delivers the work", outcome.Status, outcome.Failure)
	}

	state, instance := observedRun(t, fixture.store)
	if state.WorkflowDivergence != "" {
		t.Errorf("the run recorded the divergence %q; the project's copy expresses the same path the built-in does", state.WorkflowDivergence)
	}
	project, err := observedDeliveryGraph(DeliveryWorkflowID, configPath)
	if err != nil {
		t.Fatalf("observedDeliveryGraph(%s, %s) error = %v", DeliveryWorkflowID, configPath, err)
	}
	builtin, err := observedDeliveryGraph(DeliveryWorkflowID, "")
	if err != nil {
		t.Fatalf("observedDeliveryGraph(%s, built in) error = %v", DeliveryWorkflowID, err)
	}
	if project.Digest() == builtin.Digest() {
		t.Fatalf("the project's copy digests identically to the built-in; this measures nothing")
	}
	if instance.Digest != project.Digest() {
		t.Errorf("the run pinned %s; the project's own definition is %s and the built-in is %s",
			instance.Digest, project.Digest(), builtin.Digest())
	}
	if !instance.Terminal {
		t.Errorf("the instance stands in %q rather than a terminal", instance.State)
	}
}

// TestAProjectThatKeepsNoDefinitionExecutesTheBuiltIn is the inheritance: a
// project with a configuration and no workflow file of its own runs the
// definition this build ships, and nothing about it has to be configured.
func TestAProjectThatKeepsNoDefinitionExecutesTheBuiltIn(t *testing.T) {
	t.Parallel()

	fixture := newBaselineFixture(t, baselineItem())
	configPath := projectConfigPath(t)

	provider := roleBackend(baselineImplements, approveVerdict)
	pipeline := fixture.automatic(t, provider, []string{"test -f feature.txt"})
	pipeline.ConfigPath = configPath
	if outcome := fixture.invoke(t, "run", pipeline); outcome.Status != runstate.StatusSucceeded {
		t.Fatalf("the run ended in %s (%s)", outcome.Status, outcome.Failure)
	}

	state, instance := observedRun(t, fixture.store)
	if state.WorkflowDivergence != "" {
		t.Errorf("the run recorded the divergence %q", state.WorkflowDivergence)
	}
	builtin, err := observedDeliveryGraph(DeliveryWorkflowID, "")
	if err != nil {
		t.Fatalf("observedDeliveryGraph(%s, built in) error = %v", DeliveryWorkflowID, err)
	}
	if instance.Digest != builtin.Digest() {
		t.Errorf("the run pinned %s and the built-in definition is %s; the project keeps no copy of its own", instance.Digest, builtin.Digest())
	}
}

// TestADeliveryDefinitionAProjectGotWrongStopsTheRunBeforeItClaims is the
// refusal, and where it lands.
//
// Each defect is one a project can actually make by editing its copy, and every
// one of them is refused whole: the run fails naming the file and what is wrong
// with it, the work item is never claimed, and no instance is recorded — so
// nothing quietly executed the built-in in place of the file the project wrote.
func TestADeliveryDefinitionAProjectGotWrongStopsTheRunBeforeItClaims(t *testing.T) {
	t.Parallel()

	builtin := string(deliveryDefinition)
	for _, broken := range []struct {
		name       string
		definition string
		defect     string
	}{
		{
			name:       "selects an action nothing registers",
			definition: strings.Replace(builtin, "action: work-item.claim", "action: work-item.smuggle", 1),
			defect:     "work-item.smuggle",
		},
		{
			name:       "sends an outcome to a destination that does not exist",
			definition: strings.Replace(builtin, "claimed: develop", "claimed: improvise", 1),
			defect:     "improvise",
		},
		{
			name:       "routes an outcome the step never produces",
			definition: strings.Replace(builtin, "unavailable: abandoned", "reconsidered: abandoned", 1),
			defect:     "reconsidered",
		},
		{
			name:       "declares another workflow entirely",
			definition: strings.Replace(builtin, "id: delivery\n", "id: delivery-by-another-name\n", 1),
			defect:     "delivery-by-another-name",
		},
		{
			name:       "carries a key the schema does not describe",
			definition: strings.Replace(builtin, "initial: claim", "initial: claim\nretries: 3", 1),
			defect:     "retries",
		},
	} {
		t.Run(broken.name, func(t *testing.T) {
			t.Parallel()

			fixture := newBaselineFixture(t, baselineItem())
			configPath := projectConfigPath(t)
			path := writeProjectDefinition(t, configPath, DeliveryWorkflowID, broken.definition)

			provider := roleBackend(baselineImplements, approveVerdict)
			pipeline := fixture.automatic(t, provider, []string{"test -f feature.txt"})
			pipeline.ConfigPath = configPath
			outcome, err := pipeline.Run(context.Background(), fixture.tracker.item.ID)
			if err == nil {
				t.Fatalf("the run went ahead on a definition that %s", broken.name)
			}
			if outcome.Status != runstate.StatusFailed {
				t.Errorf("the run ended in %s; a definition that cannot be compiled fails the run", outcome.Status)
			}
			if !strings.Contains(err.Error(), path) {
				t.Errorf("the refusal is %q and does not name the file it read", err)
			}
			if !strings.Contains(err.Error(), broken.defect) {
				t.Errorf("the refusal is %q and does not name %q, which is what is wrong with the file", err, broken.defect)
			}
			if fixture.tracker.claimed {
				t.Errorf("the work item was claimed by a run whose own definition was refused")
			}
			if _, err := fixture.store.LoadWorkflowInstance(deliveryInstanceID(pipelineRunID)); err == nil {
				t.Errorf("an instance was recorded for a run whose definition was refused; something fell back to a sequence nobody chose")
			}
		})
	}
}

// deliveryWithoutAGate is a project definition that promotes a change without
// running the checks over it or buying a verdict on it. It is well formed,
// selects nothing but registered actions, and routes only outcomes those actions
// produce — everything wrong with it is the sequence itself.
const deliveryWithoutAGate = `schema: 1
id: delivery
summary: a project trying to promote what nothing judged
initial: claim
states:
  claim:
    action: work-item.claim
    on:
      claimed: develop
      unavailable: abandoned
  develop:
    action: candidate.develop
    on:
      produced: integrate
      stopped: abandoned
  integrate:
    action: candidate.integrate
    on:
      integrated: complete
      conflicted: abandoned
      contended: abandoned
      superseded: abandoned
  complete:
    action: run.complete
    on:
      completed: clean-up
  clean-up:
    action: run.clean-up
    on:
      cleaned: delivered
      partial: delivered
terminals:
  delivered:
    summary: the change is on the target branch
  abandoned:
    summary: the run stopped with nothing promoted
`

// TestAProjectDefinitionCannotPromoteWithoutTheGate is the safety half of
// project ownership: configuration selects the sequence and cannot make a
// guarantee optional.
//
// A definition that can reach the promotion without a state that runs the checks
// and one that buys an independent verdict is refused at compile, whatever order
// it puts its states in — so a project cannot write its way past the gate, and
// the run stops before it has claimed anything rather than promoting something
// nobody judged.
func TestAProjectDefinitionCannotPromoteWithoutTheGate(t *testing.T) {
	t.Parallel()

	fixture := newBaselineFixture(t, baselineItem())
	configPath := projectConfigPath(t)
	path := writeProjectDefinition(t, configPath, DeliveryWorkflowID, deliveryWithoutAGate)

	provider := roleBackend(baselineImplements, approveVerdict)
	pipeline := fixture.automatic(t, provider, []string{"test -f feature.txt"})
	pipeline.ConfigPath = configPath
	outcome, err := pipeline.Run(context.Background(), fixture.tracker.item.ID)
	if err == nil {
		t.Fatalf("the run went ahead on a definition that promotes what nothing judged")
	}
	if outcome.Status != runstate.StatusFailed {
		t.Errorf("the run ended in %s", outcome.Status)
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("the refusal is %q and does not name the file it read", err)
	}
	if !strings.Contains(err.Error(), separation.IntegrationFollowsEvidence) {
		t.Errorf("the refusal is %q and does not name the policy that refused it", err)
	}
	if fixture.tracker.claimed {
		t.Errorf("the work item was claimed by a run whose definition was refused")
	}
}

// TestARunInFlightRecordsADefinitionItsProjectBroke is the other end of the same
// strictness, and it is deliberately not a refusal.
//
// A run already under way is never failed for its project's file: the work is in
// flight, and what is broken is the watching. What must not happen is silence —
// the soak counts the runs carrying no divergence — so the run records why it
// stopped being observed, naming the file, and finishes the work exactly as it
// would have.
func TestARunInFlightRecordsADefinitionItsProjectBroke(t *testing.T) {
	t.Parallel()

	fixture := newBaselineFixture(t, baselineItem())
	configPath := projectConfigPath(t)
	path := writeProjectDefinition(t, configPath, DeliveryWorkflowID, projectsOwnDelivery())

	resetsAt := baseTime.Add(2 * time.Hour)
	limit := &backend.UsageLimit{Kind: "five_hour", ResetsAt: resetsAt}

	// The first invocation is refused for want of capacity and the wait outlasts
	// this process, so it exits with the run in flight and its instance standing
	// on the project's own definition.
	refused := usageLimitBackend(1, limit, approveVerdict)
	paused := waiting(fixture.automatic(t, refused, []string{"test -f feature.txt"}),
		&pausingClock{now: baseTime}, 6*time.Hour, time.Minute)
	paused.ConfigPath = configPath
	fixture.invoke(t, "paused invocation", paused)

	inFlight, err := fixture.store.Load(pipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if inFlight.WorkflowInstanceID == "" {
		t.Fatalf("the run records no instance; there is nothing in flight to stop observing")
	}

	// The project edits its definition into something that no longer compiles
	// while the run is paused.
	writeProjectDefinition(t, configPath, DeliveryWorkflowID,
		strings.Replace(projectsOwnDelivery(), "action: work-item.claim", "action: work-item.smuggle", 1))

	served := usageLimitBackend(0, limit, approveVerdict)
	resuming := waiting(fixture.automatic(t, served, []string{"test -f feature.txt"}),
		&pausingClock{now: resetsAt.Add(time.Minute)}, 6*time.Hour, time.Minute)
	resuming.ConfigPath = configPath
	if outcome := fixture.invoke(t, "resumed invocation", resuming); outcome.Status != runstate.StatusSucceeded {
		t.Fatalf("the resumed run ended in %s (%s); a definition that stopped compiling is never a reason the work does not finish", outcome.Status, outcome.Failure)
	}

	finished, err := fixture.store.Load(pipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if finished.WorkflowDivergence == "" {
		t.Fatalf("the run finished with its instance standing where the first process left it and recorded no divergence")
	}
	if !strings.Contains(finished.WorkflowDivergence, path) {
		t.Errorf("the divergence is %q and does not name the file that stopped compiling", finished.WorkflowDivergence)
	}
}
