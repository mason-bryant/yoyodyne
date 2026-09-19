package slack

// Starting a sink that outlives whatever started it.
//
// A maintenance pass is a short process: it runs, it acts, it exits. The sink
// it starts has to still be reporting an hour later, so it is started in a
// session of its own rather than as a child that shares the pass's terminal,
// process group, and lifetime. Without that, whatever stops the pass — a
// launchd job being unloaded, a terminal closing, a group signal — takes
// reporting down with it, hours after anybody was watching.
//
// The product's supervisor starts its other children the same way, through
// this launcher: the scheduler, and the supervisor itself when `yoyo start`
// detaches it. Each is a process that has to survive whatever started it, and
// one launcher is one set of rules about how.

import (
	"errors"
	"fmt"
	"io/fs"
	"os/exec"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/repowrite"
)

// What the sink's log and the directories above it are created as. A log of
// what the harness is doing is state rather than a checked-in document, so it
// is the owner's to read and nobody else's.
const (
	logPermissions   fs.FileMode = 0o600
	logDirectoryMode fs.FileMode = 0o700
)

// DetachedLauncher starts the process described as its own session and returns
// without waiting for it.
type DetachedLauncher struct{}

func (DetachedLauncher) Launch(spec Launch) (int, error) {
	if strings.TrimSpace(spec.Program) == "" {
		return 0, errors.New("starting a process needs the binary to start")
	}
	if strings.TrimSpace(spec.Log) == "" {
		return 0, errors.New("starting a process needs somewhere for it to say what it is doing")
	}
	// The log is opened through the confined writer rather than by name. A path
	// string proves nothing about where bytes land: one symlink along it and the
	// sink's whole output — every line of what the harness is doing — is appended
	// to a file outside the root this was told to stay in. The root is declared
	// here and the containment is decided against the filesystem below it.
	root, err := repowrite.NewRoot(spec.LogRoot)
	if err != nil {
		return 0, fmt.Errorf("the process log has to stay inside %s: %w", spec.LogRoot, err)
	}
	// Appended to rather than replaced: the log of the sink that stopped is how
	// anybody finds out why it stopped, and a pass that starts a new one every
	// few minutes would otherwise erase that before it was read.
	log, err := root.OpenAppend(spec.Log, logPermissions, logDirectoryMode)
	if err != nil {
		return 0, fmt.Errorf("open the process log: %w", err)
	}
	defer log.Close()

	command := exec.Command(spec.Program, spec.Args...)
	command.Dir = spec.Dir
	// The environment the caller constructed, and nothing this process happens
	// to be holding: the tokens in it are for this product's sink alone. The
	// Git maintenance fence goes on top of it, because the sink outlives what
	// started it and everything it spawns inherits this environment — a sink
	// exempt from the fence is a long-lived source of exactly the prune the
	// fence exists to stop.
	command.Env = execution.WithGitMaintenanceFence(spec.Env)
	command.Stdin = nil
	command.Stdout = log
	command.Stderr = log
	detachProcess(command)
	if err := command.Start(); err != nil {
		return 0, fmt.Errorf("start %s: %w", spec.Program, err)
	}
	pid := command.Process.Pid
	// Released rather than waited for: nothing here is going to reap it, and a
	// process this one never waits on is one the operating system reparents when
	// this one exits.
	if err := command.Process.Release(); err != nil {
		return pid, fmt.Errorf("release the started process: %w", err)
	}
	return pid, nil
}
