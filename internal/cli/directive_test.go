package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/directive"
	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// The whole of what an operator does with a directive from a terminal: see that
// nothing is directed, record one that pauses work, find it in the listing, lift
// it by a prefix of its identifier, and find the record still there afterwards.
//
// It goes through the commands rather than the store, because what this is about
// is the command line: the store has its own tests, and a lifecycle that only
// ever ran through the store would pass with the commands wired to nothing.
func TestDirectiveLifecycleFromTheCommandLine(t *testing.T) {
	// Not parallel: the state root the commands resolve is set here.
	t.Setenv("YOYODYNE_STATE_HOME", t.TempDir())
	configPath := writeConfig(t, validConfig)

	if empty := runDirectiveOK(t, configPath, "list"); !strings.Contains(empty, "no directives are in force") {
		t.Fatalf("listing = %q, want a product nobody has directed to say so", empty)
	}

	id := recordFromCommandLine(t, configPath,
		"--kind", "ambiguous",
		"--unresolved", "which of the two publishing behaviours was meant",
		"do publishing differently")

	// A directive that pauses work is enforced rather than filed, so the command
	// says what it just stopped rather than leaving the operator to notice work
	// going quiet.
	listed := runDirectiveOK(t, configPath, "list")
	for _, wanted := range []string{id, "do publishing differently", "which of the two publishing behaviours was meant"} {
		if !strings.Contains(listed, wanted) {
			t.Fatalf("listing = %q, want it to mention %q", listed, wanted)
		}
	}

	// An identifier may be shortened to any prefix that names exactly one, which
	// is the only way anybody settles one out of a listing.
	settled := runDirectiveOK(t, configPath, "resolve",
		"--resolution", "the second behaviour, and the design says so",
		id[:len("directive-")+6])
	for _, wanted := range []string{"resolved", "the second behaviour, and the design says so", "can carry on"} {
		if !strings.Contains(settled, wanted) {
			t.Fatalf("resolution = %q, want it to mention %q", settled, wanted)
		}
	}

	// The pause is lifted, so it is no longer what the operator is shown when
	// they ask what still applies.
	after := runDirectiveOK(t, configPath, "list")
	if strings.Contains(after, id) || !strings.Contains(after, "no directives are in force") {
		t.Fatalf("listing = %q, want a lifted pause out of what is in force", after)
	}
	// And the record of it is still there in full, which is what --all is for.
	everything := runDirectiveOK(t, configPath, "list", "--all")
	if !strings.Contains(everything, id) || !strings.Contains(everything, "resolved") {
		t.Fatalf("listing = %q, want the settled directive and how it was settled", everything)
	}
}

// The options are read wherever they were typed. `directive record --kind
// ambiguous "..."` and `directive record "..." --kind ambiguous` are the same
// command, because nobody remembers which order a flag set wants and a directive
// silently recorded as the wrong kind is one that pauses nothing it should have.
func TestDirectiveOptionsAreReadAfterTheArgument(t *testing.T) {
	// Not parallel: the state root the commands resolve is set here.
	t.Setenv("YOYODYNE_STATE_HOME", t.TempDir())
	configPath := writeConfig(t, validConfig)

	id := recordFromCommandLine(t, configPath,
		"do publishing differently",
		"--kind", "ambiguous",
		"--unresolved", "which of the two publishing behaviours was meant",
		"--received-by", "reviewer",
		"--scope", "yoyodyne-ifd.1,yoyodyne-ifd.2")

	parts, err := buildComponents(configPath)
	if err != nil {
		t.Fatalf("buildComponents() error = %v", err)
	}
	recorded, err := parts.directives.Load(id)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if recorded.Kind != directive.KindAmbiguous || !recorded.Pauses() {
		t.Fatalf("recorded = %#v, want the kind the flag named, pausing the work it affects", recorded)
	}
	if recorded.ReceivedBy != domain.RoleReviewer {
		t.Fatalf("received by = %q, want the role the flag named", recorded.ReceivedBy)
	}
	if got := strings.Join(recorded.Scope, ","); got != "yoyodyne-ifd.1,yoyodyne-ifd.2" {
		t.Fatalf("scope = %q, want the items the flag named", got)
	}
	if recorded.Text != "do publishing differently" || recorded.Unresolved != "which of the two publishing behaviours was meant" {
		t.Fatalf("recorded = %#v, want the text and what it left unresolved", recorded)
	}
}

// What the command line will not record, and will not settle. Every one of these
// is refused with nothing written down: a directive that pauses work without
// saying what it is waiting for is a pause nobody can lift, and one recorded
// under a kind or an artifact the harness cannot read is a record that enforces
// something nobody stated.
func TestDirectiveRefusesWhatCannotBeEnforcedOrLifted(t *testing.T) {
	// Not parallel: the state root the commands resolve is set here.
	t.Setenv("YOYODYNE_STATE_HOME", t.TempDir())
	configPath := writeConfig(t, validConfig)

	// One recorded directive, so the refusals about settling one are refusals
	// about how it was settled rather than about it not being there.
	pausing := recordFromCommandLine(t, configPath,
		"--kind", "ambiguous",
		"--unresolved", "which of the two publishing behaviours was meant",
		"do publishing differently")
	// And one that pauses nothing, which is settled by being carried out rather
	// than by being resolved.
	inForce := recordFromCommandLine(t, configPath, "stop opening pull requests for documentation-only changes")

	tests := []struct {
		name string
		args []string
		code int
		want string
	}{
		{
			name: "a pausing directive must say what it is waiting for",
			args: []string{"record", "--kind", "ambiguous", "do publishing differently"},
			code: 1,
			want: "unresolved is required",
		},
		{
			name: "an artifact directive must name the artifact it changes",
			args: []string{"record", "--kind", "artifact", "--unresolved", "whether the goal still covers this", "the autonomy goal is being rewritten"},
			code: 1,
			want: "artifact is required",
		},
		{
			name: "an operational directive has nothing unresolved",
			args: []string{"record", "--unresolved", "what was meant", "stop opening pull requests"},
			code: 1,
			want: "in effect already",
		},
		{
			name: "a kind the harness does not know",
			args: []string{"record", "--kind", "urgent", "stop opening pull requests"},
			code: 1,
			want: "is not a directive kind",
		},
		{
			name: "a directive with nothing in it",
			args: []string{"record", ""},
			code: 1,
			want: "text is required",
		},
		{
			name: "recording without saying anything at all",
			args: []string{"record"},
			code: 2,
			want: "requires exactly one argument",
		},
		{
			name: "settling a directive nobody recorded",
			args: []string{"resolve", "--resolution", "settled elsewhere", "directive-" + strings.Repeat("0", 32)},
			code: 1,
			want: "no directive is recorded under that reference",
		},
		{
			name: "settling without saying how",
			args: []string{"resolve", "--resolution", "", pausing},
			code: 1,
			want: "say how the directive was settled",
		},
		{
			name: "settling nothing in particular",
			args: []string{"resolve", "--resolution", "settled"},
			code: 2,
			want: "requires exactly one directive id",
		},
		{
			// An operational directive took effect when it was recorded and has
			// nothing to resolve. What came of it is recorded as an outcome, by the
			// admission that answers it, rather than settled from here.
			name: "resolving a directive that pauses nothing",
			args: []string{"resolve", "--resolution", "I did it", inForce},
			code: 1,
			want: "has nothing to resolve",
		},
		{
			name: "a subcommand the harness does not have",
			args: []string{"wibble"},
			code: 2,
			want: "unknown directive command",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var stdout, stderr strings.Builder
			code := runDirective(append(test.args, "--config", configPath), &stdout, &stderr)
			if code != test.code {
				t.Fatalf("exited %d, want %d; stdout=%q stderr=%q", code, test.code, stdout.String(), stderr.String())
			}
			if !strings.Contains(stderr.String(), test.want) {
				t.Fatalf("stderr = %q, want it to say %q", stderr.String(), test.want)
			}
		})
	}
}

// The default listing is the one place an operator asks what direction still
// applies, and an operational directive somebody carried out still applies: it
// took effect when it was recorded, it held nothing up, and recording what came
// of it says what it produced rather than withdrawing it.
//
// This is the regression the outcome half could have introduced. Before an
// operational directive could carry an outcome at all it was permanently
// unsettled, so filtering this listing on "nothing has been recorded about it"
// happened to be right; the moment one could be settled, that filter would drop
// a standing instruction out of the listing the operator reads to find it. The
// directive that pauses work is the only one settling takes out of force, and
// this holds both halves apart at the surface.
func TestTheDefaultDirectiveListingKeepsAStandingInstructionAfterItIsCarriedOut(t *testing.T) {
	// Not parallel: the state root the commands resolve is set here.
	t.Setenv("YOYODYNE_STATE_HOME", t.TempDir())
	configPath := writeConfig(t, validConfig)

	standing := recordFromCommandLine(t, configPath, "stop opening pull requests for documentation-only changes")
	lifted := recordFromCommandLine(t, configPath,
		"--kind", "ambiguous",
		"--unresolved", "which of the two publishing behaviours was meant",
		"do publishing differently")

	// Carrying one out is the admission's act rather than a command of its own,
	// so this reaches the same store the product manager writes to.
	parts, err := buildComponents(configPath)
	if err != nil {
		t.Fatalf("buildComponents() error = %v", err)
	}
	if _, err := parts.directives.CarryOut(standing, "admitted yoyodyne-ifd.170 to the backlog: make it configurable", time.Now()); err != nil {
		t.Fatalf("CarryOut() error = %v", err)
	}
	if _, err := parts.directives.Resolve(lifted, "the second behaviour", time.Now()); err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}

	listed := runDirectiveOK(t, configPath, "list")
	// Still in force, and now saying what it produced as well.
	if !strings.Contains(listed, standing) {
		t.Fatalf("listing = %q, want the carried-out instruction still listed as in force", listed)
	}
	if !strings.Contains(listed, "carried out") || !strings.Contains(listed, "yoyodyne-ifd.170") {
		t.Fatalf("listing = %q, want it to say what the instruction produced", listed)
	}
	// The pause it lifted is over, so it is not what the operator is shown by
	// default.
	if strings.Contains(listed, lifted) {
		t.Fatalf("listing = %q, want a resolved pause out of the default listing", listed)
	}
	// And --all is still the whole record, both of them.
	everything := runDirectiveOK(t, configPath, "list", "--all")
	for _, wanted := range []string{standing, lifted} {
		if !strings.Contains(everything, wanted) {
			t.Fatalf("listing = %q, want every recorded directive", everything)
		}
	}
}

// Withdrawing is the operator taking a directive back, and it is the only thing
// that ends one that pauses nothing. This is the case it was built for: a
// question the inbound machinery read as an instruction, in force from the
// moment it was written down, listed as live direction and met by every run that
// read it, with no verb anywhere that could take it out again.
//
// What it must do is end it without deleting it. An operator who withdrew a
// directive and found their own words gone would have no way to explain the runs
// that were held or judged while it stood.
func TestWithdrawingADirectiveFromTheCommandLineEndsItWithoutDeletingIt(t *testing.T) {
	// Not parallel: the state root the commands resolve is set here.
	t.Setenv("YOYODYNE_STATE_HOME", t.TempDir())
	configPath := writeConfig(t, validConfig)

	miscategorized := recordFromCommandLine(t, configPath, "--scope", "yoyodyne-ifd.194", "Is this still running?")
	if listed := runDirectiveOK(t, configPath, "list"); !strings.Contains(listed, miscategorized) {
		t.Fatalf("listing = %q, want the recorded directive in force before it is withdrawn", listed)
	}

	// By a prefix, which is how anybody names one out of a listing.
	withdrawn := runDirectiveOK(t, configPath, "withdraw",
		"--by", "Mason, at a terminal",
		"--reason", "recorded in error: this was a question about a run, not an instruction",
		miscategorized[:len("directive-")+6])
	for _, wanted := range []string{
		"withdrawn",
		"no longer in force",
		"Mason, at a terminal",
		"recorded in error: this was a question about a run, not an instruction",
		"nothing was deleted",
	} {
		if !strings.Contains(withdrawn, wanted) {
			t.Fatalf("withdrawal = %q, want it to mention %q", withdrawn, wanted)
		}
	}

	// Out of what still applies, which is the listing the operator reads to find
	// out what the harness is holding itself to.
	after := runDirectiveOK(t, configPath, "list")
	if strings.Contains(after, miscategorized) || !strings.Contains(after, "no directives are in force") {
		t.Fatalf("listing = %q, want a withdrawn directive out of what is in force", after)
	}
	// And still there in full, reading as withdrawn rather than as a record
	// somebody removed.
	everything := runDirectiveOK(t, configPath, "list", "--all")
	for _, wanted := range []string{miscategorized, "Is this still running?", "withdrawn"} {
		if !strings.Contains(everything, wanted) {
			t.Fatalf("listing = %q, want the withdrawn directive kept in full and marked withdrawn", everything)
		}
	}
}

// What the command line will not withdraw. Each of these would leave the record
// unable to say why a directive stopped applying, or would take back something
// that has already ended.
func TestWithdrawRefusesWhatWouldLeaveTheRecordUnanswerable(t *testing.T) {
	// Not parallel: the state root the commands resolve is set here.
	t.Setenv("YOYODYNE_STATE_HOME", t.TempDir())
	configPath := writeConfig(t, validConfig)

	standing := recordFromCommandLine(t, configPath, "stop opening pull requests for documentation-only changes")
	pausing := recordFromCommandLine(t, configPath,
		"--kind", "ambiguous",
		"--unresolved", "which of the two publishing behaviours was meant",
		"do publishing differently")
	runDirectiveOK(t, configPath, "resolve", "--resolution", "the second behaviour", pausing)

	tests := []struct {
		name string
		args []string
		code int
		want string
	}{
		{
			name: "withdrawing without saying why",
			args: []string{"withdraw", "--by", "Mason, at a terminal", "--reason", "", standing},
			code: 1,
			want: "say why the directive is withdrawn",
		},
		{
			// Who is asked for rather than assumed: agents run this binary too, and a
			// default would put the operator's name on a withdrawal an agent made.
			name: "withdrawing without saying who",
			args: []string{"withdraw", "--reason", "we do it the other way now", standing},
			code: 1,
			want: "say who is withdrawing it",
		},
		{
			name: "withdrawing with an empty who",
			args: []string{"withdraw", "--reason", "we do it the other way now", "--by", "  ", standing},
			code: 1,
			want: "say who is withdrawing it",
		},
		{
			name: "withdrawing a directive nobody recorded",
			args: []string{"withdraw", "--by", "Mason, at a terminal", "--reason", "recorded in error", "directive-" + strings.Repeat("0", 32)},
			code: 1,
			want: "no directive is recorded under that reference",
		},
		{
			name: "withdrawing nothing in particular",
			args: []string{"withdraw", "--by", "Mason, at a terminal", "--reason", "recorded in error"},
			code: 2,
			want: "requires exactly one directive id",
		},
		{
			// The pause is already lifted and the work it held has already carried
			// on, on the strength of the answer.
			name: "withdrawing a directive that has already ended",
			args: []string{"withdraw", "--by", "Mason, at a terminal", "--reason", "never mind", pausing},
			code: 1,
			want: "no longer in force",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var stdout, stderr strings.Builder
			code := runDirective(append(test.args, "--config", configPath), &stdout, &stderr)
			if code != test.code {
				t.Fatalf("exited %d, want %d; stdout=%q stderr=%q", code, test.code, stdout.String(), stderr.String())
			}
			if !strings.Contains(stderr.String(), test.want) {
				t.Fatalf("stderr = %q, want it to say %q", stderr.String(), test.want)
			}
		})
	}
	// Every refusal above wrote nothing: the standing instruction is untouched.
	if listed := runDirectiveOK(t, configPath, "list"); !strings.Contains(listed, standing) {
		t.Fatalf("listing = %q, want the refused withdrawals to have changed nothing", listed)
	}
}

// runDirectiveOK runs one directive command that is expected to succeed and
// returns what it printed, failing with what the command said when it does not.
func runDirectiveOK(t *testing.T, configPath string, args ...string) string {
	t.Helper()
	var stdout, stderr strings.Builder
	if code := runDirective(append(args, "--config", configPath), &stdout, &stderr); code != 0 {
		t.Fatalf("yoyo directive %s exited %d: %s", strings.Join(args, " "), code, stderr.String())
	}
	return stdout.String()
}

// recordFromCommandLine records one directive through the command an operator
// types and returns the identifier the record was given.
func recordFromCommandLine(t *testing.T, configPath string, args ...string) string {
	t.Helper()
	return directiveIDFrom(t, runDirectiveOK(t, configPath, append([]string{"record"}, args...)...))
}

// directiveIDFrom reads the identifier out of what a command printed, which is
// how an operator gets one: a rendered record opens with it in brackets.
func directiveIDFrom(t *testing.T, output string) string {
	t.Helper()
	opens := strings.Index(output, "[")
	shuts := strings.Index(output, "]")
	if opens < 0 || shuts < opens {
		t.Fatalf("output = %q, want it to name the directive it recorded", output)
	}
	id := output[opens+1 : shuts]
	if !directive.ValidID(id) {
		t.Fatalf("id = %q, want the identifier the record was given", id)
	}
	return id
}
