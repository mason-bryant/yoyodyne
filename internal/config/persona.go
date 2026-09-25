package config

import (
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// personaLoader reads one persona for the layer that declared it. Built-in
// personas come from the read-only bundle inside the executable and project
// personas from the project's .yoyodyne directory, so each layer resolves its
// own paths and neither can reach into the other.
//
// The same loader reads a program manager's remit, which is held to every rule a
// persona is held to. kind is the word its refusals name — "persona" or
// "remit" — so a remit is refused in the persona rules' own wording rather than
// in a second set of messages that could drift from the first.
type personaLoader interface {
	// load returns the document's text and a human-readable source for it.
	load(kind, personaPath string) (text string, source string, err error)
}

type builtinPersonaLoader struct {
	files  fs.FS
	bundle string
}

func (l builtinPersonaLoader) load(kind, personaPath string) (string, string, error) {
	clean, err := validatePersonaPath(kind, personaPath)
	if err != nil {
		return "", "", err
	}
	slashed := filepath.ToSlash(clean)
	if !fs.ValidPath(slashed) {
		return "", "", fmt.Errorf("%s path %q is not valid inside bundle %s", kind, personaPath, l.bundle)
	}
	data, err := fs.ReadFile(l.files, slashed)
	if err != nil {
		return "", "", fmt.Errorf("read %s %s %q: %w", l.bundle, kind, personaPath, err)
	}
	if len(data) > MaxPersonaBytes {
		return "", "", fmt.Errorf("%s %s %q is %d bytes, limit is %d", l.bundle, kind, personaPath, len(data), MaxPersonaBytes)
	}
	return string(data), l.bundle + "/" + slashed, nil
}

// directoryPersonaLoader resolves project personas strictly inside one
// directory. Absolute paths, traversal, and symlinks that escape the directory
// all fail closed rather than letting a configuration file read arbitrary
// local content into a prompt.
type directoryPersonaLoader struct {
	root string
}

func (l directoryPersonaLoader) load(kind, personaPath string) (string, string, error) {
	clean, err := validatePersonaPath(kind, personaPath)
	if err != nil {
		return "", "", err
	}
	root, err := filepath.EvalSymlinks(l.root)
	if err != nil {
		return "", "", fmt.Errorf("resolve %s directory %q: %w", kind, l.root, err)
	}
	resolved, err := filepath.EvalSymlinks(filepath.Join(root, clean))
	if err != nil {
		return "", "", fmt.Errorf("resolve %s %q: %w", kind, personaPath, err)
	}
	relative, err := filepath.Rel(root, resolved)
	if err != nil {
		return "", "", fmt.Errorf("verify %s %q: %w", kind, personaPath, err)
	}
	if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", "", fmt.Errorf("%s %q resolves outside %s", kind, personaPath, l.root)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", "", fmt.Errorf("stat %s %q: %w", kind, personaPath, err)
	}
	if !info.Mode().IsRegular() {
		return "", "", fmt.Errorf("%s %q is not a regular file", kind, personaPath)
	}
	if info.Size() > MaxPersonaBytes {
		return "", "", fmt.Errorf("%s %q is %d bytes, limit is %d", kind, personaPath, info.Size(), MaxPersonaBytes)
	}
	data, err := os.ReadFile(resolved)
	if err != nil {
		return "", "", fmt.Errorf("read %s %q: %w", kind, personaPath, err)
	}
	if len(data) > MaxPersonaBytes {
		return "", "", fmt.Errorf("%s %q is %d bytes, limit is %d", kind, personaPath, len(data), MaxPersonaBytes)
	}
	return string(data), resolved, nil
}

// firstPersonaLoader reads each persona from the first directory that has it,
// for the one configuration shape whose personas have two possible homes: a
// config.yaml outside a .yoyodyne directory, which keeps them beside itself and
// kept them in a .yoyodyne sibling before that was so.
//
// It is a fallback rather than a search: what a directory refuses -- a persona
// that traverses out of it, one that is not a Markdown file, one too large --
// it refuses in every directory, so nothing here loosens what one of them will
// read. The failure reported is the first loader's, because the first directory
// is where the persona belongs and naming the last one looked in would send an
// operator to the wrong place.
type firstPersonaLoader struct {
	loaders []personaLoader
}

func (l firstPersonaLoader) load(kind, personaPath string) (string, string, error) {
	var first error
	for _, loader := range l.loaders {
		text, source, err := loader.load(kind, personaPath)
		if err == nil {
			return text, source, nil
		}
		if first == nil {
			first = err
		}
	}
	if first == nil {
		return "", "", fmt.Errorf("%s %q cannot be resolved: no persona directory was available", kind, personaPath)
	}
	return "", "", first
}

// unavailablePersonaLoader stands in for a layer with no persona directory, so
// a configuration decoded from a stream reports why its persona cannot be read
// instead of resolving one relative to the process working directory.
type unavailablePersonaLoader struct {
	reason string
}

func (l unavailablePersonaLoader) load(kind, personaPath string) (string, string, error) {
	return "", "", fmt.Errorf("%s %q cannot be resolved: %s", kind, personaPath, l.reason)
}

func validatePersonaPath(kind, personaPath string) (string, error) {
	trimmed := strings.TrimSpace(personaPath)
	if trimmed == "" {
		return "", fmt.Errorf("%s path is required", kind)
	}
	if filepath.IsAbs(trimmed) || strings.HasPrefix(trimmed, "/") || strings.HasPrefix(trimmed, `\`) {
		return "", fmt.Errorf("%s path %q must be relative to the project %s directory", kind, personaPath, DirectoryName)
	}
	clean := filepath.Clean(trimmed)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%s path %q must not traverse outside the project %s directory", kind, personaPath, DirectoryName)
	}
	if strings.ToLower(path.Ext(filepath.ToSlash(clean))) != ".md" {
		return "", fmt.Errorf("%s path %q must be a Markdown file", kind, personaPath)
	}
	return clean, nil
}
