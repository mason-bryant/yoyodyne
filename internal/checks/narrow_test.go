package checks

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// The narrowing files each changed path under the nearest package above it, the
// way the Go command files a package's own test data, and leaves alone what is
// above every package: a document changes no package's race behaviour, and the
// checks that run whole still cover it.
func TestAChangeIsNarrowedToThePackagesItTouches(t *testing.T) {
	t.Parallel()

	root := goModule(t,
		"internal/checks/runner.go",
		"internal/checks/testdata/fixture.txt",
		"internal/orchestrator/pipeline.go",
		"cmd/yoyo/main.go",
	)
	narrowing := NarrowGoPackages(root, []string{
		"internal/checks/testdata/fixture.txt",
		"internal/orchestrator/pipeline_test.go",
		"internal/orchestrator/pipeline.go",
		"docs/configuration.md",
		"Makefile",
	})
	if narrowing.Whole {
		t.Fatalf("narrowing = %#v, want the packages rather than the whole module", narrowing)
	}
	if want := []string{"./internal/checks", "./internal/orchestrator"}; !reflect.DeepEqual(narrowing.Packages, want) {
		t.Fatalf("packages = %v, want %v", narrowing.Packages, want)
	}
	if got := narrowing.Env(); got != "YOYODYNE_CHANGED_GO_PACKAGES=./internal/checks ./internal/orchestrator" {
		t.Fatalf("Env() = %q", got)
	}
}

// A change that reaches every package is not narrowed, and the reason is
// recorded rather than inferred: a dependency change is the whole module.
func TestAModuleFileChangeIsTheWholeModule(t *testing.T) {
	t.Parallel()

	root := goModule(t, "internal/checks/runner.go")
	for _, changed := range []string{"go.mod", "go.sum", "vendor/example.com/dep/dep.go"} {
		narrowing := NarrowGoPackages(root, []string{"internal/checks/runner.go", changed})
		if !narrowing.Whole || narrowing.Value() != WholeModule || narrowing.Reason == "" {
			t.Fatalf("%s: narrowing = %#v, want the whole module with a reason", changed, narrowing)
		}
	}
}

// A repository that is not a Go module has nothing to narrow within, and says
// so rather than reporting that the change touches no package.
func TestARepositoryThatIsNoGoModuleIsTheWholeModule(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	narrowing := NarrowGoPackages(root, []string{"src/index.ts"})
	if !narrowing.Whole || narrowing.Reason == "" {
		t.Fatalf("narrowing = %#v, want the whole module with a reason", narrowing)
	}
}

// A change above every package is a change to no package: the variable is
// empty, and what it describes says why.
func TestAChangeTouchingNoPackageIsEmpty(t *testing.T) {
	t.Parallel()

	root := goModule(t, "internal/checks/runner.go")
	narrowing := NarrowGoPackages(root, []string{"docs/operations.md", "README.md", "internal/README.md"})
	if narrowing.Whole || len(narrowing.Packages) != 0 || narrowing.Value() != "" {
		t.Fatalf("narrowing = %#v, want no package", narrowing)
	}
	if got := narrowing.Describe(); got != "no Go package (the change touches none)" {
		t.Fatalf("Describe() = %q", got)
	}
}

// A directory the Go command ignores is stepped over rather than named: a
// pattern naming testdata matches nothing, and the package that owns it is what
// the change is to.
func TestADirectoryTheGoCommandIgnoresIsFiledUnderItsPackage(t *testing.T) {
	t.Parallel()

	root := goModule(t, "internal/doclink/doclink.go", "internal/doclink/testdata/broken.go", "internal/doclink/_fixtures/x.go")
	narrowing := NarrowGoPackages(root, []string{"internal/doclink/testdata/broken.go", "internal/doclink/_fixtures/x.go"})
	if want := []string{"./internal/doclink"}; !reflect.DeepEqual(narrowing.Packages, want) {
		t.Fatalf("packages = %v, want %v", narrowing.Packages, want)
	}
}

// goModule lays out a module with Go source at the given paths, so the
// narrowing reads a tree rather than a fixture that describes one.
func goModule(t *testing.T, files ...string) string {
	t.Helper()
	root := t.TempDir()
	write := func(relative, content string) {
		path := filepath.Join(root, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("MkdirAll() error = %v", err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("WriteFile() error = %v", err)
		}
	}
	write("go.mod", "module example.com/project\n")
	for _, file := range files {
		write(file, "package x\n")
	}
	return root
}
