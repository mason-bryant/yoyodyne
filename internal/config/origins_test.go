package config

import (
	"fmt"
	"sort"
	"strings"
	"testing"
)

// Every value the effective configuration carries that the file did not state
// has to say where it came from. The set of harness defaults is enumerated from
// the resolved configuration itself rather than from a list kept beside the
// origins map, so a default added to newResolution without an origin fails here
// instead of reaching `config show --origins` as a value with no provenance line
// — which is how execution.usage_limit_unknown_reset_pause and
// execution.server_overload_pause shipped.
//
// A default equal to its type's zero value is indistinguishable from nothing
// being set, so this cannot see one; the defaults that matter here are the ones
// a project would otherwise have to go looking for, and those are not zero.
func TestEveryResolvedDefaultCarriesAnOrigin(t *testing.T) {
	t.Parallel()

	resolved, err := DecodeResolved(strings.NewReader(`version: 1
product:
  id: example
  repository: .
approvals:
  brief: human
  goals: human
  designs: automatic
  integration: human
agents:
  developer:
    role: developer
    backend: claude-code
    model: opus
`))
	if err != nil {
		t.Fatalf("DecodeResolved() error = %v", err)
	}

	// Keyed the way origins are keyed, by the same flattening the baseline
	// records values with, so a field added to the configuration is enumerated
	// here without anybody adding it to a list.
	effective := flattenConfig(resolved.Config)
	zero := flattenConfig(Config{})

	var missing []string
	checked := 0
	for key, value := range effective {
		if baseline, present := zero[key]; present && baseline == value {
			continue
		}
		checked++
		if originFor(resolved.Origins, key) == "" {
			missing = append(missing, fmt.Sprintf("%s = %v", key, value))
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Fatalf("effective values with no recorded origin:\n  %s", strings.Join(missing, "\n  "))
	}
	// The enumeration has to have found the defaults at all, or a change to how
	// the configuration marshals would pass this vacuously.
	for _, key := range []string{
		"execution.usage_limit_unknown_reset_pause",
		"execution.server_overload_pause",
		"execution.check_timeout",
	} {
		if _, found := effective[key]; !found {
			t.Fatalf("enumeration did not reach %q; checked %d values", key, checked)
		}
		if got := resolved.Origins[key]; got != OriginDefault {
			t.Errorf("origin[%q] = %q, want %q", key, got, OriginDefault)
		}
	}
}

// originFor returns the origin recorded for a key or for the nearest enclosing
// key: a value recorded as a whole — the accounts mapping, a replaced list — is
// the provenance of everything inside it.
func originFor(origins map[string]string, key string) string {
	for {
		if origin := origins[key]; origin != "" {
			return origin
		}
		cut := strings.LastIndexByte(key, '.')
		if cut < 0 {
			return ""
		}
		key = key[:cut]
	}
}
