package config

import (
	"bytes"
	"strings"
	"testing"
)

// The baseline records what a bundle supplied, which is not the same set as
// what a project ends up holding. Three kinds of value are deliberately out of
// it, and each one would report a false improvement if it were in.
func TestABaselineRecordsWhatTheBundleSuppliedAndNothingElse(t *testing.T) {
	t.Parallel()

	lock, err := NewLock(BuiltinV1)
	if err != nil {
		t.Fatalf("NewLock() error = %v", err)
	}
	if lock.Bundle != BuiltinV1 || lock.Version != LockVersion {
		t.Fatalf("lock = %+v, want the built-in bundle at version %d", lock, LockVersion)
	}
	for _, key := range []string{"agents.developer.model", "agents.developer.role", "agents.developer.backend"} {
		if _, recorded := lock.Values[key]; !recorded {
			t.Errorf("the baseline records no %s, which the bundle states", key)
		}
	}
	// The persona is the value most worth telling a project about, and the one
	// the serialized configuration says least about.
	if digest := lock.Values["agents.developer.persona.text"]; !strings.HasPrefix(digest, "text-") {
		t.Errorf("persona text digest = %q, want one recorded as a digest", digest)
	}
	for key, why := range map[string]string{
		"version":                       "a version taken from a bundle would let a file written against another schema load",
		"extends":                       "extends is the statement of inheritance rather than something inherited",
		"checks":                        "checks describe the project's toolchain, which no bundle has a view of",
		"product.id":                    "a bundle that supplied product identity would name every project after itself",
		"product.specifications":        "the artifact homes are harness defaults rather than bundle values",
		"agents.developer.capabilities": "capabilities are read from the role registry and no layer supplies them",
		"agents.developer.account":      "an unstated account is derived from the effective accounts mapping",
		"triage.repair_grant_attempts":  "an unstated grant is derived from the effective repair budget",
	} {
		if value, recorded := lock.Values[key]; recorded {
			t.Errorf("the baseline records %s = %q, and it should not: %s", key, value, why)
		}
	}
}

// A baseline is written to be read back by another checkout, so the file and the
// record have to be the same thing.
func TestABaselineRoundTripsThroughTheFileItIsWrittenAs(t *testing.T) {
	t.Parallel()

	lock, err := NewLock(BuiltinV1)
	if err != nil {
		t.Fatalf("NewLock() error = %v", err)
	}
	read, err := DecodeLock(bytes.NewReader(lock.Render()))
	if err != nil {
		t.Fatalf("DecodeLock() error = %v", err)
	}
	if read.Bundle != lock.Bundle || read.Revision != lock.Revision || len(read.Values) != len(lock.Values) {
		t.Fatalf("read back %s/%s with %d values, want %s/%s with %d",
			read.Bundle, read.Revision, len(read.Values), lock.Bundle, lock.Revision, len(lock.Values))
	}
	for key, value := range lock.Values {
		if read.Values[key] != value {
			t.Errorf("read back %s = %q, want %q", key, read.Values[key], value)
		}
	}
	// The file says out loud that nothing loads it, because that property is the
	// one a later change is most likely to take away.
	if rendered := string(lock.Render()); !strings.Contains(rendered, "not read when the") {
		t.Errorf("the rendered baseline never says that nothing loads it:\n%s", rendered)
	}
}

// An edited baseline is not a baseline. Comparing against one would produce an
// answer that is confidently wrong, which is worse than having none.
func TestAnEditedBaselineIsRefusedRatherThanComparedAgainst(t *testing.T) {
	t.Parallel()

	lock, err := NewLock(BuiltinV1)
	if err != nil {
		t.Fatalf("NewLock() error = %v", err)
	}
	rendered := string(lock.Render())
	edited := strings.Replace(rendered, `agents.developer.model: "opus"`, `agents.developer.model: "haiku"`, 1)
	if edited == rendered {
		t.Fatal("the rendered baseline never stated the developer's model, so nothing was edited")
	}
	_, err = DecodeLock(strings.NewReader(edited))
	if err == nil {
		t.Fatal("DecodeLock() accepted a baseline whose revision does not digest its values")
	}
	if !strings.Contains(err.Error(), "yoyo init --force") {
		t.Errorf("DecodeLock() error = %v, want it to name the way back", err)
	}
}

func TestABaselineFromAnotherRecordVersionIsRefused(t *testing.T) {
	t.Parallel()

	_, err := DecodeLock(strings.NewReader("version: 99\nbundle: builtin:v1\nrevision: bnd-000000000000\nvalues: {}\n"))
	if err == nil || !strings.Contains(err.Error(), "version") {
		t.Fatalf("DecodeLock() error = %v, want a refusal naming the version", err)
	}
}

// The flattened form is what makes the baseline and a project's configuration
// comparable value by value, so it has to key them the same way `--origins` does
// and to keep a value readable.
func TestTheFlattenedFormKeysValuesTheWayOriginsDo(t *testing.T) {
	t.Parallel()

	resolved, err := DecodeResolved(strings.NewReader(`version: 1
extends: builtin:v1
product:
  id: flat
  repository: .
checks:
  - make test
`))
	if err != nil {
		t.Fatalf("DecodeResolved() error = %v", err)
	}
	values := flattenConfig(resolved.Config)
	if got := values["agents.developer.model"]; got != "opus" {
		t.Errorf("agents.developer.model = %q, want the value unquoted", got)
	}
	if got := values["checks"]; got != `["make test"]` {
		t.Errorf("checks = %q, want the list as one value", got)
	}
	// Every key the flattened form produces for a value origins also records has
	// to be spelled the same, or the two would never be compared.
	for key := range resolved.Origins {
		if _, flattened := values[key]; !flattened {
			continue
		}
		if strings.Contains(key, " ") {
			t.Errorf("origin key %q is not a key the flattened form could produce", key)
		}
	}
	if _, present := values["agents.developer.persona.path"]; !present {
		t.Error("the flattened form says nothing about a persona's path")
	}
}
