package launchd

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// fakeLaunchctl answers launchctl as a machine would: the job is loaded when
// the test says so, bootstrap loads it, bootout unloads it.
type fakeLaunchctl struct {
	loaded   bool
	commands []string
}

func (f *fakeLaunchctl) Run(_ context.Context, command execution.Command, _ execution.OutputObserver) (execution.ProcessResult, error) {
	joined := command.Name + " " + strings.Join(command.Args, " ")
	f.commands = append(f.commands, joined)
	switch {
	case strings.HasPrefix(joined, "launchctl print"):
		if !f.loaded {
			return execution.ProcessResult{Status: execution.ProcessFailed, ExitCode: 113, Stderr: "Could not find service \"com.yoyodyne.calc\" in domain for uid: 501"}, nil
		}
	case strings.HasPrefix(joined, "launchctl bootstrap"):
		f.loaded = true
	case strings.HasPrefix(joined, "launchctl bootout"):
		f.loaded = false
	}
	return execution.ProcessResult{Status: execution.ProcessSucceeded}, nil
}

// The plist runs the supervisor verb from the installing binary, starts with
// the machine, is restarted by launchd only on a failure, abandons its process
// group so the children survive it, and carries the operator's PATH and
// nothing secret. Every value is escaped, because a path with an ampersand in
// it is a path.
func TestRenderWritesTheSupervisorJob(t *testing.T) {
	t.Parallel()

	rendered := string(Render(Spec{
		Label:            "com.yoyodyne.calc",
		Program:          "/Users/mason/github/calc & co/bin/yoyo",
		Args:             []string{"start", "--foreground", "--config", "/Users/mason/github/calc & co/.yoyodyne/config.yaml"},
		WorkingDirectory: "/Users/mason/github/calc & co",
		Log:              "/state/products/calc/supervisor/supervisor.log",
		Environment:      Environment([]string{"PATH=/opt/homebrew/bin:/usr/bin", "SLACK_BOT_TOKEN=xoxb-1", "HOME=/Users/mason", "YOYODYNE_STATE_HOME=/state"}),
	}))
	for _, want := range []string{
		`<?xml version="1.0" encoding="UTF-8"?>`,
		"<key>Label</key>\n  <string>com.yoyodyne.calc</string>",
		"<key>ProgramArguments</key>\n  <array>\n    <string>/Users/mason/github/calc &amp; co/bin/yoyo</string>\n    <string>start</string>\n    <string>--foreground</string>\n    <string>--config</string>\n    <string>/Users/mason/github/calc &amp; co/.yoyodyne/config.yaml</string>\n  </array>",
		"<key>WorkingDirectory</key>\n  <string>/Users/mason/github/calc &amp; co</string>",
		"<key>RunAtLoad</key>\n  <true/>",
		"<key>KeepAlive</key>\n  <dict>\n    <key>SuccessfulExit</key>\n    <false/>\n  </dict>",
		"<key>AbandonProcessGroup</key>\n  <true/>",
		"<key>StandardOutPath</key>\n  <string>/state/products/calc/supervisor/supervisor.log</string>",
		"<key>StandardErrorPath</key>\n  <string>/state/products/calc/supervisor/supervisor.log</string>",
		"<key>EnvironmentVariables</key>\n  <dict>\n    <key>PATH</key>\n    <string>/opt/homebrew/bin:/usr/bin</string>\n    <key>YOYODYNE_STATE_HOME</key>\n    <string>/state</string>\n  </dict>",
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("rendered plist lacks %q:\n%s", want, rendered)
		}
	}
	// YOYODYNE_STATE_HOME is carried; HOME is launchd's to set.
	for _, absent := range []string{"SLACK_BOT_TOKEN", "xoxb", "<key>HOME</key>"} {
		if strings.Contains(rendered, absent) {
			t.Errorf("rendered plist carries %q, which the job is not given:\n%s", absent, rendered)
		}
	}
}

// The agent is named for the product under the user's launch agents, written
// there, read back, told apart from another checkout's by the configuration
// it runs, and driven through launchctl in the user's domain.
func TestTheAgentIsInstalledReadAndDrivenThroughLaunchctl(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	launchctl := &fakeLaunchctl{}
	agent := AgentFor("calc", Controller{Runner: launchctl, UserHomeDir: func() (string, error) { return home, nil }, Getuid: func() int { return 501 }})
	if agent.Label != "com.yoyodyne.calc" || agent.Path != filepath.Join(home, "Library", "LaunchAgents", "com.yoyodyne.calc.plist") {
		t.Fatalf("agent = %+v, want it named for the product under the user's launch agents", agent)
	}
	if _, found, err := agent.Installed(); err != nil || found {
		t.Fatalf("Installed() = %t, %v before anything was written", found, err)
	}
	if loaded, err := agent.Loaded(context.Background()); err != nil || loaded {
		t.Fatalf("Loaded() = %t, %v before anything was loaded", loaded, err)
	}

	content := Render(Spec{Label: agent.Label, Program: "/opt/yoyo", Args: []string{"start", "--foreground", "--config", "/p/.yoyodyne/config.yaml"}})
	if err := agent.Install(content); err != nil {
		t.Fatalf("Install() error = %v", err)
	}
	read, found, err := agent.Installed()
	if err != nil || !found || string(read) != string(content) {
		t.Fatalf("Installed() = %q, %t, %v after Install(), want what was written", read, found, err)
	}
	if runs, err := agent.Runs("/p/.yoyodyne/config.yaml"); err != nil || !runs {
		t.Errorf("Runs(this configuration) = %t, %v, want true", runs, err)
	}
	if runs, err := agent.Runs("/elsewhere/.yoyodyne/config.yaml"); err != nil || runs {
		t.Errorf("Runs(another checkout's configuration) = %t, %v, want false", runs, err)
	}

	if err := agent.Bootstrap(context.Background()); err != nil {
		t.Fatalf("Bootstrap() error = %v", err)
	}
	if loaded, err := agent.Loaded(context.Background()); err != nil || !loaded {
		t.Fatalf("Loaded() = %t, %v after Bootstrap()", loaded, err)
	}
	if err := agent.Kickstart(context.Background()); err != nil {
		t.Fatalf("Kickstart() error = %v", err)
	}
	if err := agent.Bootout(context.Background()); err != nil {
		t.Fatalf("Bootout() error = %v", err)
	}
	want := []string{
		"launchctl print gui/501/com.yoyodyne.calc",
		"launchctl bootstrap gui/501 " + agent.Path,
		"launchctl print gui/501/com.yoyodyne.calc",
		"launchctl kickstart gui/501/com.yoyodyne.calc",
		"launchctl bootout gui/501/com.yoyodyne.calc",
	}
	if strings.Join(launchctl.commands, "\n") != strings.Join(want, "\n") {
		t.Errorf("launchctl was run as:\n%s\nwant:\n%s", strings.Join(launchctl.commands, "\n"), strings.Join(want, "\n"))
	}
	if !strings.Contains(agent.UninstallCommand(), "launchctl bootout gui/$(id -u)/com.yoyodyne.calc; rm ") || !strings.Contains(agent.BootstrapCommand(), "launchctl bootstrap gui/$(id -u) ") {
		t.Errorf("commands = %q and %q", agent.UninstallCommand(), agent.BootstrapCommand())
	}

	// A refusal from launchctl is reported with what it said.
	launchctl.loaded = true
	refusing := &fakeLaunchctl{}
	other := AgentFor("calc", Controller{Runner: refusingRunner{refusing}, UserHomeDir: func() (string, error) { return home, nil }})
	if err := other.Bootstrap(context.Background()); err == nil || !strings.Contains(err.Error(), "Bootstrap failed") {
		t.Errorf("Bootstrap() against a refusing launchctl = %v, want its words", err)
	}
	if _, err := os.Stat(agent.Path); err != nil {
		t.Errorf("the plist was removed by something other than the operator: %v", err)
	}
}

type refusingRunner struct{ *fakeLaunchctl }

func (refusingRunner) Run(context.Context, execution.Command, execution.OutputObserver) (execution.ProcessResult, error) {
	return execution.ProcessResult{Status: execution.ProcessFailed, ExitCode: 5, Stderr: "Bootstrap failed: 5: Input/output error"}, nil
}
