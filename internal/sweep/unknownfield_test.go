package sweep

import (
	"strings"
	"testing"
)

// The run-record listings tolerate a key this build does not know, because the
// record on the other side of them was written by a build that may be newer than
// this one. Nothing on the other side of this block is: it was written seconds
// ago by a role this build told what to write. So a key this build does not know
// stays refused here, the refusal names the key, and the caller is handed an
// error rather than a sweep with part of an answer in it.
func TestASweepBlockWithAKeyThisBuildDoesNotKnowIsRefusedAndNamed(t *testing.T) {
	t.Parallel()

	result, err := Decode(`{"status":"complete","summary":"a quiet pass","a_key_a_newer_build_added":["x"]}`)
	if err == nil {
		t.Fatalf("Decode() = %#v, want the key this build does not know refused", result)
	}
	if !strings.Contains(err.Error(), "a_key_a_newer_build_added") {
		t.Fatalf("Decode() error = %v, want the key named", err)
	}
	if result != nil {
		t.Fatalf("Decode() = %#v, want nothing beside the refusal", result)
	}
}
