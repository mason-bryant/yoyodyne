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
