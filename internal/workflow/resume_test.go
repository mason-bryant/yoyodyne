package workflow

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
)

// The child half of the kill test is told what to do through the environment,
// because it is this same test binary run again rather than a program with
// arguments of its own.
const (
	instanceRootEnvironment = "YOYODYNE_WORKFLOW_TEST_INSTANCES"
	journalEnvironment      = "YOYODYNE_WORKFLOW_TEST_JOURNAL"
	instanceEnvironment     = "YOYODYNE_WORKFLOW_TEST_INSTANCE"
	dieAtEnvironment        = "YOYODYNE_WORKFLOW_TEST_DIE_AT"
)

// TestAKilledProcessResumesAtItsLastCheckpoint is the criterion this milestone
// exists for, and it is tested at every boundary rather than at one: a real
// process runs the fixture, is killed while performing each state in turn, and
// what another process reads out of the store is where it stopped.
//
// The kill is a kill rather than a simulated one. A process that returned an
// error, or exited, would have unwound — deferred writes flushed, files closed —
// and the thing worth testing is exactly the process that got none of that.
func TestAKilledProcessResumesAtItsLastCheckpoint(t *testing.T) {
	if os.Getenv(instanceRootEnvironment) != "" {
		t.Skip("this process is the child half of the kill test")
	}
	t.Parallel()

	states := deliveredPath[:len(deliveredPath)-1]
	for boundary, state := range states {
		t.Run(state, func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			instances := filepath.Join(root, "state")
			logged := filepath.Join(root, "performed")
			executor := checkpointing(t, instances)
			if _, err := executor.Start("delivery"); err != nil {
				t.Fatalf("Start() error = %v", err)
			}
			performs := actionsAlong(t, executor.Graph, states)

			// A process of its own runs the instance and is killed performing this
			// state's action, having recorded that it performed it.
			child := exec.Command(os.Args[0], "-test.run=^TestRunningUntilThisProcessIsKilled$")
			child.Env = append(os.Environ(),
				instanceRootEnvironment+"="+instances,
				journalEnvironment+"="+logged,
				instanceEnvironment+"=delivery",
				dieAtEnvironment+"="+performs[boundary],
			)
			output, err := child.CombinedOutput()
			if err == nil {
				t.Fatalf("the child process ran to completion instead of being killed:\n%s", output)
			}

			killed, err := executor.Instances.LoadWorkflowInstance("delivery")
			if err != nil {
				t.Fatalf("Load() after the child was killed: %v", err)
			}
			if killed.State != state {
				t.Fatalf("the killed process left the instance in %q, want the state it was performing, %q", killed.State, state)
			}
			if want := boundary + 1; len(killed.Checkpoints) != want {
				t.Fatalf("the killed process left %d checkpoints, want the %d boundaries it had crossed", len(killed.Checkpoints), want)
			}
			if walked, want := killed.Path(), states[:boundary+1]; !slices.Equal(walked, want) {
				t.Fatalf("the killed process walked %v, want %v", walked, want)
			}
			if acted, want := performed(t, logged), performs[:boundary+1]; !slices.Equal(acted, want) {
				t.Fatalf("the killed process performed %v, want %v", acted, want)
			}

			// Resuming starts at exactly that boundary: the state it died in is
			// performed again, and nothing before it is.
			finished, err := executor.Run(context.Background(), "delivery", &journal{path: logged})
			if err != nil {
				t.Fatalf("Run() resuming a killed instance: %v", err)
			}
			if finished.State != "delivered" || !finished.Done() {
				t.Fatalf("the resumed instance ended in %q (done %t), want delivered", finished.State, finished.Done())
			}
			if walked := finished.Path(); !slices.Equal(walked, deliveredPath) {
				t.Fatalf("the resumed instance walked %v, want %v", walked, deliveredPath)
			}
			want := slices.Concat(performs[:boundary+1], performs[boundary:])
			if acted := performed(t, logged); !slices.Equal(acted, want) {
				t.Fatalf("the two processes performed %v, want %v", acted, want)
			}
		})
	}
}

// TestRunningUntilThisProcessIsKilled is the child half of the test above: it
// runs the instance the environment names and is killed performing the action
// the environment names. Nothing else runs it, and in an ordinary test run it
// skips.
func TestRunningUntilThisProcessIsKilled(t *testing.T) {
	instances := os.Getenv(instanceRootEnvironment)
	if instances == "" {
		t.Skip("the kill test runs this in a process of its own")
	}
	executor := checkpointing(t, instances)
	subject := &journal{path: os.Getenv(journalEnvironment), dieAt: os.Getenv(dieAtEnvironment)}
	instance, err := executor.Run(context.Background(), os.Getenv(instanceEnvironment), subject)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	t.Fatalf("this process was meant to be killed performing %q and reached %q", subject.dieAt, instance.State)
}
