package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const (
	// DirectoryName is the portable project configuration directory. It holds
	// the project configuration and any persona overrides, and nothing that
	// depends on the machine it was written on.
	DirectoryName = ".yoyodyne"
	// FileName is the project configuration file inside DirectoryName.
	FileName = "config.yaml"
	// LegacyFileName is the pre-directory configuration file. It is still
	// accepted so an existing project keeps working without being migrated.
	LegacyFileName = ".yoyodyne.yaml"
)

// NotFoundError reports that no configuration exists at or above a directory.
// It names what was searched for so an operator can create the right file
// rather than guess.
type NotFoundError struct {
	StartDirectory string
}

func (e NotFoundError) Error() string {
	return fmt.Sprintf("no Yoyodyne configuration found in %s or any parent directory (looked for %s/%s and %s)",
		e.StartDirectory, DirectoryName, FileName, LegacyFileName)
}

// Discover finds the nearest project configuration by walking from a starting
// directory towards the filesystem root. Yoyodyne is therefore runnable from a
// nested directory of a project, not only from its root.
func Discover(startDirectory string) (string, error) {
	start, err := filepath.Abs(startDirectory)
	if err != nil {
		return "", fmt.Errorf("resolve start directory: %w", err)
	}
	for directory := start; ; {
		for _, candidate := range []string{
			filepath.Join(directory, DirectoryName, FileName),
			filepath.Join(directory, LegacyFileName),
		} {
			info, err := os.Stat(candidate)
			if err != nil {
				if errors.Is(err, os.ErrNotExist) {
					continue
				}
				return "", fmt.Errorf("inspect %q: %w", candidate, err)
			}
			if info.Mode().IsRegular() {
				return candidate, nil
			}
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			return "", NotFoundError{StartDirectory: start}
		}
		directory = parent
	}
}

// ProjectDirectory is the directory a configuration's relative paths resolve
// against. A configuration inside .yoyodyne describes the project that contains
// that directory, not the directory itself, so "repository: ." keeps meaning
// the project root after migration.
func ProjectDirectory(configPath string) string {
	directory := filepath.Dir(configPath)
	if filepath.Base(directory) == DirectoryName {
		return filepath.Dir(directory)
	}
	return directory
}

// personaDirectory is where a configuration file's project personas live. A
// configuration inside .yoyodyne uses that directory; a legacy configuration
// beside it uses the .yoyodyne directory of the same project.
func personaDirectory(configPath string) string {
	directory := filepath.Dir(configPath)
	if filepath.Base(directory) == DirectoryName {
		return directory
	}
	return filepath.Join(directory, DirectoryName)
}
