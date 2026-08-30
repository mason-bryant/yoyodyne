// Package buildinfo answers which build of Yoyodyne is running.
//
// A binary arrives three ways and each records its identity somewhere else. A
// release binary is stamped by the linker from the tag the release was built
// from; "go install <module>@<tag>" leaves no stamp at all and records the tag
// in the module's build information instead; a build from a checkout has only
// whatever the Makefile could describe. Consulting them in that order is what
// lets "yoyo version" name a release however the release was obtained.
package buildinfo

import "runtime/debug"

// Development is what a build reports when nothing better is known about it.
const Development = "dev"

// Resolve names the build, given whatever the linker stamped into it.
func Resolve(stamped string) string {
	return resolve(stamped, debug.ReadBuildInfo)
}

// Commit names the repository revision this binary was built from, which is a
// different question from which release it is: a version answers "what is this",
// and a revision is what a count of changes since can be measured against. Go
// stamps it into anything built from a checkout, which is every binary a
// long-running process is started from here.
//
// A binary from the module cache — "go install <module>@<tag>" — carries none,
// and so does one built with the stamping turned off. The absence is reported as
// one rather than guessed at from the version: a comparison nobody can make is
// an answer, and a comparison made against the wrong commit is not.
func Commit() string {
	return commit(debug.ReadBuildInfo)
}

func commit(read func() (*debug.BuildInfo, bool)) string {
	info, ok := read()
	if !ok {
		return ""
	}
	for _, setting := range info.Settings {
		if setting.Key == "vcs.revision" {
			return setting.Value
		}
	}
	return ""
}

func resolve(stamped string, read func() (*debug.BuildInfo, bool)) string {
	if stamped != "" && stamped != Development {
		return stamped
	}
	// The module version is the tag for a "go install module@tag" binary and
	// reads "(devel)" for anything built from a checkout, which says nothing
	// the stamp has not already said.
	if info, ok := read(); ok {
		if version := info.Main.Version; version != "" && version != "(devel)" {
			return version
		}
	}
	if stamped == "" {
		return Development
	}
	return stamped
}
