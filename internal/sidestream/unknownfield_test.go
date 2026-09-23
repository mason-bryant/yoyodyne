package sidestream

import (
	"strings"
	"testing"
)

// The run-record listings tolerate a key this build does not know, because what
// they read may have been written by a newer build. This block was written
// seconds ago by a role this build told what to write, so a key this build does
// not know is a thread that answered something other than what it was asked, and
// it stays refused. The prose still comes back — the turn was spent on it — and
// the draft the main thread would have ratified does not.
func TestASideBlockWithAKeyThisBuildDoesNotKnowIsRefusedAndNamed(t *testing.T) {
	t.Parallel()

	reply, err := ReadReply("The item is not ready.\n\n" + Fence + `
{"side":{"concluded":true,"commitments":["say so"]},"a_key_a_newer_build_added":["x"]}
` + "```" + "\n")
	if err == nil {
		t.Fatalf("ReadReply() = %#v, want the key this build does not know refused", reply)
	}
	if !strings.Contains(err.Error(), "a_key_a_newer_build_added") {
		t.Fatalf("ReadReply() error = %v, want the key named", err)
	}
	if reply.Concluded || len(reply.Commitments) != 0 {
		t.Fatalf("ReadReply() = %#v, want nothing ratified off a block that was refused", reply)
	}
	if reply.Prose != "The item is not ready." {
		t.Fatalf("ReadReply().Prose = %q, want what the thread said kept", reply.Prose)
	}
}
