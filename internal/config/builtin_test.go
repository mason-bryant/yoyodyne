package config

import (
	"fmt"
	"strings"
	"testing"
	"testing/fstest"
)

// The bundle that ships inside the executable declares the schema version this
// executable implements. A project overwrites that version with its own, so
// nothing else would notice a bundle left behind by a schema change.
func TestBuiltinBundleDeclaresCurrentVersion(t *testing.T) {
	t.Parallel()

	loaded, err := loadBuiltinBundle(BuiltinV1)
	if err != nil {
		t.Fatalf("loadBuiltinBundle() error = %v", err)
	}
	if loaded.document.Version == nil {
		t.Fatalf("bundle %s declares no version", BuiltinV1)
	}
	if *loaded.document.Version != CurrentVersion {
		t.Errorf("bundle %s version = %d, want %d", BuiltinV1, *loaded.document.Version, CurrentVersion)
	}
}

// A bundle is embedded and read-only, so a version it should not be declaring
// is a defect in this executable. It fails at load, naming the bundle, rather
// than resolving into an effective configuration.
func TestBundleWithAnUnsupportedVersionFailsToLoad(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name    string
		bundle  string
		problem string
	}{
		{
			name:    "later version",
			bundle:  fmt.Sprintf("version: %d\n", CurrentVersion+1),
			problem: fmt.Sprintf("bundle builtin:v1 declares version %d, and this executable implements version %d", CurrentVersion+1, CurrentVersion),
		},
		{
			name:    "earlier version",
			bundle:  fmt.Sprintf("version: %d\n", CurrentVersion-1),
			problem: fmt.Sprintf("bundle builtin:v1 declares version %d, and this executable implements version %d", CurrentVersion-1, CurrentVersion),
		},
		{
			name:    "no version at all",
			bundle:  "execution:\n  max_concurrent_developers: 1\n",
			problem: fmt.Sprintf("bundle builtin:v1 must declare version %d", CurrentVersion),
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			files := fstest.MapFS{"bundle.yaml": &fstest.MapFile{Data: []byte(test.bundle)}}
			_, err := loadBundleFiles(BuiltinV1, files)
			if err == nil || !strings.Contains(err.Error(), test.problem) {
				t.Fatalf("loadBundleFiles() error = %v, want %q", err, test.problem)
			}
		})
	}
}

// The version check is one of several rules a bundle is held to, and adding it
// left the others in force.
func TestBundleRulesBeyondVersionStillHold(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name    string
		bundle  string
		problem string
	}{
		{
			name:    "extends another bundle",
			bundle:  "version: 1\nextends: builtin:v1\n",
			problem: "bundle builtin:v1 must not extend another bundle",
		},
		{
			name:    "declares a product",
			bundle:  "version: 1\nproduct:\n  id: example\n  repository: .\n",
			problem: "bundle builtin:v1 must not declare a product",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			files := fstest.MapFS{"bundle.yaml": &fstest.MapFile{Data: []byte(test.bundle)}}
			_, err := loadBundleFiles(BuiltinV1, files)
			if err == nil || !strings.Contains(err.Error(), test.problem) {
				t.Fatalf("loadBundleFiles() error = %v, want %q", err, test.problem)
			}
		})
	}
}
