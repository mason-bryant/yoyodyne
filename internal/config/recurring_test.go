package config

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
)

const projectWithSweep = `version: 1
extends: builtin:v1
product:
  id: example
  repository: .
recurring_tasks:
  development-manager-sweep:
    role: development-manager
    every: 1h
    enabled: true
    max_turns: 4
    prompt: |
      Sweep for unresolved issues and fix what your authority allows.
`

func TestRecurringTaskLoadsAsRoleCadenceAndPrompt(t *testing.T) {
	t.Parallel()

	cfg := loadProject(t, projectWithSweep, nil).Config
	task, err := cfg.RecurringTaskNamed("development-manager-sweep")
	if err != nil {
		t.Fatalf("RecurringTaskNamed() error = %v", err)
	}
	if task.Role != domain.RoleDevelopmentManager {
		t.Errorf("role = %q, want %q", task.Role, domain.RoleDevelopmentManager)
	}
	if task.Every.Duration() != time.Hour {
		t.Errorf("every = %s, want 1h", task.Every)
	}
	if !task.Enabled {
		t.Error("enabled = false, want the configured task switched on")
	}
	if task.Turns() != 4 {
		t.Errorf("turns = %d, want the configured 4", task.Turns())
	}
	if !strings.Contains(task.Prompt, "Sweep for unresolved issues") {
		t.Errorf("prompt = %q, want what the project wrote", task.Prompt)
	}
}

// A project that schedules nothing is the default and stays it: the capability
// is opted in to, and a configuration that never mentions it schedules nothing
// rather than inheriting somebody's idea of a sensible cadence.
func TestProjectWithoutRecurringTasksSchedulesNothing(t *testing.T) {
	t.Parallel()

	cfg := loadProject(t, minimalProjectConfig, nil).Config
	if len(cfg.RecurringTasks) != 0 {
		t.Errorf("recurring tasks = %v, want a project that schedules nothing", cfg.RecurringTasks)
	}
	if _, err := cfg.RecurringTaskNamed("anything"); err == nil {
		t.Error("RecurringTaskNamed() on an unscheduled project returned no error")
	}
}

// The invariant this schema is held to, asserted rather than described: no key
// here grants anything. The strict loader is what enforces it, so the check is
// that the authority-shaped names somebody would reach for first are refused as
// keys that do not exist.
func TestRecurringTaskCannotGrantAuthority(t *testing.T) {
	t.Parallel()

	for _, granting := range []string{
		"    capabilities:\n      - work-item-mutate\n",
		"    tools:\n      - bash\n",
		"    account: other\n",
		"    grants:\n      - direct-work\n",
	} {
		_, err := loadProjectError(t, strings.Replace(projectWithSweep, "    max_turns: 4\n", granting, 1), nil)
		if err == nil {
			t.Fatalf("a recurring task carrying %q loaded, and configuration must not be able to widen authority", strings.TrimSpace(granting))
		}
	}
}

func TestRecurringTaskRefusesWhatCouldNeverRun(t *testing.T) {
	t.Parallel()

	for name, replacement := range map[string]struct{ from, to string }{
		"a cadence below the floor":    {"    every: 1h\n", "    every: 30s\n"},
		"a role nobody fills":          {"    role: development-manager\n", "    role: nobody\n"},
		"more turns than the bound":    {"    max_turns: 4\n", "    max_turns: 99\n"},
		"a name that is no identifier": {"  development-manager-sweep:\n", "  Development Manager Sweep:\n"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := loadProjectError(t, strings.Replace(projectWithSweep, replacement.from, replacement.to, 1), nil)
			if err == nil {
				t.Fatalf("%s loaded, and it describes a task that could never run", name)
			}
		})
	}
}

// A prompt is the whole of what the harness has to say to the role, so a task
// with none wakes it for nothing. It is refused rather than fired with an empty
// message, which the role would answer by asking what was wanted.
func TestRecurringTaskRequiresSomethingToSay(t *testing.T) {
	t.Parallel()

	empty := strings.Replace(projectWithSweep,
		"    prompt: |\n      Sweep for unresolved issues and fix what your authority allows.\n",
		"    prompt: \"\"\n", 1)
	_, err := loadProjectError(t, empty, nil)
	if err == nil {
		t.Fatal("a recurring task with no prompt loaded")
	}
	if !strings.Contains(err.Error(), "prompt is required") {
		t.Errorf("error = %v, want it to name the missing prompt", err)
	}
}

// Turns default rather than being required, because a project that did not think
// about the bound still gets iteration: a heavy pass that says it has more to do
// is given another turn instead of being truncated at one.
func TestRecurringTaskIteratesByDefault(t *testing.T) {
	t.Parallel()

	if turns := (RecurringTask{}).Turns(); turns != DefaultRecurringTurns {
		t.Errorf("turns = %d, want the default %d", turns, DefaultRecurringTurns)
	}
	if DefaultRecurringTurns < 2 {
		t.Errorf("DefaultRecurringTurns = %d, and a default below 2 is no iteration at all", DefaultRecurringTurns)
	}
}

// The scheduled and the unscheduled task are named in one order whatever order a
// map hands them back in, because which task a pass fires must be decided by the
// schedule rather than by map iteration.
func TestRecurringTaskNamesAreOrdered(t *testing.T) {
	t.Parallel()

	cfg := Config{RecurringTasks: map[string]RecurringTask{
		"second-task": {},
		"first-task":  {},
		"third-task":  {},
	}}
	names := cfg.RecurringTaskNames()
	want := []string{"first-task", "second-task", "third-task"}
	if len(names) != len(want) {
		t.Fatalf("names = %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("names = %v, want %v", names, want)
		}
	}
}

// The generated file shows the shape, switched off. An operator given no sign of
// the schema has to be told it by somebody, which is the state the commented
// Slack and operators sections above it exist to end.
func TestScaffoldShowsTheRecurringSectionCommented(t *testing.T) {
	t.Parallel()

	resolved := loadScaffold(t, ScaffoldOptions{ProductID: "example", Repository: "."})
	rendered, err := os.ReadFile(resolved.Path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	for _, want := range []string{
		"# recurring_tasks:\n",
		"#   development-manager-sweep:\n",
		"#     role: development-manager\n",
		"#     every: 1h\n",
	} {
		if !strings.Contains(string(rendered), want) {
			t.Errorf("generated configuration does not show %q:\n%s", want, rendered)
		}
	}
	if len(resolved.Config.RecurringTasks) != 0 {
		t.Errorf("recurring tasks = %v, want a generated project that schedules nothing", resolved.Config.RecurringTasks)
	}
}

// The example is only worth showing if the gesture it asks for works, so it is
// uncommented here and loaded: an example that does not load is worse than none,
// because the operator who tried it has no reason to think the fault is the
// file's.
func TestScaffoldedRecurringExampleLoadsWhenUncommented(t *testing.T) {
	t.Parallel()

	resolved := loadScaffoldEdited(t, ScaffoldOptions{ProductID: "example", Repository: "."}, func(content string) string {
		return uncommentScaffoldBlock(t, content, "recurring_tasks:")
	})
	task, err := resolved.Config.RecurringTaskNamed("development-manager-sweep")
	if err != nil {
		t.Fatalf("RecurringTaskNamed() error = %v", err)
	}
	if task.Role != domain.RoleDevelopmentManager || task.Every.Duration() != time.Hour || !task.Enabled {
		t.Errorf("task = %+v, want the development manager woken hourly", task)
	}
	if !strings.Contains(task.Prompt, "root-cause work") {
		t.Errorf("prompt = %q, want the filing instruction the example carries", task.Prompt)
	}
}

func TestMinimumCadenceIsAboveTheAccident(t *testing.T) {
	t.Parallel()

	// The floor exists to catch "1m" written where "1h" was meant, so it has to
	// be above the plausible typo and below any cadence a sweep of a domain is
	// worth running at.
	if MinRecurringInterval <= time.Minute {
		t.Errorf("MinRecurringInterval = %s, which does not catch a minute typed for an hour", MinRecurringInterval)
	}
}
