package config

import (
	"bytes"
	"embed"
	"fmt"
	"io/fs"
	"strings"
)

// BuiltinV1 is the versioned agent bundle shipped inside the executable. It is
// read-only: a project inherits it by name and overlays what it needs, so
// Yoyodyne can run in a repository that never sees the Yoyodyne source.
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
	data, err := builtinFiles.ReadFile(directory + "/bundle.yaml")
	if err != nil {
		return bundle{}, fmt.Errorf("read bundle %s: %w", trimmed, err)
	}
	document, err := decodeDocument(bytes.NewReader(data))
	if err != nil {
		return bundle{}, fmt.Errorf("bundle %s: %w", trimmed, err)
	}
	if document.Extends != nil {
		return bundle{}, fmt.Errorf("bundle %s must not extend another bundle", trimmed)
	}
	if document.Product != nil {
		// Product identity belongs to the project, not to the harness. A bundle
		// that supplied one would silently name every project after itself.
		return bundle{}, fmt.Errorf("bundle %s must not declare a product", trimmed)
	}
	files, err := fs.Sub(builtinFiles, directory)
	if err != nil {
		return bundle{}, fmt.Errorf("open bundle %s: %w", trimmed, err)
	}
	return bundle{
		name:     trimmed,
		document: document,
		personas: builtinPersonaLoader{files: files, bundle: trimmed},
	}, nil
}
