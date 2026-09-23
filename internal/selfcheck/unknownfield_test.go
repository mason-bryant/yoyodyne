package selfcheck

import (
	"strings"
	"testing"
)

// The run-record listings tolerate a key this build does not know, because what
// they read may have been written by a newer build. This block was written
// seconds ago by a developer this build told what to write, so a key this build
// does not know is a developer that recorded something other than the evidence
// it was asked for, and it stays refused. What the caller gets is an error
// naming the key rather than a record with an empty probe in it, which is the
// one answer a gate must never be handed.
func TestAVerificationBlockWithAKeyThisBuildDoesNotKnowIsRefusedAndNamed(t *testing.T) {
	t.Parallel()

	record, err := Decode(`{"probe":{"command":"make build","outcome":"passed"},"a_key_a_newer_build_added":["x"]}`)
	if err == nil {
		t.Fatalf("Decode() = %#v, want the key this build does not know refused", record)
	}
	if !strings.Contains(err.Error(), "a_key_a_newer_build_added") {
		t.Fatalf("Decode() error = %v, want the key named", err)
	}
	if record.Recorded() {
		t.Fatalf("Decode() = %#v, want nothing a gate could read beside the refusal", record)
	}
}
