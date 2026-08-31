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
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/buildinfo"
	"github.com/mason-bryant/yoyodyne/internal/chat"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/notify"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/slack"
)

// The environment the sink reads its credentials from, named where the launch
// that fills it is: a sink reading one variable while supervision sets another
// is a sink that never starts, and one definition cannot drift from itself.
const (
	botTokenVariable = slack.BotTokenVariable
	appTokenVariable = slack.AppTokenVariable
)

func runSlack(ctx context.Context, args []string, stdout, stderr io.Writer, version string) int {
	// `ensure` is the sink supervised rather than run: it starts one if nothing
	// is reporting for this product and returns either way, which is what an
	// unattended pass can call every few minutes. Everything else here is the
	// sink itself, so the verb is looked for before the flags are.
	if len(args) > 0 && args[0] == "ensure" {
		return ensureSlackSink(ctx, args[1:], stdout, stderr)
	}
	flags := flag.NewFlagSet("slack", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "", "configuration file path (default: the nearest project configuration)")
	once := flags.Bool("once", false, "make one pass over the records and exit, rather than staying open")
	poll := flags.Duration("poll", slack.DefaultPollInterval, "how often to read the durable records")
	heartbeat := flags.Duration("heartbeat", slack.DefaultHeartbeat, "how often to say again that the line is choosing nothing over ready work")
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
	// There is no way to ask for no heartbeat at all, and that is deliberate: what
	// it would buy is silence that means waiting-on-you, which is the thing the
	// heartbeat exists to end. What an operator can change is how often.
	if *heartbeat <= 0 {
		fmt.Fprintln(stderr, "heartbeat must be positive; it is a cadence rather than a switch, because silence has to mean nothing to do")
		return 2
	}

	sink, channel, err := buildSlackSink(*configPath, *poll, *heartbeat, version, stdout)
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

// ensureSlackSink is the step a maintenance pass takes about reporting: this
// product's sink started if nothing is reporting for it, from this product's
// own stored tokens, and nothing done at all if something already is.
//
// It is one product per invocation, because a product is a configuration and a
// checkout rather than something a machine keeps a list of. A machine running
// several harnesses runs this in each of them, and the three things that keep
// those apart are all per product: the lease that answers whether a sink is
// running, the keychain items the tokens come from, and the state the sink
// writes. None of them can see a sibling's, so no pass ever double-starts a
// sink or starts one holding the wrong project's tokens.
//
// Nothing in the harness calls this on a schedule yet, and that is stated here
// rather than left to be discovered: the pass that is meant to — the
// productization of the operator's hand-rolled maintenance script, tracked as
// yoyodyne-ifd.207 — is not in this tree, so there is nothing here to wire the
// step into. Until it lands, the operator's own schedule calls it, and the
// hand-rolled Slack step it supersedes is still what runs on their machine.
func ensureSlackSink(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("slack ensure", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "", "configuration file path (default: the nearest project configuration)")
	jsonOutput := flags.Bool("json", false, "emit machine-readable JSON")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "slack ensure does not accept positional arguments")
		printSlackUsage(stderr)
		return 2
	}

	resolved, err := loadConfiguration(*configPath)
	if err != nil {
		fmt.Fprintf(stderr, "slack ensure failed: %v\n", err)
		return 1
	}
	productID := resolved.Config.Product.ID
	if !resolved.Config.Slack.Enabled {
		// A product that never asked for reporting is healthy with nothing to
		// supervise, so this is the ordinary answer rather than a failure: a pass
		// that ran over every product on the machine would otherwise report one
		// every time it passed a project that reports nowhere.
		return reportSupervision(stdout, stderr, *jsonOutput, slack.Supervision{
			Product: productID,
			Outcome: slack.OutcomeReportingOff,
			Detail:  "set slack.enabled and slack.channel in " + resolved.Path + " to turn it on",
		})
	}
	if runtime.GOOS != "darwin" {
		// The keychain is macOS's. Elsewhere the pair lives in a file this
		// project's own launch sources, and starting a sink from that is nothing
		// this has been asked to do -- so it says which arrangement it supports
		// rather than reporting a keychain that was never going to be there.
		fmt.Fprintf(stderr, "slack ensure reads this product's tokens from the macOS keychain, and this machine is %s\n", runtime.GOOS)
		fmt.Fprintf(stderr, "start the sink from the environment file instead: (set -a; . ~/.config/yoyo/%s/slack.env; %s=%s exec yoyo slack)\n",
			productID, slack.SecretNamespaceVariable, productID)
		return 1
	}

	stateRoot, err := runstate.SystemDefaultRoot(os.Getenv, os.UserHomeDir)
	if err != nil {
		fmt.Fprintf(stderr, "slack ensure failed: %v\n", err)
		return 1
	}
	store, err := slack.NewStore(stateRoot, productID)
	if err != nil {
		fmt.Fprintf(stderr, "slack ensure failed: %v\n", err)
		return 1
	}
	// The sink is started from the binary that is supervising it rather than
	// from whatever `yoyo` is on the pass's PATH, so the sink that comes back
	// after a stop is the build that noticed it was gone.
	program, err := os.Executable()
	if err != nil {
		fmt.Fprintf(stderr, "slack ensure failed: %v\n", err)
		return 1
	}
	supervision, err := slack.Supervisor{
		Store:    store,
		Secrets:  slack.Keychain{Runner: execution.OSProcessRunner{}},
		Launcher: slack.DetachedLauncher{},
		Program:  program,
		// The configuration this pass read, named to the sink rather than left
		// for it to find: a sink started by a pass run from somewhere else would
		// otherwise read whichever project is nearest that directory.
		Config:  resolved.Path,
		Dir:     config.ProjectDirectory(resolved.Path),
		Environ: os.Environ(),
	}.Ensure(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "slack ensure failed: %v\n", err)
		return 1
	}
	return reportSupervision(stdout, stderr, *jsonOutput, supervision)
}

// reportSupervision says what the pass did, and fails only where reporting is
// on and nothing is running: that is the one outcome somebody has to act on,
// and an unattended pass that exited 0 on it would be silence over a channel
// that has gone quiet.
func reportSupervision(stdout, stderr io.Writer, jsonOutput bool, supervision slack.Supervision) int {
	if jsonOutput {
		if code := writeJSON(stdout, stderr, supervision); code != 0 {
			return code
		}
	} else {
		fmt.Fprintln(stdout, describeSupervision(supervision))
	}
	if supervision.Outcome == slack.OutcomeSecretsUnavailable {
		return 1
	}
	return 0
}

// describeSupervision is the same account in a sentence. Nothing it prints is a
// credential: the secrets it names are the items, never their values.
func describeSupervision(supervision slack.Supervision) string {
	switch supervision.Outcome {
	case slack.OutcomeRunning:
		return fmt.Sprintf("a Slack sink is already reporting for %s; nothing was started", supervision.Product)
	case slack.OutcomeStarted:
		return fmt.Sprintf("started the Slack sink for %s as pid %d, logging to %s", supervision.Product, supervision.PID, supervision.Log)
	case slack.OutcomeSecretsUnavailable:
		// The store is named, because "the tokens could not be read" is the same
		// sentence for a pair nobody stored and a pair kept somewhere this does
		// not read — and only one of those is the operator's to do anything
		// about.
		return fmt.Sprintf("no Slack sink is reporting for %s and the keychain would not produce its tokens: %s\n%s\nrun `yoyo doctor` for what this machine has stored and the command that stores the rest",
			supervision.Product, strings.Join(supervision.Secrets, " and "), supervision.Detail)
	case slack.OutcomeReportingOff:
		return fmt.Sprintf("Slack reporting is off for %s, so there is no sink to run: %s", supervision.Product, supervision.Detail)
	}
	return fmt.Sprintf("the Slack sink for %s: %s", supervision.Product, supervision.Outcome)
}

// buildSlackSink assembles the sink from the configuration, the state root, and
// the environment. It deliberately does not go through the run pipeline's
// components, for the reason the reporting verbs do not: the sink needs no
// worktree and no pipeline, and a process an operator leaves running must not
// refuse to start because of what a run would need.
//
// It does need the tracker, for what the heartbeat says: how much admitted work
// is ready, which separates a line waiting on somebody from an honestly quiet
// one, and the queue itself, which is where the four lines get the refusal
// standing against each item. Both are asked at most once per heartbeat and
// never on the path of anything. A tracker that will not answer costs the sink
// that one message and nothing else — it is said in the sink's own log, or in
// the line that could not be read, and asked again later — so this is still a
// process that starts wherever the operator runs it.
func buildSlackSink(configPath string, poll, heartbeat time.Duration, version string, stdout io.Writer) (*slack.Sink, string, error) {
	resolved, err := loadConfiguration(configPath)
	if err != nil {
		return nil, "", err
	}
	repository, err := resolvePath(config.ProjectDirectory(resolved.Path), resolved.Config.Product.Repository)
	if err != nil {
		return nil, "", fmt.Errorf("resolve product repository: %w", err)
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
	usageLimits, err := runstate.NewUsageLimitStore(stateRoot, productID)
	if err != nil {
		return nil, "", err
	}
	store, err := slack.NewStore(stateRoot, productID)
	if err != nil {
		return nil, "", err
	}
	// The same product-scoped directive records `yoyo directive` writes and every
	// run consults. A directive recorded from a thread lands there rather than in
	// a second pile beside it, which is the whole of what makes a reply reach the
	// work exactly as a directive typed at a terminal does.
	directives, err := runstate.NewDirectiveStore(stateRoot, productID)
	if err != nil {
		return nil, "", err
	}
	// One window onto a process that otherwise runs silently, shared by the
	// reading and the posting so an operator watching it sees one account.
	log := func(format string, args ...any) {
		fmt.Fprintf(stdout, format+"\n", args...)
	}
	// One tracker for the two questions the sink asks it, neither of which is on
	// the path of anything: how much work is ready, and what an item nothing named
	// is called.
	tracker := beads.Client{Runner: execution.OSProcessRunner{}, Dir: repository}
	// The four lines, from the same derivation `yoyo status` prints them from,
	// assembled once and read by both halves of the sink: the heartbeat says them
	// while the line is stopped, and the inbound half says them to whoever asks
	// for them in the channel. Assembling them twice would be two readings of one
	// standing that could differ, which is the disagreement only the operator
	// could adjudicate. Every store here is one this sink already holds.
	standing := &readmodel.Sources{
		Runs:           runs,
		Conversations:  conversations,
		Tracker:        tracker,
		Directives:     directives,
		Amendments:     proposals,
		OperatorHolds:  holds,
		IntakeHolds:    intake,
		Sessions:       watch,
		Capacity:       resolved.Config.Execution.MaxConcurrentDevelopers,
		TrackerTimeout: chatTrackerTimeout,
	}
	sink, err := slack.New(slack.Options{
		Channel: settings.Channel,
		// What the project configured is the picture beside each name and nothing
		// else about who is speaking; a speaker it named none for keeps the one
		// the harness ships. Which product is talking goes after every speaker's
		// name, and comes from the store above rather than from anything named
		// again here — it is the id the configuration already carries, so an
		// operator running a second harness can tell the two apart without opening
		// a thread.
		Avatars: notify.Avatars(settings.Avatars),
		Store:   store,
		API:     api,
		// A thread is named from the record that opened it wherever that record
		// carried a name. Where it did not — an item whose first appearance in the
		// channel is its priority changing, which is every item admitted before
		// there was a channel — the tracker is asked, once, rather than the thread
		// being headed by an identifier somebody has to go and resolve.
		Titles: trackerTitles{tracker: tracker},
		// What this process is, for whoever asks later whether the sink reporting
		// for this product is the right one. The namespace is taken from the
		// environment rather than assumed to be this product: what it records is
		// whose secrets the launcher said it read, and a launcher that said
		// nothing has to read as nothing rather than as agreement.
		Identity: slack.Presence{
			Version: version,
			// And the revision that release was built from, which is what tells two
			// unreleased builds apart. A harness developing itself runs nothing but
			// those, so the version alone reports every sink as the installed one.
			Build:           buildinfo.Commit(),
			Config:          resolved.Path,
			SecretNamespace: os.Getenv(slack.SecretNamespaceVariable),
		},
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
			UsageLimits:   usageLimits,
			// A held or idle line with work ready to pull says so again while it
			// stands, because the message that said it began is hours stale by the
			// time somebody reads it and silence has to keep meaning nothing to do.
			Backlog: readyBacklog{tracker: tracker},
			// How far the binary a live watch session is running is behind what has
			// landed. A session that stays open runs whatever it was started with,
			// so a fix merged after it started is not in it until somebody restarts
			// it — and nothing else in the record says so while it goes on choosing
			// work and spending rounds against defects that are already dead.
			//
			// It is the product's repository, which is the harness's own source only
			// where the harness is the product — the self-hosting case this was
			// written for. Nothing here asserts that: the reading refuses a build
			// the repository does not hold, so a product that is somebody else's
			// gets silence rather than a count taken from an unrelated history.
			Deployments: repositoryDeployments{
				repository: repository,
				runner:     execution.OSProcessRunner{},
				timeout:    chatTrackerTimeout,
			},
			Heartbeat: heartbeat,
			// The four lines, from the same derivation `yoyo status` prints them
			// from. They are said with the heartbeat because that message is the one
			// somebody reads at three in the morning, and what it said before was
			// that choosing had stopped and nothing whatever about what the machine
			// was doing instead. Every store here is one this sink already holds,
			// wired into the read model rather than read a second way.
			Standing: standing,
			// What became of a directive somebody asked for in a thread, said in
			// that thread and addressed to them. The same records the inbound half
			// writes and reads: the product's directives, and the sink's own note of
			// which of them were said into a thread.
			Directives: directives,
			Steers:     store,
			Log:        log,
		},
		// The inbound half. Where a reply is recorded, and whose replies are acted
		// on: the humans this project granted direct-work who bound a Slack member
		// id, derived from the operators mapping rather than authored beside it. A
		// project that has granted nobody gets an empty list, which is a sink that
		// acknowledges every reply and acts on none.
		Directives: directives,
		Operators:  resolved.Config.SlackOperators(),
		// Who the project recognizes, which is the wider reading of the same
		// mapping: every human it names who bound a member id, and what it calls
		// them. A message from anybody else is a stranger's, and is told so once per
		// thread with the humans above named as who to reach out to instead.
		Recognized: resolved.Config.SlackMembers(),
		Contacts:   resolved.Config.OperatorNames(),
		// Where things stand, for the other half of the same question: a message
		// that addresses this app at the top of the channel is answered with the
		// four lines rather than with silence.
		Standing: standing,
		// And everything else somebody says to this app, which goes to the product
		// manager in the one durable conversation `yoyo chat` holds. The
		// configuration is named rather than a conversation handed over, because a
		// conversation is opened per message: see productManagerConversation.
		Conversation: productManagerConversation{configPath: configPath, log: log},
		Poll:         poll,
		Log:          log,
	})
	if err != nil {
		return nil, "", err
	}
	return sink, settings.Channel, nil
}

// productManagerConversation is the durable product-manager conversation as the
// Slack door reaches it: the same one `yoyo chat` opens, in the same state root,
// resumed from the same provider session. That sameness is the whole feature —
// a conversation begun at a terminal is answered from a channel and carried
// back — and it is had by opening the conversation exactly as `yoyo chat` does
// rather than by keeping anything about it here.
//
// It is opened per message and released as soon as the answer is in hand, which
// is two decisions rather than one:
//
// The conversation admits a single holder, so a sink that opened it at startup
// and kept it would hold it against the operator's own `yoyo chat` for as long as
// the sink ran — which is the exact seam ifd.130 records as the reason the
// harness could not reach the product manager while a console was open. A client
// that holds it only while it is talking cannot cause that.
//
// And the sink deliberately starts without the run pipeline's components, so a
// process an operator leaves running does not refuse to start because of what a
// run would need. Building them here, when somebody actually asks something,
// keeps that true: a machine that cannot open a conversation still reports, and
// says so in the thread of whoever asked rather than by never having started.
type productManagerConversation struct {
	configPath string
	log        func(format string, args ...any)
}

// Say opens the conversation, takes one message to it, and releases it again.
func (c productManagerConversation) Say(ctx context.Context, said string) (slack.Answer, error) {
	// The warnings openChat writes go into the sink's own log, which is the
	// operator's window onto this process; there is no terminal to write them to.
	session, lease, err := openChat(ctx, domain.RoleProductManager, "", c.configPath, false, logWriter{log: c.log})
	if err != nil {
		return slack.Answer{}, err
	}
	defer func() {
		if err := lease.Release(); err != nil {
			c.log("the product manager's conversation could not be released after a message from Slack, so a later one may be refused as held: %v", err)
		}
	}()
	return sayToConversation(ctx, session, said, c.log)
}

// slackCommandRefusal is what a command typed at this app gets. It says the
// thing this client does not do and the two places the same authority is
// actually exercised, because somebody who has just been told no is owed the
// next thing to try.
//
// It is a refusal rather than a dispatch on purpose. A command is the operator's
// own authority carried out by the harness — `/work` starts a run this process
// does not supervise, `/stop` ends one, `/refresh` retakes a picture — and
// carrying those out from a reporting sink would put a second driver of work
// beside the terminal. What matters is that it is refused rather than spoken:
// said to the product manager it would buy a confused answer and a turn the
// operator paid for, which is the defect `yoyo chat --message` already had once.
const slackCommandRefusal = "That is a command, and commands are not carried out from here — they are your own authority rather than anything the product manager can do. Type it at `yoyo chat`, or as `yoyo` at the terminal. Nothing was said to the product manager and no turn was spent."

// sayToConversation is what one message does to a conversation somebody else
// opened, which is the whole of what a client of it decides.
//
// Its two dispatches are `yoyo chat --message`'s own, and for its reason: an
// answer to a proposal this conversation is still waiting on is a decision the
// harness carries out rather than speech to say to an agent, and a conversation
// two clients dispatched differently would be one where an approval typed at a
// terminal and the same approval typed in a channel did different things.
//
// Where the two clients differ is the third: a terminal carries a command out
// and this client refuses it. A command does reach here — the message arrives
// mention-first, so `@yoyodyne /backlog` has its mention stripped and a leading
// slash again — and what it must not do is arrive at the product manager as
// prose. The rule that a slash is a command is asked of the chat package rather
// than restated, so the two clients cannot come to disagree about what one is.
func sayToConversation(ctx context.Context, session *chat.Session, said string, log func(format string, args ...any)) (slack.Answer, error) {
	evidence := session.Evidence()
	answer := slack.Answer{ConversationID: evidence.ConversationID, Turns: evidence.Turns}
	if chat.IsCommand(said) {
		// The harness's own answer, exactly as a decision below is: nothing said it,
		// no turn was spent, and the product manager was never asked.
		answer.Harness = true
		answer.Text = slackCommandRefusal
		return answer, nil
	}
	if outcomes, decided, err := session.Decide(ctx, said); decided {
		// A decision is the harness's own answer: no turn was spent, and the product
		// manager was never asked. What was decided travels back whether or not it
		// then failed, because a decision that was recorded happened.
		answer.Harness = true
		answer.Text = renderDecisions(outcomes)
		answer.Turns = session.Evidence().Turns
		return answer, err
	}
	// How old the conversation's picture of the product is. It is said in the log
	// rather than in the channel: it is a caveat on the answer for whoever is
	// watching this process, and a thread carrying one every time would bury the
	// answer somebody asked for.
	log("%s", session.Freshness(ctx))
	reply, err := session.Send(ctx, said)
	answer.Text = reply.Text
	answer.ConversationID = reply.Evidence.ConversationID
	answer.Turns = reply.Evidence.Turns
	// A turn that failed part way still answered part way, so what it said travels
	// with the failure rather than behind it — the door posts both.
	return answer, err
}

// logWriter turns a stream of warnings into lines in the sink's log. It is for
// the one caller that has warnings to pass on and no terminal to pass them to.
type logWriter struct {
	log func(format string, args ...any)
}

func (w logWriter) Write(payload []byte) (int, error) {
	for _, line := range strings.Split(strings.TrimRight(string(payload), "\n"), "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			w.log("%s", trimmed)
		}
	}
	return len(payload), nil
}

// readyBacklog is the tracker's own count of what is ready to pull, which is the
// whole of what the sink asks the backlog. It is a count rather than the items
// because which items they are is the scheduler's business and not a reporting
// process's, and it is the tracker's answer rather than a listing this filtered:
// readiness lives in the tracker's dependency graph, which is the same reason the
// scheduler asks for it rather than inferring it.
type readyBacklog struct {
	tracker beads.Client
}

// trackerTitles is what the tracker calls one item, which is the whole of what
// the sink asks it about any item at all. It is a title rather than the item
// because a header is a line: everything else an item holds is in the tracker,
// and the thread is what leads a reader there.
type trackerTitles struct {
	tracker beads.Client
}

func (t trackerTitles) Title(ctx context.Context, workItemID string) (string, error) {
	item, err := t.tracker.Show(ctx, workItemID)
	if err != nil {
		return "", err
	}
	return item.Title, nil
}

func (b readyBacklog) Ready(ctx context.Context) (int, error) {
	items, err := b.tracker.Ready(ctx)
	if err != nil {
		return 0, err
	}
	return len(items), nil
}

// repositoryDeployments answers how far one repository has moved past one build,
// which is what says whether the session choosing work is executing the harness
// that is deployed or one from before the last few fixes landed.
//
// It counts against the recorded revision rather than against a time, for the
// reason a conversation's freshness does: the question is what the repository
// holds that the build did not, and a commit's own date answers a different one.
// It is asked at most once per heartbeat and never on the path of anything.
//
// The repository it is given is the product's, and the build it is asked about is
// the revision the yoyodyne binary was built from. Those are one history in
// exactly one case — the harness developing itself, which is where the stall this
// exists for happened — and nothing about a product configuration makes it so.
// So the relationship is not assumed anywhere: it is asked of Git, once per
// comparison, and a build this repository has never held is reported as
// ErrUnrelatedBuild rather than counted. Counting anyway is the failure that
// matters, because "31 harness changes" derived from somebody else's history is a
// number an operator would act on.
type repositoryDeployments struct {
	repository string
	runner     execution.ProcessRunner
	timeout    time.Duration
}

// buildRevision is what a build a session recorded may look like before it is
// handed to Git.
var buildRevision = regexp.MustCompile(`^[a-f0-9]{7,64}$`)

func (d repositoryDeployments) Behind(ctx context.Context, build string) (int, error) {
	// The revision is a Git object name or nothing at all: the record it comes
	// from holds it to that, and this is the other side of the same boundary
	// rather than a second opinion about it. A record written by something that
	// did not is a comparison that is refused rather than an argument handed to
	// Git.
	if !buildRevision.MatchString(build) {
		return 0, fmt.Errorf("%q is not a revision the repository could be asked about", build)
	}
	// Whether this repository is the one that binary came out of, asked rather
	// than assumed. Git answering that it has no such commit is the whole of the
	// check: an object name is its own content, so a repository that holds it is
	// the repository that history is in.
	held, err := d.git(ctx, "cat-file", "-e", build+"^{commit}")
	if err != nil {
		return 0, err
	}
	if held.Status != execution.ProcessSucceeded {
		return 0, fmt.Errorf("%w: %s is not in %s", slack.ErrUnrelatedBuild, build, d.repository)
	}
	result, err := d.git(ctx, "rev-list", "--count", build+"..HEAD")
	if err != nil {
		return 0, err
	}
	if result.Status != execution.ProcessSucceeded {
		return 0, fmt.Errorf("git rev-list failed: %s", singleLine(firstNonEmpty(result.Stderr, result.Stdout)))
	}
	count, err := strconv.Atoi(strings.TrimSpace(result.Stdout))
	if err != nil {
		return 0, fmt.Errorf("git rev-list did not answer with a count: %s", singleLine(result.Stdout))
	}
	return count, nil
}

func (d repositoryDeployments) git(ctx context.Context, args ...string) (execution.ProcessResult, error) {
	result, err := d.runner.Run(ctx, execution.Command{
		Name:    "git",
		Args:    args,
		Dir:     d.repository,
		Timeout: d.timeout,
	}, nil)
	if err != nil {
		return execution.ProcessResult{}, fmt.Errorf("run Git command: %w", err)
	}
	return result, nil
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
       yoyo slack ensure [--config <path>] [--json]

Report what the harness is doing into the configured Slack channel: one thread
per work item, one message per milestone, and every report an agent files at the
severity it was filed under. The backlog moving is reported too — work admitted,
decomposed, attributed, or reordered in a conversation — so the queue changing is
as visible as the runs it feeds.

It reads the durable records and posts from them, so it is an observation rather
than a gate: nothing waits on it, a workspace that is down delays messages
rather than losing them, and a sink that has been away catches up from its own
cursors when it returns. Catching up is paced to what Slack keeps accepting, and
a backlog too deep to post one message at a time is said once per thread --
how much accumulated, over what span -- with the durable records holding the
whole of it. What is recent, and anything critical, is always said in full.

Replies go the other way. A reply in a work item's thread, from somebody this
project granted direct-work with a bound Slack member id, is recorded as a
directive against that item -- the same record `+"`yoyo directive record`"+` writes, with
the same pause semantics and the same resolution. Every reply is answered in its
thread with what was recorded or why nothing was, and a project that has granted
nobody is steered by nobody. What a reply may say is in `+"`docs/slack/setup.md`"+`.

One thing it says is a state rather than an event. A line that is choosing
nothing -- intake held, everything held, the watch session idle, or no session
running -- while the tracker still calls work ready says so every --heartbeat,
naming what stopped it, how long it has stood, and how much is waiting. It stops
the moment the state clears, and a line that is idle with nothing ready says
nothing at all: silence has to mean nothing to do rather than waiting on you.

It needs both tokens in its own environment and takes them from nowhere else:

  export SLACK_BOT_TOKEN=xoxb-...
  export SLACK_APP_TOKEN=xapp-...

Exported into a shell they are inherited by everything started from it, which on
a machine running more than one harness is how a sink ends up posting this
project's work through another project's Slack app. The supported form is a
launcher that reads this project's own stored pair into exactly one process and
says which project it read them for:

  YOYO_SLACK_SECRET_NAMESPACE=<product id> exec yoyo slack

That name is not a credential. It is what the sink records about whose secrets it
was launched with, so `+"`yoyo doctor`"+` can tell a sink that is merely running from
one that is running for this project.

One sink per product. Two of them hold separate thread maps, so the second
opens its own threads and posts everything twice; the second to start is
refused. Setting it up from nothing is `+"`docs/slack/setup.md`"+`, and the app
manifest it asks for is beside it.

Keeping one running is `+"`yoyo slack ensure`"+`, which is the launcher above done by
the harness rather than by a shell line:

  yoyo slack ensure

It starts a sink only if nothing is reporting for this product, reading this
product's own stored pair into that one process and nothing else. Whether a sink
is running is asked of this product's lease rather than of the process table, so
on a machine running more than one harness a sibling's sink is neither mistaken
for this one nor fought over. Run it from an unattended pass as often as you
like: with a sink already running it does nothing and says so, and it fails only
where reporting is on, nothing is running, and the stored tokens could not be
read.

Commands:
  ensure             start a sink for this product if none is running

Options:
  --config <path>    configuration file (default: the nearest .yoyodyne/config.yaml)
  --once             make one pass over the records and exit
  --poll <d>         how often to read the durable records (default 15s)
  --heartbeat <d>    how often to say again that the line is choosing nothing
                     over ready work (default 1h)`)
}
