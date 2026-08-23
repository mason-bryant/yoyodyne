package config

import (
	"bytes"
	"embed"
	"fmt"
	"io/fs"
	"strings"
)

// BuiltinV1 is the versioned agent bundle shipped inside the executable. It is
// read-only, and it is used two ways: `yoyo init` generates a project's own
// complete configuration from it, and a project that would rather keep
// receiving later improvements inherits it by name and overlays what it needs.
// Either way Yoyodyne runs in a repository that never sees the Yoyodyne source.
const BuiltinV1 = "builtin:v1"

//go:embed builtin
var builtinFiles embed.FS

const builtinRoot = "builtin"

// builtinBundleDirectories maps a bundle name to its directory inside the
// embedded filesystem. New bundle versions are added here rather than by
// interpreting the name, so an unknown bundle can never resolve to a path.
var builtinBundleDirectories = map[string]string{
	BuiltinV1: builtinRoot + "/v1",
}

type bundle struct {
	name     string
	document configDocument
	personas personaLoader
}

// BuiltinBundleNames lists the bundles this executable can extend, in the order
// they are reported to an operator diagnosing a configuration.
func BuiltinBundleNames() []string {
	return []string{BuiltinV1}
}

func loadBuiltinBundle(name string) (bundle, error) {
	trimmed := strings.TrimSpace(name)
	directory, ok := builtinBundleDirectories[trimmed]
	if !ok {
		return bundle{}, fmt.Errorf("unknown configuration bundle %q; this executable provides %s", name, strings.Join(BuiltinBundleNames(), ", "))
	}
	files, err := fs.Sub(builtinFiles, directory)
	if err != nil {
		return bundle{}, fmt.Errorf("open bundle %s: %w", trimmed, err)
	}
	return loadBundleFiles(trimmed, files)
}

// loadBundleFiles reads and checks one bundle from the filesystem rooted at its
// own directory. It is separate from the lookup above so a bundle that is not
// the embedded one can be put through the same rules in a test.
func loadBundleFiles(name string, files fs.FS) (bundle, error) {
	data, err := fs.ReadFile(files, "bundle.yaml")
	if err != nil {
		return bundle{}, fmt.Errorf("read bundle %s: %w", name, err)
	}
	document, err := decodeDocument(bytes.NewReader(data))
	if err != nil {
		return bundle{}, fmt.Errorf("bundle %s: %w", name, err)
	}
	if err := checkBundleDocument(name, document); err != nil {
		return bundle{}, err
	}
	return bundle{
		name:     name,
		document: document,
		personas: builtinPersonaLoader{files: files, bundle: name},
	}, nil
}

// checkBundleDocument holds a bundle to the constraints that apply to a bundle
// rather than to a project layer. A bundle is embedded and read-only, so
// anything it fails here is a defect in this executable rather than in a
// project's configuration: every message names the bundle, and the load fails
// instead of resolving into an effective configuration.
func checkBundleDocument(name string, document configDocument) error {
	// The project layer states its own version and overwrites this one in every
	// valid configuration, so the bundle's version is never what a project runs
	// under. It is checked anyway, because the only thing it can report is a
	// bundle shipped against a schema this executable does not implement.
	if document.Version == nil {
		return fmt.Errorf("bundle %s must declare version %d", name, CurrentVersion)
	}
	if *document.Version != CurrentVersion {
		return fmt.Errorf("bundle %s declares version %d, and this executable implements version %d", name, *document.Version, CurrentVersion)
	}
	if document.Extends != nil {
		return fmt.Errorf("bundle %s must not extend another bundle", name)
	}
	if document.Product != nil {
		// Product identity belongs to the project, not to the harness. A bundle
		// that supplied one would silently name every project after itself.
		return fmt.Errorf("bundle %s must not declare a product", name)
	}
	return nil
}
