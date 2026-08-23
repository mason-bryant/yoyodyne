package cli

// Reading what the product manager made of the ideas the operator brought it.
//
// These commands only read. There is nothing here to approve and nothing to
// decline, deliberately: an evaluation is advice, and what becomes of advice is
// a work item or a document revision with its own record and its own approval.
// A verdict recorded against the advice itself would be a second place the same
// decision was written down, and the two would disagree the first time somebody
// changed their mind.

import (
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/evaluation"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

type evaluationOutput struct {
	Evaluations []evaluation.Evaluation `json:"evaluations,omitempty"`
	Error       string                  `json:"error,omitempty"`
}

func runEvaluation(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		printEvaluationUsage(stdout)
		return 0
	}
	switch args[0] {
	case "list":
		return listEvaluations(args[1:], stdout, stderr)
	case "show":
		return showEvaluation(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown evaluation command %q\n\n", args[0])
		printEvaluationUsage(stderr)
		return 2
	}
}

func listEvaluations(args []string, stdout, stderr io.Writer) int {
	flags := newEvaluationFlags("evaluation list", stderr)
	recommendation := flags.set.String("recommendation", "", "list only one recommendation: adopt, reject, defer, or experiment")
	if code, ok := flags.parse(args, 0); !ok {
		return code
	}
	store, code := flags.store(stderr)
	if code != 0 {
		return code
	}
	recorded, err := store.List()
	if err != nil {
		return reportEvaluationError(stdout, stderr, *flags.jsonOutput, err)
	}
	if named := strings.TrimSpace(*recommendation); named != "" {
		// A recommendation nothing recognizes is refused rather than filtered on.
		// An operator who mistyped it would otherwise read "nothing was evaluated"
		// as an answer about the record instead of about their typing.
		wanted := evaluation.Recommendation(named)
		if !wanted.Valid() {
			return reportEvaluationError(stdout, stderr, *flags.jsonOutput,
				fmt.Errorf("no recommendation %q exists; --recommendation is one of %s", named, strings.Join(recommendationNames(), ", ")))
		}
		recorded = recommending(recorded, wanted)
	}
	// Newest first: the question an operator asks a listing is what was decided
	// recently, and the oldest evaluation of an idea is the one most likely to
	// have been overtaken.
	sort.SliceStable(recorded, func(i, j int) bool { return recorded[i].RecordedAt.After(recorded[j].RecordedAt) })
	if *flags.jsonOutput {
		return writeJSON(stdout, stderr, evaluationOutput{Evaluations: recorded})
	}
	if len(recorded) == 0 {
		fmt.Fprintln(stdout, "no ideas have been evaluated for this product")
		return 0
	}
	for _, one := range recorded {
		fmt.Fprint(stdout, one.Render())
	}
	return 0
}

func showEvaluation(args []string, stdout, stderr io.Writer) int {
	flags := newEvaluationFlags("evaluation show", stderr)
	if code, ok := flags.parse(args, 1); !ok {
		return code
	}
	store, code := flags.store(stderr)
	if code != 0 {
		return code
	}
	found, exists, err := store.Find(flags.id())
	if err != nil {
		return reportEvaluationError(stdout, stderr, *flags.jsonOutput, err)
	}
	if !exists {
		return reportEvaluationError(stdout, stderr, *flags.jsonOutput,
			fmt.Errorf("no evaluation %q was recorded for this product", flags.id()))
	}
	if *flags.jsonOutput {
		return writeJSON(stdout, stderr, evaluationOutput{Evaluations: []evaluation.Evaluation{found}})
	}
	fmt.Fprint(stdout, found.Render())
	return 0
}

func recommending(recorded []evaluation.Evaluation, wanted evaluation.Recommendation) []evaluation.Evaluation {
	matched := make([]evaluation.Evaluation, 0, len(recorded))
	for _, one := range recorded {
		if one.Entry.Recommendation == wanted {
			matched = append(matched, one)
		}
	}
	return matched
}

// recommendationNames lists the recommendations a refusal names, derived from
// the package that defines them rather than listed again here: a second list is
// one a later recommendation could be added without.
func recommendationNames() []string {
	names := make([]string, 0, 4)
	for _, candidate := range []evaluation.Recommendation{
		evaluation.RecommendAdopt, evaluation.RecommendReject,
		evaluation.RecommendDefer, evaluation.RecommendExperiment,
	} {
		names = append(names, string(candidate))
	}
	return names
}

// evaluationFlags is the flag set every evaluation command shares: which
// configuration names the product whose record this is, and how to report the
// result.
type evaluationFlags struct {
	set        *flag.FlagSet
	name       string
	configPath *string
	jsonOutput *bool
	// args are the positional arguments, collected by parse rather than read off
	// the flag set, because the flags may come after them.
	args []string
}

func newEvaluationFlags(name string, stderr io.Writer) *evaluationFlags {
	set := flag.NewFlagSet(name, flag.ContinueOnError)
	set.SetOutput(stderr)
	return &evaluationFlags{
		set:        set,
		name:       name,
		configPath: set.String("config", "", "configuration file path (default: the nearest project configuration)"),
		jsonOutput: set.Bool("json", false, "emit machine-readable JSON"),
	}
}

func (f *evaluationFlags) parse(args []string, positional int) (int, bool) {
	parsed, err := parseArguments(f.set, args)
	if err != nil {
		return 2, false
	}
	f.args = parsed
	if len(f.args) != positional {
		if positional == 0 {
			fmt.Fprintf(f.set.Output(), "%s does not accept positional arguments\n", f.name)
		} else {
			fmt.Fprintf(f.set.Output(), "%s requires exactly one evaluation id\n", f.name)
		}
		printEvaluationUsage(f.set.Output())
		return 2, false
	}
	return 0, true
}

func (f *evaluationFlags) id() string {
	return argumentAt(f.args, 0)
}

// store opens the product's evaluation log, addressed the way every other
// durable record is: by the product the configuration names, in the state root
// they all share.
func (f *evaluationFlags) store(stderr io.Writer) (*runstate.EvaluationStore, int) {
	resolved, err := loadConfiguration(*f.configPath)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return nil, 1
	}
	stateRoot, err := runstate.SystemDefaultRoot(os.Getenv, os.UserHomeDir)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return nil, 1
	}
	store, err := runstate.NewEvaluationStore(stateRoot, resolved.Config.Product.ID)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return nil, 1
	}
	return store, 0
}

func reportEvaluationError(stdout, stderr io.Writer, jsonOutput bool, err error) int {
	if jsonOutput {
		if code := writeJSON(stdout, stderr, evaluationOutput{Error: err.Error()}); code != 0 {
			return code
		}
		return 1
	}
	fmt.Fprintln(stderr, err)
	return 1
}

func printEvaluationUsage(writer io.Writer) {
	fmt.Fprintln(writer, `Usage: yoyo evaluation <list|show> [options]

An operator brings the product manager an idea rather than a work item — "what
if we did X", "is Y worth it" — and what it makes of one is written down here:
the recommendation, how the idea sits against the brief and the goals, what the
evidence states and where that came from, what was inferred rather than read,
what is still uncertain, what argues the other way, and the reasoning.

Everything here is advice. Recording an evaluation admitted no work, changed no
document, and approved nothing, and nothing in these commands does either. What
became of the advice is elsewhere and has its own record: work in the backlog,
which reached it through the approval your project asks for, and a change to a
document, which reached it through that document's owner and `+"`yoyo amendment`"+`.

Each evaluation also keeps what the harness actually retrieved for it and when,
beside the sources the product manager cited. The two are different claims and
they are kept apart on purpose: one is what it says it read, the other is what
was fetched.

  list [--recommendation <adopt|reject|defer|experiment>]   what has been evaluated, newest first
  show <id>                                                 one evaluation in full

Options:
  --config <path>       configuration file (default: the nearest .yoyodyne/config.yaml)
  --json                emit machine-readable JSON`)
}
