package cli

// Letting the harness choose work again, from where the operator is standing.
//
// Holding intake has always had two placers — the conversation's `/hold`, and
// the failure-storm brake, which places the operator's own switch when runs keep
// blocking — but until this verb existed it had one lifter, `/release`, inside a
// product-manager conversation. So an operator at a terminal met a hold the
// harness placed overnight, was told what had stopped, and was told a remedy
// they could not run from where they were reading it. An override that only
// exists behind a door you have to remember is not an override.
//
// This is that remedy as a command. It is the same switch the conversation lifts
// — one record under the product, read by every path that would start work the
// operator did not name — so which surface lifted it is not a fact the harness
// keeps, and lifting it here is indistinguishable afterwards from lifting it
// there.
//
// It is deliberately only the lifting half. Placing a hold is a decision with a
// reason attached, and the conversation is where that reason belongs; recovery
// is what has to work with no conversation open.

import (
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

type releaseOutput struct {
	// Hold is what was in force, whether or not this command lifted it. It is
	// the same shape the conversation reports, because it is the same record.
	Hold *runstate.IntakeHold `json:"hold,omitempty"`
	// Held reports what intake is now, and Lifted what this command did about
	// it. Releasing what was never held leaves intake exactly as the operator
	// wants it while having changed nothing, and a caller reading JSON has to be
	// able to tell that from the act itself.
	Held   bool   `json:"held"`
	Lifted bool   `json:"lifted,omitempty"`
	Error  string `json:"error,omitempty"`
}

func releaseIntake(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("release", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "", "configuration file path (default: the nearest project configuration)")
	jsonOutput := flags.Bool("json", false, "emit machine-readable JSON")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	// An operator who names the item they can see is one word from a different
	// verb, so the refusal says which word rather than only that this is not it.
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "release does not accept positional arguments: it lifts the hold on what the harness chooses, and `yoyo resume <beads-id>` is what releases one run's wait on the provider")
		printReleaseUsage(stderr)
		return 2
	}

	parts, err := buildComponents(*configPath)
	if err != nil {
		return reportReleaseError(stdout, stderr, *jsonOutput, err)
	}
	lifted, wasHeld, err := parts.intake.Release()
	if err != nil {
		return reportReleaseError(stdout, stderr, *jsonOutput, err)
	}
	if *jsonOutput {
		output := releaseOutput{Held: false, Lifted: wasHeld}
		if wasHeld {
			output.Hold = &lifted
		}
		return writeJSON(stdout, stderr, output)
	}
	if !wasHeld {
		fmt.Fprintln(stdout, "intake was not held; the harness may already choose work on its own and nothing changed")
		return 0
	}
	fmt.Fprintf(stdout, "released the hold on intake, held since %s; the harness may choose work from the backlog again\n",
		lifted.HeldAt.Format(time.RFC3339))
	if reason := strings.TrimSpace(lifted.Reason); reason != "" {
		fmt.Fprintln(stdout, reason)
	}
	fmt.Fprintln(stdout, "a watching `yoyo work` session chooses again at its next poll; a session that has ended is started again by `yoyo work`")
	fmt.Fprintln(stdout, "this is the same hold the conversation's /release lifts, so nothing else has to be done there")
	return 0
}

func reportReleaseError(stdout, stderr io.Writer, jsonOutput bool, err error) int {
	if jsonOutput {
		if code := writeJSON(stdout, stderr, releaseOutput{Error: err.Error()}); code != 0 {
			return code
		}
		return 1
	}
	fmt.Fprintln(stderr, err)
	return 1
}

func printReleaseUsage(writer io.Writer) {
	fmt.Fprintln(writer, `Usage: yoyo release [options]

Let the harness choose work from this product's backlog again, after intake was
held -- by you in a conversation, or by the failure-storm brake when runs kept
blocking with nothing landing between them. It is the terminal half of the
conversation's `+"`/release`"+`: one record under the product, so whichever
surface lifts it, the hold is gone for both.

Nothing else changes. Runs already going were never stopped by the hold, an item
you named with `+"`yoyo run`"+` was never subject to it, and releasing what is
not held is not an error -- you mean the harness to be choosing work, and it is.

A watching `+"`yoyo work`"+` session brakes rather than exits while intake is
held, so it starts choosing again at its next poll. A session that has already
ended is started again with `+"`yoyo work`"+`.

Holding intake is not here: it is a decision with a reason worth recording, and
`+"`/hold`"+` in `+"`yoyo chat`"+` is where that is said. This verb is the
recovery path that works with no conversation open.

Options:
  --config <path>   configuration file (default: the nearest .yoyodyne/config.yaml)
  --json            emit machine-readable JSON`)
}
