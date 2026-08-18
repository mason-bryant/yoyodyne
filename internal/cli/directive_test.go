package cli

import (
	"encoding/json"
	"strings"
	"testing"
)

// A directive given to an agent other than the product manager is still written
// down, and the record says which role heard it. That is what the design means
// by a directive applying regardless of which agent received it: the receiving
// role is attribution, and the record is the product's.
//
// The whole lifecycle runs here because the parts only mean anything together —
// a directive that is recorded and never lifted is as useless as one that is
// recorded and never enforced.
func TestDirectiveLifecycleFromTheCommandLine(t *testing.T) {
	// Not parallel: the state root every command addresses is set here, and the
	// records under it are what one command writes and the next reads.
	t.Setenv("YOYODYNE_STATE_HOME", t.TempDir())
	configPath := writeConfig(t, validConfig)

	stdout, stderr, code := runCLI(t, "directive", "record",
		"--config", configPath,
		"--kind", "ambiguous",
		"--received-by", "reviewer",
		"--unresolved", "which of the two publishing behaviours was meant",
		"do publishing differently")
	if code != 0 {
		t.Fatalf("record code = %d, stderr = %q", code, stderr)
	}
	// Recording a pausing directive says what it just stopped, because that is
	// the point of it rather than a side effect.
	for _, want := range []string{"by the reviewer", "which of the two publishing behaviours", "is paused"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("record stdout = %q, want it to mention %q", stdout, want)
		}
	}

	stdout, stderr, code = runCLI(t, "directive", "list", "--config", configPath, "--json")
	if code != 0 {
		t.Fatalf("list code = %d, stderr = %q", code, stderr)
	}
	var listed struct {
		Directives []struct {
			ID         string `json:"id"`
			Kind       string `json:"kind"`
			ReceivedBy string `json:"received_by"`
			Unresolved string `json:"unresolved"`
		} `json:"directives"`
	}
	if err := json.Unmarshal([]byte(stdout), &listed); err != nil {
		t.Fatalf("Unmarshal() error = %v over %q", err, stdout)
	}
	if len(listed.Directives) != 1 {
		t.Fatalf("listed = %#v, want the one unresolved directive", listed.Directives)
	}
	recorded := listed.Directives[0]
	if recorded.Kind != "ambiguous" || recorded.ReceivedBy != "reviewer" || recorded.Unresolved == "" {
		t.Fatalf("recorded = %#v", recorded)
	}

	// Nobody types thirty-two hex digits, so a prefix that names one directive is
	// an answer.
	prefix := recorded.ID[:len("directive-")+6]
	stdout, stderr, code = runCLI(t, "directive", "resolve", "--config", configPath,
		"--resolution", "the second behaviour was meant", prefix)
	if code != 0 {
		t.Fatalf("resolve code = %d, stderr = %q", code, stderr)
	}
	if !strings.Contains(stdout, "the second behaviour was meant") || !strings.Contains(stdout, "can carry on") {
		t.Fatalf("resolve stdout = %q", stdout)
	}

	// A settled directive holds nothing up, so it leaves the default listing.
	stdout, _, code = runCLI(t, "directive", "list", "--config", configPath)
	if code != 0 {
		t.Fatalf("list after resolve code = %d", code)
	}
	if !strings.Contains(stdout, "no unresolved directives") {
		t.Fatalf("list after resolve = %q, want the settled directive out of the default listing", stdout)
	}
	stdout, _, code = runCLI(t, "directive", "list", "--config", configPath, "--all")
	if code != 0 || !strings.Contains(stdout, recorded.ID) {
		t.Fatalf("list --all = %q (code %d), want the settled directive still readable", stdout, code)
	}
}

// Everything here would produce a directive nobody could act on or lift, so it
// is refused rather than recorded.
func TestDirectiveRefusesWhatCannotBeEnforcedOrLifted(t *testing.T) {
	t.Setenv("YOYODYNE_STATE_HOME", t.TempDir())
	configPath := writeConfig(t, validConfig)

	for name, args := range map[string][]string{
		"a pausing directive with nothing unresolved": {"directive", "record", "--config", configPath,
			"--kind", "ambiguous", "do it the other way"},
		"an artifact directive naming no artifact": {"directive", "record", "--config", configPath,
			"--kind", "artifact", "--unresolved", "whether it still holds", "the goal is changing"},
		"an operational directive claiming something unresolved": {"directive", "record", "--config", configPath,
			"--unresolved", "what was meant", "prefer smaller changes"},
		"a kind the harness does not know": {"directive", "record", "--config", configPath,
			"--kind", "urgent", "--unresolved", "what was meant", "do it now"},
		"a directive with nothing in it":  {"directive", "record", "--config", configPath, "   "},
		"resolving nothing":               {"directive", "resolve", "--config", configPath, "--resolution", "settled", "directive-missing"},
		"resolving without saying how":    {"directive", "resolve", "--config", configPath, "directive-missing"},
		"an operation the harness has no": {"directive", "retract", "--config", configPath, "directive-missing"},
	} {
		_, stderr, code := runCLI(t, args...)
		if code == 0 {
			t.Fatalf("%s: command succeeded", name)
		}
		if strings.TrimSpace(stderr) == "" {
			t.Fatalf("%s: refused without saying why", name)
		}
	}
}
