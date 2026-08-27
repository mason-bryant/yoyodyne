package config

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
)

const (
	// DirectoryName is the portable project configuration directory. It holds
	// the project configuration and any persona overrides, and nothing that
	// depends on the machine it was written on.
	DirectoryName = ".yoyodyne"
	// FileName is the project configuration file inside DirectoryName. It is
	// also what names a configuration directory anywhere else: a directory
	// holding this file holds the personas beside it.
	FileName = "config.yaml"
	// LegacyFileName is the pre-directory configuration file. It is still
	// accepted so an existing project keeps working without being migrated.
	LegacyFileName = ".yoyodyne.yaml"
)

const (
	// EnvironmentVariable names one configuration outright, wherever it is. It
	// is read before anything is searched for, so a shell that exports it
	// configures every command run in that shell without repeating --config.
	EnvironmentVariable = "YOYODYNE_CONFIG"
	// HomeVariable relocates the directory external configurations are kept in,
	// the way YOYODYNE_STATE_HOME relocates run state, and for the same reasons:
	// a machine that keeps its dot-directories somewhere else, and a test that
	// must not read the operator's own.
	HomeVariable = "YOYODYNE_CONFIG_HOME"
	// ExternalDirectoryName holds one directory per repository inside that home.
	ExternalDirectoryName = "projects"
)

// externalKeyDigits is how much of a repository's digest its directory name
// carries: enough that two repositories one machine holds never collide, and
// short enough that an operator can still match a directory to a project by eye
// from the readable half beside it. It is the configuration revision's number
// for the configuration revision's reason.
const externalKeyDigits = 12

// gitDirectoryName marks the root of the repository an external configuration is
// keyed by. It is read off the filesystem rather than asked of Git, because
// discovery happens before anything else and must not depend on a subprocess.
const gitDirectoryName = ".git"

// NotFoundError reports that no configuration exists for a directory. It names
// every place that was looked in so an operator can create the right file rather
// than guess, and the external path only when there was a repository to key one
// by — naming a path nothing could have looked at would be inventing a place.
type NotFoundError struct {
	StartDirectory string
	// ExternalPath is where this machine would keep a configuration for the
	// repository the start directory is in, and is empty when it is in none.
	ExternalPath string
}

func (e NotFoundError) Error() string {
	message := fmt.Sprintf("no Yoyodyne configuration found in %s or any parent directory (looked for %s/%s and %s)",
		e.StartDirectory, DirectoryName, FileName, LegacyFileName)
	if e.ExternalPath != "" {
		message += fmt.Sprintf(", nor at %s, where a configuration kept outside the repository lives", e.ExternalPath)
	}
	return message + fmt.Sprintf("; write one with `yoyo init`, keep one outside the repository with `yoyo init --external`, or name one with --config or %s",
		EnvironmentVariable)
}

// Discover finds the configuration that governs a directory, using this
// process's own environment.
func Discover(startDirectory string) (string, error) {
	return DiscoverIn(os.Getenv, os.UserHomeDir, startDirectory)
}

// DiscoverIn finds the configuration that governs a directory on a described
// machine, in this order:
//
//  1. the configuration YOYODYNE_CONFIG names, if it names one;
//  2. otherwise the nearest project configuration, walking from the starting
//     directory towards the filesystem root, so Yoyodyne is runnable from a
//     nested directory of a project rather than only from its root;
//  3. otherwise this machine's own configuration for the repository that
//     directory is in, which is what a contributor who cannot commit tool
//     config to somebody else's repository keeps.
//
// The project's own configuration is looked for before this machine's on
// purpose: a repository that describes itself is what every collaborator gets,
// and a file on one machine must not quietly win over it.
func DiscoverIn(getenv func(string) string, userHomeDir func() (string, error), startDirectory string) (string, error) {
	start, err := filepath.Abs(startDirectory)
	if err != nil {
		return "", fmt.Errorf("resolve start directory: %w", err)
	}
	named, err := namedConfiguration(getenv)
	if err != nil || named != "" {
		return named, err
	}
	project, err := projectConfiguration(start)
	if err != nil || project != "" {
		return project, err
	}

	// An external configuration is keyed by the repository, so a directory in no
	// repository has no key and nothing was looked up: that is reported as having
	// searched one place fewer rather than as a failure to read this machine.
	external, err := externalConfiguration(getenv, userHomeDir, start)
	if err != nil {
		return "", err
	}
	if external == "" {
		return "", NotFoundError{StartDirectory: start}
	}
	regular, err := isRegularFile(external)
	if err != nil {
		return "", err
	}
	if regular {
		return external, nil
	}
	return "", NotFoundError{StartDirectory: start, ExternalPath: external}
}

// namedConfiguration reads the configuration YOYODYNE_CONFIG names, and answers
// with nothing at all when the variable is unset.
//
// A variable that is set and names nothing readable is a failure rather than a
// step that is skipped. It is an instruction, exactly as --config is, and
// silently falling through to a different configuration than the one the
// operator named is how a command ends up doing the right thing to the wrong
// project.
func namedConfiguration(getenv func(string) string) (string, error) {
	value := strings.TrimSpace(getenv(EnvironmentVariable))
	if value == "" {
		return "", nil
	}
	if !filepath.IsAbs(value) {
		return "", fmt.Errorf("%s must be an absolute path and is %q", EnvironmentVariable, value)
	}
	candidate := filepath.Clean(value)
	info, err := os.Stat(candidate)
	if err != nil {
		return "", fmt.Errorf("%s names %s: %w", EnvironmentVariable, candidate, err)
	}
	// A directory is taken as a configuration directory rather than refused, so
	// the variable accepts what an operator has in hand: `.yoyodyne` and the
	// directory `init --external` reports are both directories holding the file.
	if info.IsDir() {
		candidate = filepath.Join(candidate, FileName)
		if info, err = os.Stat(candidate); err != nil {
			return "", fmt.Errorf("%s names a directory with no %s in it: %w", EnvironmentVariable, FileName, err)
		}
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("%s names %s, which is not a file", EnvironmentVariable, candidate)
	}
	return candidate, nil
}

// projectConfiguration is the nearest configuration a project carries, or
// nothing when no directory up to the filesystem root carries one.
func projectConfiguration(start string) (string, error) {
	for directory := start; ; {
		for _, candidate := range []string{
			filepath.Join(directory, DirectoryName, FileName),
			filepath.Join(directory, LegacyFileName),
		} {
			regular, err := isRegularFile(candidate)
			if err != nil {
				return "", err
			}
			if regular {
				return candidate, nil
			}
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			return "", nil
		}
		directory = parent
	}
}

// externalConfiguration is where this machine keeps the configuration of the
// repository a directory is in, whether or not anything is there yet, and
// nothing at all when the directory is in no repository.
func externalConfiguration(getenv func(string) string, userHomeDir func() (string, error), start string) (string, error) {
	repository, err := RepositoryRoot(start)
	if err != nil || repository == "" {
		return "", err
	}
	candidate, err := ExternalPath(getenv, userHomeDir, repository)
	if err == nil {
		return candidate, nil
	}
	// A machine with no home directory has nowhere to keep one, which is a place
	// fewer to look rather than a failure of the search: a command run where HOME
	// is unset must still report the configuration it did not find rather than
	// the home it could not resolve. A variable that was set and is wrong is the
	// operator's instruction, and still fails.
	if strings.TrimSpace(getenv(HomeVariable)) == "" && strings.TrimSpace(getenv("XDG_CONFIG_HOME")) == "" {
		return "", nil
	}
	return "", err
}

func isRegularFile(candidate string) (bool, error) {
	info, err := os.Stat(candidate)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("inspect %q: %w", candidate, err)
	}
	return info.Mode().IsRegular(), nil
}

// ExternalHome is the directory this machine keeps the configurations of
// projects that do not carry their own. It is one location on every platform,
// unlike the state root, because it holds files an operator edits by hand and a
// path they can be told over a shoulder is worth more here than each system's
// own convention for application data.
func ExternalHome(getenv func(string) string, userHomeDir func() (string, error)) (string, error) {
	if value := strings.TrimSpace(getenv(HomeVariable)); value != "" {
		if !filepath.IsAbs(value) {
			return "", fmt.Errorf("%s must be an absolute path and is %q", HomeVariable, value)
		}
		return filepath.Clean(value), nil
	}
	if value := strings.TrimSpace(getenv("XDG_CONFIG_HOME")); value != "" {
		if !filepath.IsAbs(value) {
			return "", errors.New("XDG_CONFIG_HOME must be an absolute path")
		}
		return filepath.Join(filepath.Clean(value), "yoyodyne"), nil
	}
	home, err := userHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve user home directory: %w", err)
	}
	return filepath.Join(home, ".config", "yoyodyne"), nil
}

// ExternalDirectory is where one repository's external configuration lives,
// named relative to ExternalHome in slash form so it can be written through the
// confined-write primitive as a path inside that home.
func ExternalDirectory(repositoryRoot string) string {
	return path.Join(ExternalDirectoryName, externalKey(repositoryRoot))
}

// ExternalPath is the external configuration file of one repository, absolute.
func ExternalPath(getenv func(string) string, userHomeDir func() (string, error), repositoryRoot string) (string, error) {
	home, err := ExternalHome(getenv, userHomeDir)
	if err != nil {
		return "", err
	}
	return filepath.Join(home, filepath.FromSlash(ExternalDirectory(repositoryRoot)), FileName), nil
}

// externalKey names one repository's directory inside the configurations home.
//
// It is two halves because it answers to two readers. The digest is what makes
// it a key: a repository is identified by where it is checked out, which is a
// path rather than a name, and two checkouts of the same project on one machine
// are two projects to configure. The readable half is for the operator listing
// the directory, who otherwise has a home full of hex.
//
// The path is resolved through its own symlinks first, so a checkout reached by
// two names is one key rather than two configurations that silently disagree.
func externalKey(repositoryRoot string) string {
	resolved := repositoryRoot
	if absolute, err := filepath.Abs(repositoryRoot); err == nil {
		resolved = absolute
		if linked, err := filepath.EvalSymlinks(absolute); err == nil {
			resolved = linked
		}
	}
	digest := sha256.Sum256([]byte(resolved))
	key := hex.EncodeToString(digest[:])[:externalKeyDigits]
	if name := externalKeyName(filepath.Base(resolved)); name != "" {
		return name + "-" + key
	}
	return key
}

// externalKeyName is the readable half of a key: the checkout's own directory
// name, reduced to what is a directory name everywhere. It carries no meaning —
// the digest beside it is what identifies the repository — so a name that
// reduces to nothing is simply left out rather than invented.
func externalKeyName(name string) string {
	var builder strings.Builder
	separated := false
	for _, letter := range strings.ToLower(name) {
		switch {
		case letter >= 'a' && letter <= 'z', letter >= '0' && letter <= '9':
			if separated && builder.Len() > 0 {
				builder.WriteByte('-')
			}
			separated = false
			builder.WriteRune(letter)
		default:
			separated = true
		}
	}
	return builder.String()
}

// RepositoryRoot is the repository a directory belongs to, or nothing when it
// belongs to none. It is what an external configuration is keyed by: a
// configuration kept outside a repository still describes that repository, and
// it has to be found from any directory inside it.
//
// A linked worktree is resolved to the repository it was added from rather than
// treated as a repository of its own, because it is the same project: a run's
// worktree, and a check or a hook that shells out to `yoyo` from inside one,
// must find the configuration the checkout it came from is configured by.
func RepositoryRoot(startDirectory string) (string, error) {
	start, err := filepath.Abs(startDirectory)
	if err != nil {
		return "", fmt.Errorf("resolve start directory: %w", err)
	}
	for directory := start; ; {
		marker := filepath.Join(directory, gitDirectoryName)
		info, err := os.Stat(marker)
		switch {
		case err == nil && info.IsDir():
			return directory, nil
		case err == nil && info.Mode().IsRegular():
			common, err := commonGitDirectory(marker)
			if err != nil {
				return "", err
			}
			if common == "" {
				// A pointer this does not understand is left as the worktree it was
				// found in rather than refused: an unreadable marker is not a reason
				// for every command to stop, and the worktree is still a directory a
				// configuration can be keyed by.
				return directory, nil
			}
			return filepath.Dir(common), nil
		case err != nil && !errors.Is(err, os.ErrNotExist):
			return "", fmt.Errorf("inspect %q: %w", marker, err)
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			return "", nil
		}
		directory = parent
	}
}

// commonGitDirectory reads the repository directory a linked worktree's `.git`
// file points at. Git writes `gitdir: <path>` there, naming the worktree's own
// administrative directory, and puts the path of the repository they all share
// in a `commondir` file beside it. Anything else is answered with nothing.
func commonGitDirectory(marker string) (string, error) {
	content, err := os.ReadFile(marker)
	if err != nil {
		return "", fmt.Errorf("read %q: %w", marker, err)
	}
	pointer, found := strings.CutPrefix(strings.TrimSpace(string(content)), "gitdir:")
	if !found {
		return "", nil
	}
	worktree := strings.TrimSpace(pointer)
	if worktree == "" {
		return "", nil
	}
	if !filepath.IsAbs(worktree) {
		worktree = filepath.Join(filepath.Dir(marker), worktree)
	}
	common, err := os.ReadFile(filepath.Join(worktree, "commondir"))
	if errors.Is(err, os.ErrNotExist) {
		return filepath.Clean(worktree), nil
	}
	if err != nil {
		return "", fmt.Errorf("read the repository directory of the worktree at %q: %w", filepath.Dir(marker), err)
	}
	shared := strings.TrimSpace(string(common))
	if shared == "" {
		return filepath.Clean(worktree), nil
	}
	if !filepath.IsAbs(shared) {
		shared = filepath.Join(worktree, shared)
	}
	return filepath.Clean(shared), nil
}

// ProjectDirectory is the directory a configuration's relative paths resolve
// against. A configuration inside .yoyodyne describes the project that contains
// that directory, not the directory itself, so "repository: ." keeps meaning
// the project root after migration.
//
// A configuration kept outside the repository it describes has no such project
// above it, and this answers with the directory the file is in. That is why an
// external configuration states an absolute `product.repository` — which is what
// `yoyo init --external` writes — rather than a relative one that would resolve
// against a directory holding nothing but the configuration.
func ProjectDirectory(configPath string) string {
	directory := filepath.Dir(configPath)
	if filepath.Base(directory) == DirectoryName {
		return filepath.Dir(directory)
	}
	return directory
}

// personaDirectory is where a configuration file's project personas live. A
// configuration inside .yoyodyne uses that directory, and so does one in a
// directory of its own outside the repository: in both, the personas are beside
// the file. A legacy configuration uses the .yoyodyne directory of the same
// project, which is where migrating it would put them.
func personaDirectory(configPath string) string {
	directory := filepath.Dir(configPath)
	if filepath.Base(directory) == DirectoryName || filepath.Base(configPath) == FileName {
		return directory
	}
	return filepath.Join(directory, DirectoryName)
}
