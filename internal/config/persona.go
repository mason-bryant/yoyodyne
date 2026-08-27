package config

import (
	"errors"
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
type personaLoader interface {
	// load returns the persona text and a human-readable source for it.
	load(personaPath string) (text string, source string, err error)
}

type builtinPersonaLoader struct {
	files  fs.FS
	bundle string
}

func (l builtinPersonaLoader) load(personaPath string) (string, string, error) {
	clean, err := validatePersonaPath(personaPath)
	if err != nil {
		return "", "", err
	}
	slashed := filepath.ToSlash(clean)
	if !fs.ValidPath(slashed) {
		return "", "", fmt.Errorf("persona path %q is not valid inside bundle %s", personaPath, l.bundle)
	}
	data, err := fs.ReadFile(l.files, slashed)
	if err != nil {
		return "", "", fmt.Errorf("read %s persona %q: %w", l.bundle, personaPath, err)
	}
	if len(data) > MaxPersonaBytes {
		return "", "", fmt.Errorf("%s persona %q is %d bytes, limit is %d", l.bundle, personaPath, len(data), MaxPersonaBytes)
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

func (l directoryPersonaLoader) load(personaPath string) (string, string, error) {
	clean, err := validatePersonaPath(personaPath)
	if err != nil {
		return "", "", err
	}
	root, err := filepath.EvalSymlinks(l.root)
	if err != nil {
		return "", "", fmt.Errorf("resolve persona directory %q: %w", l.root, err)
	}
	resolved, err := filepath.EvalSymlinks(filepath.Join(root, clean))
	if err != nil {
		return "", "", fmt.Errorf("resolve persona %q: %w", personaPath, err)
	}
	relative, err := filepath.Rel(root, resolved)
	if err != nil {
		return "", "", fmt.Errorf("verify persona %q: %w", personaPath, err)
	}
	if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", "", fmt.Errorf("persona %q resolves outside %s", personaPath, l.root)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", "", fmt.Errorf("stat persona %q: %w", personaPath, err)
	}
	if !info.Mode().IsRegular() {
		return "", "", fmt.Errorf("persona %q is not a regular file", personaPath)
	}
	if info.Size() > MaxPersonaBytes {
		return "", "", fmt.Errorf("persona %q is %d bytes, limit is %d", personaPath, info.Size(), MaxPersonaBytes)
	}
	data, err := os.ReadFile(resolved)
	if err != nil {
		return "", "", fmt.Errorf("read persona %q: %w", personaPath, err)
	}
	if len(data) > MaxPersonaBytes {
		return "", "", fmt.Errorf("persona %q is %d bytes, limit is %d", personaPath, len(data), MaxPersonaBytes)
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

func (l firstPersonaLoader) load(personaPath string) (string, string, error) {
	var first error
	for _, loader := range l.loaders {
		text, source, err := loader.load(personaPath)
		if err == nil {
			return text, source, nil
		}
		if first == nil {
			first = err
		}
	}
	if first == nil {
		return "", "", fmt.Errorf("persona %q cannot be resolved: no persona directory was available", personaPath)
	}
	return "", "", first
}

// unavailablePersonaLoader stands in for a layer with no persona directory, so
// a configuration decoded from a stream reports why its persona cannot be read
// instead of resolving one relative to the process working directory.
type unavailablePersonaLoader struct {
	reason string
}

func (l unavailablePersonaLoader) load(personaPath string) (string, string, error) {
	return "", "", fmt.Errorf("persona %q cannot be resolved: %s", personaPath, l.reason)
}

func validatePersonaPath(personaPath string) (string, error) {
	trimmed := strings.TrimSpace(personaPath)
	if trimmed == "" {
		return "", errors.New("persona path is required")
	}
	if filepath.IsAbs(trimmed) || strings.HasPrefix(trimmed, "/") || strings.HasPrefix(trimmed, `\`) {
		return "", fmt.Errorf("persona path %q must be relative to the project %s directory", personaPath, DirectoryName)
	}
	clean := filepath.Clean(trimmed)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("persona path %q must not traverse outside the project %s directory", personaPath, DirectoryName)
	}
	if strings.ToLower(path.Ext(filepath.ToSlash(clean))) != ".md" {
		return "", fmt.Errorf("persona path %q must be a Markdown file", personaPath)
	}
	return clean, nil
}
