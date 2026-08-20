package cli

// Running the Slack sink: the one process that reports what the harness is
// doing into a chat workspace.
//
// It is a verb an operator starts and leaves running, which is why it is a
// command of its own rather than something a run does on the side. That shape is
// the credential boundary: the two tokens belong to this process's environment
// and to nothing else, so no run — and therefore no agent's subprocess tree —
// ever has one. It is also what makes reporting an observation rather than a
// gate: this process failing, or never being started at all, changes nothing
// about any run.

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/notify"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/slack"
)

// The environment the sink reads its credentials from. They are deliberately
// the names Slack's own documentation uses, so the setup document can say
// "export the two tokens the app page shows you" and mean it literally.
const (
	botTokenVariable = "SLACK_BOT_TOKEN"
	appTokenVariable = "SLACK_APP_TOKEN"
)

func runSlack(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("slack", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "", "configuration file path (default: the nearest project configuration)")
	once := flags.Bool("once", false, "make one pass over the records and exit, rather than staying open")
	poll := flags.Duration("poll", slack.DefaultPollInterval, "how often to read the durable records")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "slack does not accept positional arguments")
		printSlackUsage(stderr)
		return 2
	}
	if *poll <= 0 {
		fmt.Fprintln(stderr, "poll must be positive")
		return 2
	}

	sink, channel, err := buildSlackSink(*configPath, *poll, stdout)
	if err != nil {
		fmt.Fprintf(stderr, "slack failed: %v\n", err)
		return 1
	}

	if *once {
		// One pass is what a setup document can tell somebody to run: it posts
		// whatever is already due, says what went wrong if anything did, and
		// leaves nothing running behind it.
		if err := sink.Once(ctx); err != nil {
			fmt.Fprintf(stderr, "slack failed: %v\n", err)
			return 1
		}
		return 0
	}

	fmt.Fprintf(stdout, "starting the Slack sink for %s; stop it with Ctrl-C\n", channel)
	if err := sink.Run(ctx); err != nil {
		fmt.Fprintf(stderr, "slack failed: %v\n", err)
		return 1
	}
	return 0
}

// buildSlackSink assembles the sink from the configuration, the state root, and
// the environment. It deliberately does not go through the run pipeline's
// components, for the reason the reporting verbs do not: the sink needs no
// repository, no worktree, and no process runner, and a process an operator
// leaves running must not refuse to start because of where their checkout
// happens to sit.
func buildSlackSink(configPath string, poll time.Duration, stdout io.Writer) (*slack.Sink, string, error) {
	resolved, err := loadConfiguration(configPath)
	if err != nil {
		return nil, "", err
	}
	settings := resolved.Config.Slack
	if !settings.Enabled {
		return nil, "", fmt.Errorf("slack reporting is not enabled in %s; set slack.enabled and slack.channel to turn it on", resolved.Path)
	}
	api, err := slackAPI()
	if err != nil {
		return nil, "", err
	}
	stateRoot, err := runstate.SystemDefaultRoot(os.Getenv, os.UserHomeDir)
	if err != nil {
		return nil, "", err
	}
	productID := resolved.Config.Product.ID
	runs, err := runstate.NewStore(stateRoot, productID)
	if err != nil {
		return nil, "", err
	}
	conversations, err := runstate.NewConversationStore(stateRoot, productID)
	if err != nil {
		return nil, "", err
	}
	reports, err := runstate.NewReportStore(stateRoot, productID)
	if err != nil {
		return nil, "", err
	}
	proposals, err := runstate.NewAmendmentStore(stateRoot, productID)
	if err != nil {
		return nil, "", err
	}
	intake, err := runstate.NewIntakeHoldStore(stateRoot, productID)
	if err != nil {
		return nil, "", err
	}
	holds, err := runstate.NewOperatorHoldStore(stateRoot)
	if err != nil {
		return nil, "", err
	}
	watch, err := runstate.NewWatchStore(stateRoot, productID)
	if err != nil {
		return nil, "", err
	}
	store, err := slack.NewStore(stateRoot, productID)
	if err != nil {
		return nil, "", err
	}
	// One window onto a process that otherwise runs silently, shared by the
	// reading and the posting so an operator watching it sees one account.
	log := func(format string, args ...any) {
		fmt.Fprintf(stdout, format+"\n", args...)
	}
	sink, err := slack.New(slack.Options{
		Channel: settings.Channel,
		// What the project configured is the picture beside each name and nothing
		// else about who is speaking; a speaker it named none for keeps the one
		// the harness ships.
		Avatars: notify.Avatars(settings.Avatars),
		Store:   store,
		API:     api,
		// The sink reports what happens from the moment somebody first pointed
		// one at this product. What was already over before then is in the
		// records the reporting verbs read, and a channel opened today does not
		// want a month of it arriving at once. That moment is recorded once, in
		// the sink's own state, rather than taken again on every start: a
		// watermark that moved forward with each restart would read past
		// everything filed while the sink was down.
		Feed: &slack.HarnessFeed{
			Runs:          runs,
			Conversations: conversations,
			Reports:       reports,
			Proposals:     proposals,
			Intake:        intake,
			Holds:         holds,
			Watch:         watch,
			Log:           log,
		},
		Poll: poll,
		Log:  log,
	})
	if err != nil {
		return nil, "", err
	}
	return sink, settings.Channel, nil
}

// slackAPI reads the two tokens. A missing one is refused here, before anything
// is read or posted, and the refusal names the variable rather than the value:
// nothing in this process ever prints a token, and a diagnostic that helpfully
// showed one would put it in a terminal, a scrollback, and whatever collects
// them.
func slackAPI() (*slack.API, error) {
	botToken := os.Getenv(botTokenVariable)
	appToken := os.Getenv(appTokenVariable)
	var missing []error
	if botToken == "" {
		missing = append(missing, fmt.Errorf("%s is not set", botTokenVariable))
	}
	if appToken == "" {
		missing = append(missing, fmt.Errorf("%s is not set", appTokenVariable))
	}
	if err := errors.Join(missing...); err != nil {
		return nil, fmt.Errorf("the Slack sink needs both tokens in its own environment: %w", err)
	}
	return slack.NewAPI(botToken, appToken)
}

func printSlackUsage(writer io.Writer) {
	fmt.Fprintln(writer, `Usage: yoyo slack [options]

Report what the harness is doing into the configured Slack channel: one thread
per work item, one message per milestone, and every report an agent files at the
severity it was filed under. The backlog moving is reported too — work admitted,
decomposed, attributed, or reordered in a conversation — so the queue changing is
as visible as the runs it feeds.

It reads the durable records and posts from them, so it is an observation rather
than a gate: nothing waits on it, a workspace that is down delays messages
rather than losing them, and a sink that has been away catches up from its own
cursors when it returns.

It needs both tokens in its own environment and takes them from nowhere else:

  export SLACK_BOT_TOKEN=xoxb-...
  export SLACK_APP_TOKEN=xapp-...

One sink per product. Two of them hold separate thread maps, so the second
opens its own threads and posts everything twice; the second to start is
refused. Setting it up from nothing is `+"`docs/slack/setup.md`"+`, and the app
manifest it asks for is beside it.

Options:
  --config <path>   configuration file (default: the nearest .yoyodyne/config.yaml)
  --once            make one pass over the records and exit
  --poll <d>        how often to read the durable records (default 15s)`)
}
