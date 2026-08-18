package buildinfo

import (
	"runtime/debug"
	"testing"
)

func TestResolvePrefersTheLinkerStamp(t *testing.T) {
	moduleVersion := buildInfo("v9.9.9")

	if got := resolve("v1.2.3", moduleVersion); got != "v1.2.3" {
		t.Fatalf("resolve = %q, want the stamped version v1.2.3", got)
	}
}

func TestResolveFallsBackToTheModuleVersion(t *testing.T) {
	// This is the "go install module@tag" case: nothing was stamped, and the
	// tag survives only in the module's build information.
	for _, stamped := range []string{"", Development} {
		if got := resolve(stamped, buildInfo("v1.2.3")); got != "v1.2.3" {
			t.Fatalf("resolve(%q) = %q, want the module version v1.2.3", stamped, got)
		}
	}
}

func TestResolveReportsDevelopmentWhenNothingKnowsBetter(t *testing.T) {
	cases := map[string]func() (*debug.BuildInfo, bool){
		"a checkout build":        buildInfo("(devel)"),
		"an empty version":        buildInfo(""),
		"no build info at all":    func() (*debug.BuildInfo, bool) { return nil, false },
		"build info with no main": func() (*debug.BuildInfo, bool) { return &debug.BuildInfo{}, true },
	}
	for name, read := range cases {
		t.Run(name, func(t *testing.T) {
			if got := resolve("", read); got != Development {
				t.Fatalf("resolve = %q, want %q", got, Development)
			}
			if got := resolve(Development, read); got != Development {
				t.Fatalf("resolve = %q, want %q", got, Development)
			}
		})
	}
}

// TestResolveKeepsAMakefileDescription covers the local "make build" case: the
// Makefile stamps "git describe" output, which is not a tag and must still be
// preferred over the module version's "(devel)".
func TestResolveKeepsAMakefileDescription(t *testing.T) {
	if got := resolve("a1371f2-dirty", buildInfo("(devel)")); got != "a1371f2-dirty" {
		t.Fatalf("resolve = %q, want the stamped description a1371f2-dirty", got)
	}
}

func buildInfo(version string) func() (*debug.BuildInfo, bool) {
	return func() (*debug.BuildInfo, bool) {
		return &debug.BuildInfo{Main: debug.Module{Path: "github.com/mason-bryant/yoyodyne", Version: version}}, true
	}
}
