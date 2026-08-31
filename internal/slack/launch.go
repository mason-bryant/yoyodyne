package slack

// Starting a sink that outlives whatever started it.
//
// A maintenance pass is a short process: it runs, it acts, it exits. The sink
// it starts has to still be reporting an hour later, so it is started in a
// session of its own rather than as a child that shares the pass's terminal,
// process group, and lifetime. Without that, whatever stops the pass — a
// launchd job being unloaded, a terminal closing, a group signal — takes
// reporting down with it, hours after anybody was watching.

import (
	"errors"
	"fmt"
	"io/fs"
	"os/exec"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/repowrite"
)

// What the sink's log and the directories above it are created as. A log of
// what the harness is doing is state rather than a checked-in document, so it
// is the owner's to read and nobody else's.
const (
	logPermissions   fs.FileMode = 0o600
	logDirectoryMode fs.FileMode = 0o700
)

// DetachedLauncher starts the sink as its own process and returns without
// waiting for it.
type DetachedLauncher struct{}

func (DetachedLauncher) Launch(spec Launch) (int, error) {
	if strings.TrimSpace(spec.Program) == "" {
		return 0, errors.New("starting a sink needs the binary to start")
	}
	if strings.TrimSpace(spec.Log) == "" {
		return 0, errors.New("starting a sink needs somewhere for it to say what it is doing")
	}
	// The log is opened through the confined writer rather than by name. A path
	// string proves nothing about where bytes land: one symlink along it and the
	// sink's whole output — every line of what the harness is doing — is appended
	// to a file outside the root this was told to stay in. The root is declared
	// here and the containment is decided against the filesystem below it.
	root, err := repowrite.NewRoot(spec.LogRoot)
	if err != nil {
		return 0, fmt.Errorf("the Slack sink log has to stay inside %s: %w", spec.LogRoot, err)
	}
	// Appended to rather than replaced: the log of the sink that stopped is how
	// anybody finds out why it stopped, and a pass that starts a new one every
	// few minutes would otherwise erase that before it was read.
	log, err := root.OpenAppend(spec.Log, logPermissions, logDirectoryMode)
	if err != nil {
		return 0, fmt.Errorf("open the Slack sink log: %w", err)
	}
	defer log.Close()

	command := exec.Command(spec.Program, spec.Args...)
	command.Dir = spec.Dir
	// The environment the caller constructed, and nothing this process happens
	// to be holding: the tokens in it are for this product's sink alone.
	command.Env = spec.Env
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
		return pid, fmt.Errorf("release the Slack sink process: %w", err)
	}
	return pid, nil
}
