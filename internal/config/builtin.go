package config

import (
	"bytes"
	"embed"
	"fmt"
	"io/fs"
	"path"
	"path/filepath"
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
	files    fs.FS
}

// bundlePersonaDirectory is where a bundle keeps its personas, and so where a
// generated project finds them: the same relative path on both sides.
const bundlePersonaDirectory = "personas"

// shippedPersonas lists every persona the bundle carries, by the path a
// configuration refers to it by, in name order. It is more than the personas the
// bundle's agents bind: a role that `yoyo init` configures no agent for -- the
// program manager, whose instance is a lane somebody chooses -- still ships its
// persona, so the project that later configures one has it to bind.
func (b bundle) shippedPersonas() ([]string, error) {
	entries, err := fs.ReadDir(b.files, bundlePersonaDirectory)
	if err != nil {
		return nil, fmt.Errorf("list %s personas: %w", b.name, err)
	}
	paths := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.EqualFold(path.Ext(entry.Name()), ".md") {
			continue
		}
		paths = append(paths, path.Join(bundlePersonaDirectory, entry.Name()))
	}
	return paths, nil
}

// unboundPersonas is the shipped personas no agent of the bundle binds. They are
// the ones the resolved configuration says nothing about, so everything that
// carries the bundle's personas somewhere -- the scaffold, the baseline -- reads
// these from here and the rest from the agents that name them.
func (b bundle) unboundPersonas(bound Config) ([]string, error) {
	shipped, err := b.shippedPersonas()
	if err != nil {
		return nil, err
	}
	named := map[string]bool{}
	for _, agent := range bound.Agents {
		if agent.Persona.Defined() {
			named[path.Clean(filepath.ToSlash(agent.Persona.Path))] = true
		}
	}
	unbound := make([]string, 0, len(shipped))
	for _, shippedPath := range shipped {
		if !named[shippedPath] {
			unbound = append(unbound, shippedPath)
		}
	}
	return unbound, nil
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
		files:    files,
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
