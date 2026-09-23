package selfcheck

import (
	"strings"
	"testing"
)

// The ordinary record: a probe and a check, both passing, out of a reply that
// also says something in prose. The prose comes back without the block, because
// the reply is the run's account of itself and this channel must not cost it.
func TestARecordIsReadOutOfAReplyWithoutCostingItsProse(t *testing.T) {
	t.Parallel()

	reply := "Implemented the gate and extended the suite.\n\n" + block(
		`{"probe":{"command":"make build","outcome":"passed"},"checks":[{"command":"make test ./internal/selfcheck/...","outcome":"passed"}]}`)

	rest, record, err := Extract(reply)
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if !record.Recorded() || !record.Probed() || !record.Evidenced() {
		t.Fatalf("record = %#v, want a probe and a passing check", record)
	}
	if record.Probe.Command != "make build" || len(record.Checks) != 1 {
		t.Fatalf("record = %#v, want the two commands as they were typed", record)
	}
	if strings.Contains(rest, Fence) {
		t.Errorf("the block is still in the account: %q", rest)
	}
	if !strings.Contains(rest, "Implemented the gate") {
		t.Errorf("the account lost the developer's prose: %q", rest)
	}
	if owed := record.Missing(true); len(owed) != 0 {
		t.Errorf("a full record still owes %v", owed)
	}
}

// A reply with no block at all is the case the whole gate exists for: it is not
// an error, and it is not an empty record either — it is a developer that
// recorded nothing, and the caller is the one that decides what that costs.
func TestAReplyWithNoBlockRecordsNothingAndIsNotAnError(t *testing.T) {
	t.Parallel()

	rest, record, err := Extract("I made the change and it looks right.")
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if record.Recorded() {
		t.Fatalf("record = %#v, want nothing recorded", record)
	}
	if rest != "I made the change and it looks right." {
		t.Errorf("rest = %q, want the reply unchanged", rest)
	}
	owed := record.Missing(true)
	if len(owed) != 2 {
		t.Fatalf("owed = %v, want both the probe and the check named", owed)
	}
	// And the same absence owes only the probe where nothing the checks read was
	// touched, because that is the whole of the leeway.
	if owed := record.Missing(false); len(owed) != 1 || !strings.Contains(owed[0], "probe") {
		t.Fatalf("owed = %v, want the probe alone", owed)
	}
}

// What a record may hold is exactly what the contract says, and everything else
// is refused where the untrusted text is read rather than carried further.
func TestARecordIsRefusedForEveryWayItCanBeWrong(t *testing.T) {
	t.Parallel()

	for _, refused := range []struct {
		name    string
		payload string
		says    string
	}{
		{"no probe", `{"checks":[{"command":"make test","outcome":"passed"}]}`, "command is required"},
		{"unknown outcome", `{"probe":{"command":"make build","outcome":"ran"}}`, `outcome "ran"`},
		{"a failure that says nothing", `{"probe":{"command":"make build","outcome":"failed"}}`, "detail is required"},
		{"a refusal that says nothing", `{"probe":{"command":"make build","outcome":"refused"}}`, "detail is required"},
		{"a field nobody defined", `{"probe":{"command":"make build","outcome":"passed"},"duration":"4s"}`, "unknown field"},
		{"trailing content", `{"probe":{"command":"make build","outcome":"passed"}} and one more thing`, "trailing content"},
		{"empty", "  ", "empty"},
	} {
		t.Run(refused.name, func(t *testing.T) {
			t.Parallel()
			if _, err := Decode(refused.payload); err == nil || !strings.Contains(err.Error(), refused.says) {
				t.Fatalf("Decode(%s) error = %v, want one naming %q", refused.payload, err, refused.says)
			}
		})
	}
}

// A record whose every check failed is not evidence that the change works. It is
// a change to repair, and the distinction matters because the harness answers
// the two differently: one is handed back with the failure, and the other is
// handed back for having recorded nothing at all.
func TestAFailedCheckIsRecordedAndIsNotEvidence(t *testing.T) {
	t.Parallel()

	record, err := Decode(`{"probe":{"command":"make build","outcome":"passed"},` +
		`"checks":[{"command":"make test","outcome":"failed","detail":"TestTheGate: want 2 owed, got 0"}]}`)
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if record.Evidenced() {
		t.Fatal("a record whose only check failed reads as evidence the change works")
	}
	if failures := record.Failures(); len(failures) != 1 || failures[0].Command != "make test" {
		t.Fatalf("failures = %#v, want the one failing check", failures)
	}
	if owed := record.Missing(true); len(owed) != 1 || !strings.Contains(owed[0], "every check you recorded") {
		t.Fatalf("owed = %v, want the failure named", owed)
	}
}

// A probe that could not start is the environment refusing, and it is recorded
// as such: the detail is required precisely so that whoever reads the run
// afterwards is told what refused, in the words the command itself used.
func TestARefusedProbeIsReadAsAnEnvironmentThatCannotExecute(t *testing.T) {
	t.Parallel()

	record, err := Decode(`{"probe":{"command":"make build","outcome":"refused",` +
		`"detail":"could not start /bin/zsh: argument list too long"}}`)
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if !record.Recorded() || record.Probed() || !record.ProbeRefused() {
		t.Fatalf("record = %#v, want a recorded probe that never started", record)
	}
	if described := record.Describe(); !strings.Contains(described, "argument list too long") ||
		!strings.Contains(described, "would not start") {
		t.Errorf("the description does not say the command never ran: %q", described)
	}
}

// And a probe that ran and failed is the opposite fact: the environment works,
// and something else — the commit the run was cut from, most often — is red. The
// two were one word in the first draft of this, which would have filed every red
// baseline as a broken sandbox.
func TestAProbeThatRanAndFailedStillProvesTheEnvironmentExecutes(t *testing.T) {
	t.Parallel()

	record, err := Decode(`{"probe":{"command":"make test","outcome":"failed",` +
		`"detail":"TestSomethingElse: want 2, got 3"}}`)
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if !record.Probed() || record.ProbeRefused() {
		t.Fatalf("record = %#v, want a probe that ran and proved the environment", record)
	}
	if !record.Probe.Started() || record.Probe.Passed() {
		t.Fatalf("probe = %#v, want an execution that started and did not pass", record.Probe)
	}
	if described := record.Describe(); !strings.Contains(described, "ran and failed") {
		t.Errorf("the description does not say the command ran: %q", described)
	}
	// It owes nothing more for the probe's sake: the probe was made, and what is
	// red is what the harness's own checks are about to say.
	if owed := record.Missing(false); len(owed) != 0 {
		t.Errorf("a probe that ran still owes %v", owed)
	}
}

// The description is what a reviewer and a work item's notes are both shown, so
// the record nobody made has to describe itself too — a reviewer shown only the
// records somebody wrote is shown nothing on exactly the runs this gate is for.
func TestTheDescriptionCoversTheRecordNobodyMade(t *testing.T) {
	t.Parallel()

	if described := (Record{}).Describe(); !strings.Contains(described, "no execution") {
		t.Fatalf("described = %q, want the absence said plainly", described)
	}
}

// The contract an agent is given names the checks this project actually
// declares, because a contract asking for a command nobody has is a contract
// asking for something that cannot be done.
func TestTheContractNamesTheProjectsOwnChecks(t *testing.T) {
	t.Parallel()

	contract := Contract([]string{"make test", "make vet"})
	for _, named := range []string{"make test", "make vet", Fence, `"refused"`, "the command's own message"} {
		if !strings.Contains(contract, named) {
			t.Errorf("the contract does not name %q", named)
		}
	}
	// And a project that declares none still gets a contract that asks for a
	// probe, rather than one with an empty list in it.
	bare := Contract(nil)
	if strings.Contains(bare, "declared checks are:") || !strings.Contains(bare, "declares no checks") {
		t.Errorf("the contract for a project with no checks reads wrong: %q", bare)
	}
}

func block(payload string) string {
	return Fence + "\n" + payload + "\n```\n"
}
