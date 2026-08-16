package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDiscoverFindsTheNearestProjectConfiguration(t *testing.T) {
	t.Parallel()

	project := t.TempDir()
	writeProject(t, project, minimalProjectConfig, nil)
	nested := filepath.Join(project, "internal", "deeply", "nested")
	if err := os.MkdirAll(nested, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	want := filepath.Join(project, DirectoryName, FileName)

	for _, start := range []string{project, nested} {
		got, err := Discover(start)
		if err != nil {
			t.Fatalf("Discover(%q) error = %v", start, err)
		}
		if got != want {
			t.Errorf("Discover(%q) = %q, want %q", start, got, want)
		}
	}
}

// A project that has not migrated yet keeps working: the legacy file is still
// discovered, and its personas resolve against the .yoyodyne directory the
// project would create during migration.
func TestDiscoverFallsBackToTheLegacyFile(t *testing.T) {
	t.Parallel()

	project := t.TempDir()
	path := filepath.Join(project, LegacyFileName)
	if err := os.WriteFile(path, []byte(validBootstrapConfig), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	got, err := Discover(project)
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if got != path {
		t.Fatalf("Discover() = %q, want %q", got, path)
	}
	if want := project; personaDirectory(got) != filepath.Join(want, DirectoryName) {
		t.Fatalf("persona directory = %q", personaDirectory(got))
	}
}

// The directory form wins over the legacy file in the same directory, so a
// half-finished migration cannot silently keep using the old configuration.
func TestDiscoverPrefersTheDirectoryForm(t *testing.T) {
	t.Parallel()

	project := t.TempDir()
	writeProject(t, project, minimalProjectConfig, nil)
	if err := os.WriteFile(filepath.Join(project, LegacyFileName), []byte(validBootstrapConfig), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	got, err := Discover(project)
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if want := filepath.Join(project, DirectoryName, FileName); got != want {
		t.Fatalf("Discover() = %q, want %q", got, want)
	}
}

func TestDiscoverReportsWhatItLookedFor(t *testing.T) {
	t.Parallel()

	_, err := Discover(t.TempDir())
	var notFound NotFoundError
	if !errors.As(err, &notFound) {
		t.Fatalf("Discover() error = %v, want NotFoundError", err)
	}
	for _, expected := range []string{DirectoryName + "/" + FileName, LegacyFileName} {
		if !strings.Contains(err.Error(), expected) {
			t.Errorf("error %q does not mention %q", err, expected)
		}
	}
}

func TestProjectDirectoryIsTheProjectNotTheConfigurationDirectory(t *testing.T) {
	t.Parallel()

	project := filepath.Join(string(filepath.Separator), "tmp", "example")
	if got := ProjectDirectory(filepath.Join(project, DirectoryName, FileName)); got != project {
		t.Errorf("ProjectDirectory() = %q, want %q", got, project)
	}
	if got := ProjectDirectory(filepath.Join(project, LegacyFileName)); got != project {
		t.Errorf("ProjectDirectory() legacy = %q, want %q", got, project)
	}
}
