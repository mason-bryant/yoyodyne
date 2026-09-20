package cli

// The verbs that record a person's decision refuse a process the harness
// launched for a role. What is asserted is each verb in-process with the
// marker set, and then the case the marker is for: the binary launched from
// exactly the environment a backend builds for a developer, running the verb
// from a shell, and being told a person does this.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

func TestTheVerbsThatRecordAPersonsDecisionRefuseAnAgentsProcess(t *testing.T) {
	// Not parallel: the state root and the marker are this process's environment.
	stateRoot := t.TempDir()
	t.Setenv("YOYODYNE_STATE_HOME", stateRoot)
	t.Setenv(execution.AgentRoleVariable, string(domain.RoleDeveloper))
	configPath := writeConfig(t, validConfig)
	project := filepath.Dir(configPath)
	writeArtifact(t, project, "docs/product/brief.md", artifactDocument("brief", "brief", "Product brief", nil))
	goals := filepath.Join(project, "docs", "product", "goals", "v1-goals.md")
	writeArtifact(t, project, "docs/product/goals/v1-goals.md", artifactDocument("v1-goals", "goals", "V1 goals", []string{"brief"}))
	before, err := os.ReadFile(goals)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	holds, err := runstate.NewOperatorHoldStore(stateRoot)
	if err != nil {
		t.Fatalf("NewOperatorHoldStore() error = %v", err)
	}
	intake, err := runstate.NewIntakeHoldStore(stateRoot, "yoyodyne")
	if err != nil {
		t.Fatalf("NewIntakeHoldStore() error = %v", err)
	}
	if _, err := intake.Hold(runstate.IntakeHolderOperator, "held by a person", time.Now()); err != nil {
		t.Fatalf("Hold() error = %v", err)
	}

	for _, verb := range []struct {
		args []string
		act  string
	}{
		{[]string{"pause", "--config", configPath}, "a person pauses the harness"},
		{[]string{"resume", "--config", configPath}, "a person lifts a pause or releases a run's wait"},
		{[]string{"resume", "--config", configPath, "yoyodyne-ifd.1"}, "a person lifts a pause or releases a run's wait"},
		{[]string{"release", "--config", configPath}, "a person releases intake"},
		{[]string{"artifact", "approve", "--config", configPath, "v1-goals", "--reason", "approved by a shell"}, "a person approves an artifact"},
	} {
		stdout, stderr, code := runCLI(t, verb.args...)
		if code != 1 {
			t.Fatalf("%v code = %d, stdout = %q, stderr = %q, want the refusal", verb.args, code, stdout, stderr)
		}
		// The sentence says whose act it is and which process this is, so the
		// agent reading it back knows the verb is somebody else's rather than broken.
		for _, want := range []string{"refused from a process the harness launched for the developer", verb.act} {
			if !strings.Contains(stderr, want) {
				t.Fatalf("%v stderr = %q, want it to say %q", verb.args, stderr, want)
			}
		}
		// And the same refusal in the machine-readable shape, for a script an
		// agent wrote.
		stdout, _, code = runCLI(t, append(verb.args, "--json")...)
		var payload struct {
			Error string `json:"error"`
		}
		if err := json.Unmarshal([]byte(stdout), &payload); err != nil || code != 1 {
			t.Fatalf("%v --json code = %d, stdout = %q, err = %v, want the refusal as JSON", verb.args, code, stdout, err)
		}
		if !strings.Contains(payload.Error, verb.act) {
			t.Fatalf("%v --json error = %q, want it to say %q", verb.args, payload.Error, verb.act)
		}
	}

	// Nothing was recorded by any of them: no pause placed, the intake hold a
	// person placed still standing, and the goals document untouched.
	if _, held, err := holds.Held(); err != nil || held {
		t.Fatalf("Held() after the refused pause = %t, %v, want no hold", held, err)
	}
	if _, held, err := intake.Held(); err != nil || !held {
		t.Fatalf("intake Held() after the refused release = %t, %v, want the hold still standing", held, err)
	}
	if after, err := os.ReadFile(goals); err != nil || !bytes.Equal(before, after) {
		t.Fatalf("the goals document changed under a refused approval (err = %v):\n%s", err, after)
	}

	// Clearing the marker is what a person at a shell an agent opened does,
	// and it is enough: the verb is theirs again.
	t.Setenv(execution.AgentRoleVariable, "")
	if stdout, stderr, code := runCLI(t, "release", "--config", configPath); code != 0 || !strings.Contains(stdout, "released the hold on intake") {
		t.Fatalf("release with the marker cleared code = %d, stdout = %q, stderr = %q, want the hold released", code, stdout, stderr)
	}
}

// The case the marker exists for. The binary is launched from the environment
// a backend builds for a developer -- the allowlist, with the role on top --
// through a shell, as an agent would run it, and asserted to be refused. What
// this pins is that the marker survives the allowlist and the shell in between,
// which is the whole of why it is under the harness's own prefix.
func TestTheVerbLaunchedFromADevelopersEnvironmentIsRefused(t *testing.T) {
	// Not parallel: the state root and the helper switch are this process's
	// environment, which the launched environment is built from.
	stateRoot := t.TempDir()
	t.Setenv("YOYODYNE_STATE_HOME", stateRoot)
	t.Setenv(productHelperVariable, "1")
	configPath := writeConfig(t, validConfig)
	holds, err := runstate.NewOperatorHoldStore(stateRoot)
	if err != nil {
		t.Fatalf("NewOperatorHoldStore() error = %v", err)
	}
	program, err := os.Executable()
	if err != nil {
		t.Fatalf("Executable() error = %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	// The shell is what an agent has, and `yoyo pause` typed into it is the
	// binary inheriting the shell's environment, which inherited the agent's.
	shell := exec.CommandContext(ctx, "/bin/sh", "-c", `"$0" pause --config "$1"`, program, configPath)
	shell.Env = execution.WithAgentRole(execution.ExplicitEnvironment(nil), domain.RoleDeveloper)
	var stdout, stderr bytes.Buffer
	shell.Stdout, shell.Stderr = &stdout, &stderr
	err = shell.Run()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 1 {
		t.Fatalf("pause from a developer's environment err = %v, stdout = %q, stderr = %q, want exit 1", err, stdout.String(), stderr.String())
	}
	for _, want := range []string{"refused from a process the harness launched for the developer", "a person pauses the harness"} {
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("pause stderr = %q, want it to say %q", stderr.String(), want)
		}
	}
	if _, held, err := holds.Held(); err != nil || held {
		t.Fatalf("Held() after the refused pause = %t, %v, want no hold placed", held, err)
	}

	// The same launch with no role on it is a person's, and places the hold:
	// the refusal is the marker's and not the launch's.
	person := exec.CommandContext(ctx, "/bin/sh", "-c", `"$0" pause --config "$1"`, program, configPath)
	person.Env = execution.ExplicitEnvironment(nil)
	stdout.Reset()
	stderr.Reset()
	person.Stdout, person.Stderr = &stdout, &stderr
	if err := person.Run(); err != nil {
		t.Fatalf("pause from a person's environment err = %v, stdout = %q, stderr = %q", err, stdout.String(), stderr.String())
	}
	if _, held, err := holds.Held(); err != nil || !held {
		t.Fatalf("Held() after a person's pause = %t, %v, want the hold placed", held, err)
	}
}
