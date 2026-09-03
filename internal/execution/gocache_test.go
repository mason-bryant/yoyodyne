package execution

// Where a run's Go build cache goes.
//
// The default is under the user's home, which no run's sandbox grants, so a
// harness that leaves it alone hands every run a toolchain that dies at setup
// before it compiles anything. What is asserted here is the redirect itself and
// the two things about it that are easy to get silently wrong: that it lands
// where the sandbox actually grants writes, and that it wins over whatever the
// machine already had in the environment.

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestACheckoutCachesInsideItsOwnGitDirectory(t *testing.T) {
	t.Parallel()

	checkout := t.TempDir()
	makeDirectory(t, filepath.Join(checkout, ".git"))

	want := filepath.Join(checkout, ".git", "yoyodyne", "go-build")
	if cache := buildCacheIn(t, WithGoBuildCache([]string{"PATH=/usr/bin"}, checkout)); cache != want {
		t.Fatalf("GOCACHE = %q, want %q", cache, want)
	}
}

// Every worktree of one repository compiles against one cache. A cache per
// worktree would be correct and would also mean every run of every work item
// compiles the whole dependency graph again, which is the cost this is placed
// in the repository's own Git directory to avoid.
func TestEveryWorktreeOfARepositoryCachesInTheOneGitDirectory(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	checkout := filepath.Join(root, "checkout")
	gitDirectory := filepath.Join(checkout, ".git")
	administrative := filepath.Join(gitDirectory, "worktrees", "one")
	makeDirectory(t, administrative)
	writeFile(t, filepath.Join(administrative, "commondir"), "../..\n")

	worktree := filepath.Join(root, "worktree")
	makeDirectory(t, worktree)
	writeFile(t, filepath.Join(worktree, ".git"), "gitdir: "+administrative+"\n")

	want := filepath.Join(gitDirectory, "yoyodyne", "go-build")
	if cache := buildCacheIn(t, WithGoBuildCache(nil, worktree)); cache != want {
		t.Fatalf("GOCACHE = %q, want the repository's own %q", cache, want)
	}
}

// The environment a command would have inherited survives beside the redirect:
// a process given only GOCACHE has no PATH and no HOME and runs nothing.
func TestTheRedirectIsAddedToTheEnvironmentRatherThanReplacingIt(t *testing.T) {
	t.Parallel()

	checkout := t.TempDir()
	makeDirectory(t, filepath.Join(checkout, ".git"))

	environment := WithGoBuildCache(nil, checkout)
	if len(environment) < 2 {
		t.Fatalf("environment = %v, want this process's own environment beside the redirect", environment)
	}
	for _, entry := range os.Environ() {
		if strings.HasPrefix(entry, goBuildCacheVariable+"=") {
			continue
		}
		if !slices.Contains(environment, entry) {
			t.Fatalf("environment dropped %q", entry)
		}
	}
}

// An operator's own GOCACHE is the value the sandbox refuses, so honoring it
// would put the failure back on the machines most likely to have set one.
func TestAnInheritedBuildCacheIsReplaced(t *testing.T) {
	t.Parallel()

	checkout := t.TempDir()
	makeDirectory(t, filepath.Join(checkout, ".git"))

	environment := WithGoBuildCache([]string{"GOCACHE=/somewhere/nobody/granted", "PATH=/usr/bin"}, checkout)
	want := filepath.Join(checkout, ".git", "yoyodyne", "go-build")
	if cache := buildCacheIn(t, environment); cache != want {
		t.Fatalf("GOCACHE = %q, want %q", cache, want)
	}
	for _, entry := range environment {
		if entry == "GOCACHE=/somewhere/nobody/granted" {
			t.Fatalf("environment = %v, want the inherited cache gone rather than shadowed", environment)
		}
	}
}

// A directory in no repository has nowhere known to be writable, so it is left
// exactly as it was rather than pointed at a guess.
func TestADirectoryInNoRepositoryIsLeftAlone(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name      string
		directory string
	}{
		{name: "no repository", directory: t.TempDir()},
		{name: "named nothing", directory: "   "},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if environment := WithGoBuildCache(nil, test.directory); environment != nil {
				t.Fatalf("WithGoBuildCache() = %v, want the environment untouched", environment)
			}
			if environment := WithGoBuildCache([]string{"PATH=/usr/bin"}, test.directory); !slices.Equal(environment, []string{"PATH=/usr/bin"}) {
				t.Fatalf("WithGoBuildCache() = %v, want the environment untouched", environment)
			}
		})
	}
}

// A `.git` file that names nothing readable is a repository this cannot place a
// cache in, which is left alone rather than resolved to the file itself.
func TestAWorktreeFileNamingNoDirectoryIsLeftAlone(t *testing.T) {
	t.Parallel()

	worktree := t.TempDir()
	writeFile(t, filepath.Join(worktree, ".git"), "this is not what Git writes\n")

	if environment := WithGoBuildCache([]string{"PATH=/usr/bin"}, worktree); !slices.Equal(environment, []string{"PATH=/usr/bin"}) {
		t.Fatalf("WithGoBuildCache() = %v, want the environment untouched", environment)
	}
}

func buildCacheIn(t *testing.T, environment []string) string {
	t.Helper()

	cache := ""
	for _, entry := range environment {
		if value, ok := strings.CutPrefix(entry, goBuildCacheVariable+"="); ok {
			cache = value
		}
	}
	if cache == "" {
		t.Fatalf("environment = %v, want a %s entry", environment, goBuildCacheVariable)
	}
	return cache
}

func makeDirectory(t *testing.T, path string) {
	t.Helper()

	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", path, err)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()

	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", path, err)
	}
}
