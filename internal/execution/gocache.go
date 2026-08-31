package execution

import (
	"os"
	"path/filepath"
	"strings"
)

// A run's sandbox grants writes to its worktree, to the repository's Git
// directory, and to its own temporary directory, and to nothing else. The Go
// toolchain's build cache defaults to none of those -- it is under the user's
// home -- so the first Go command a run makes dies at setup with "operation not
// permitted" before it compiles anything. What that reads like is a broken
// toolchain rather than a directory nobody granted, which is why five reports
// across four work items each rediscovered it and each worked around it by hand.
//
// So the harness points the cache at somewhere the run may actually write, for
// every run it makes: the developer's own execution probe and the check runner
// the harness applies to the change both get it, and neither has to know it
// happened. An environment the harness did not make is the Makefile's to warn --
// `make check` refuses with the redirect named rather than with the setup
// failure alone.
//
// Only GOCACHE is redirected. GOTMPDIR defaults to the temporary directory the
// sandbox already grants, and the Go command refuses a GOTMPDIR that does not
// exist, so naming one would add a way for a run to fail rather than remove one.
const goBuildCacheVariable = "GOCACHE"

// WithGoBuildCache returns environment with the Go build cache pointed inside
// the repository that workingDirectory belongs to. A nil environment starts
// from this process's own, which is what a command that would otherwise have
// inherited it needs; a working directory in no repository is left exactly as it
// was, because there is nowhere known to be writable to point the cache at.
//
// An inherited GOCACHE is replaced rather than honored. That value is the one a
// run's sandbox refuses -- an operator who set it set it for this machine, not
// for a sandbox -- so carrying it through would reintroduce the failure on
// exactly the machines most likely to have one.
//
// Nothing here writes: the Go command creates its own cache directory, so the
// harness names a path and creates nothing.
func WithGoBuildCache(environment []string, workingDirectory string) []string {
	cache, found := goBuildCache(workingDirectory)
	if !found {
		return environment
	}
	if environment == nil {
		environment = os.Environ()
	}
	kept := make([]string, 0, len(environment)+1)
	for _, entry := range environment {
		if strings.HasPrefix(entry, goBuildCacheVariable+"=") {
			continue
		}
		kept = append(kept, entry)
	}
	return append(kept, goBuildCacheVariable+"="+cache)
}

// goBuildCache is where a working directory's build cache belongs, and whether
// there is anywhere at all: a directory that is not in a repository has no Git
// directory to hold one.
//
// It goes in the repository's own Git directory rather than the worktree's
// administrative one, so every run of every worktree compiles against one cache
// instead of paying for the same packages again; the Go command trims what it
// has not used, so nothing there grows without bound. Outside the working tree
// is the other half of the choice: a cache inside it would be untracked content
// in somebody's repository, which is a dirty tree to every gate that reads one
// and unrecognized content to the composition audit.
func goBuildCache(workingDirectory string) (string, bool) {
	if strings.TrimSpace(workingDirectory) == "" {
		return "", false
	}
	gitDirectory, found := repositoryGitDirectory(workingDirectory)
	if !found {
		return "", false
	}
	return filepath.Join(gitDirectory, "yoyodyne", "go-build"), true
}

// repositoryGitDirectory resolves the Git directory of the repository a working
// directory belongs to, reading the files Git itself writes rather than running
// Git: this is asked on the way to building one command's environment, and a
// subprocess per invocation to learn a path that is written down is a cost with
// nothing to show for it.
//
// A checkout carries `.git` as a directory. A worktree carries it as a file
// naming that worktree's administrative directory, which in turn records the
// repository's own directory in `commondir`.
func repositoryGitDirectory(workingDirectory string) (string, bool) {
	path := filepath.Join(workingDirectory, ".git")
	info, err := os.Stat(path)
	if err != nil {
		return "", false
	}
	if info.IsDir() {
		return path, true
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	administrative, named := strings.CutPrefix(strings.TrimSpace(string(content)), "gitdir:")
	if !named {
		return "", false
	}
	directory := resolveGitPath(strings.TrimSpace(administrative), workingDirectory)
	if directory == "" {
		return "", false
	}
	common, err := os.ReadFile(filepath.Join(directory, "commondir"))
	if err != nil {
		// A worktree whose administrative directory records no common directory
		// is one this cannot share a cache across, which is slower and not wrong.
		return directory, true
	}
	if resolved := resolveGitPath(strings.TrimSpace(string(common)), directory); resolved != "" {
		return resolved, true
	}
	return directory, true
}

// resolveGitPath reads one of the paths Git writes into these files, which is
// absolute or is relative to the file that named it.
func resolveGitPath(path, against string) string {
	if path == "" {
		return ""
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(against, path)
	}
	return filepath.Clean(path)
}
