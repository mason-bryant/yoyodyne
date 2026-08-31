package slack

// Starting the sink nobody is running, which is the one thing an unattended
// pass can do about reporting without waking anybody.
//
// What it performs is the arrangement the setup document writes out by hand:
// this product's own token pair, read out of the store only its own launch
// looks in, into exactly one process's environment. Doing that here rather than
// in a shell line is what makes it survive a machine running more than one
// product, and both halves of that are things a hand-rolled pass gets wrong:
//
//   - It asks the process table whether a sink is running, and `pgrep -f "yoyo
//     slack"` matches the sibling product's sink. The second product is then
//     never started, because the first one's process answered for it. The
//     question asked here is this product's lease — the same one the sink
//     itself takes, and one per product — so a sibling's sink is neither seen
//     nor fought over.
//   - It names one product's keychain items in a line that runs for every
//     product, so every sink it starts holds the first product's tokens. The
//     names here come from the product being supervised and from nothing else.
//
// No token reaches anything that keeps it. It exists in this process for as
// long as it takes to put it in the child's environment, and nothing that is
// printed, logged, or recorded — the outcome below included — carries one.

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// KeychainAccount is the account this product's two Slack secrets are stored
// under on macOS. It is fixed because the distinguishing part is the service
// name, which carries the product.
const KeychainAccount = "yoyo"

// BotTokenVariable and AppTokenVariable are the environment the sink reads its
// credentials from. They are deliberately the names Slack's own documentation
// uses, so the setup document can say "export the two tokens the app page shows
// you" and mean it literally.
const (
	BotTokenVariable = "SLACK_BOT_TOKEN"
	AppTokenVariable = "SLACK_APP_TOKEN"
)

// sinkLogFile is where a sink started by supervision says what it is doing. It
// sits beside that sink's own state, so it is one log per product rather than
// one file every product on the machine appends to.
const sinkLogFile = "sink.log"

// DefaultSecretTimeout bounds asking the store for one token. The keychain
// answers immediately or refuses; a bound is here so that a pass which runs
// unattended cannot be the thing that waits forever.
const DefaultSecretTimeout = 15 * time.Second

// ErrSecretUnavailable is a token the store would not produce: never stored,
// or stored and refused. Both are the operator's to settle and neither is a
// fault of the pass that asked, so they are one sentinel rather than a failure.
var ErrSecretUnavailable = errors.New("the stored secret could not be read")

// SecretReader produces one stored secret by name. It is an interface because
// where the pair lives is the machine's business — a keychain here, something
// else elsewhere — while what supervision does with them is the same either way.
type SecretReader interface {
	Secret(ctx context.Context, name string) (string, error)
}

// Launch is one sink process, described in full. Env is the whole environment
// the process gets rather than an addition to one, because the tokens in it are
// the reason this type exists: an environment assembled by inheritance is an
// environment whose contents nobody decided.
type Launch struct {
	Program string
	Args    []string
	Dir     string
	Env     []string
	// LogRoot is the root the sink's output is confined to and Log names the
	// file inside it. They are a root and a path within it rather than one
	// absolute name because that is what a confined write is: containment is
	// decided against the filesystem below the root, so no symlink along Log can
	// put a running sink's output outside it.
	LogRoot string
	Log     string
}

// Launcher starts the sink and leaves it running, reporting the process it
// started. It outlives whatever asked for it — a launchd job, a terminal, a
// cron line — because reporting has to still be there afterwards.
type Launcher interface {
	Launch(Launch) (int, error)
}

// Outcome is what one pass found and did.
type Outcome string

const (
	// OutcomeRunning is a sink already holding this product's lease. It is the
	// ordinary outcome of a pass that runs every few minutes, and it is the one
	// that must never start a second sink: two of them hold separate thread maps
	// and post everything twice.
	OutcomeRunning Outcome = "already_running"
	OutcomeStarted Outcome = "started"
	// OutcomeSecretsUnavailable is nothing reporting and no way to start it,
	// which is the operator's to fix and is said rather than retried around.
	OutcomeSecretsUnavailable Outcome = "secrets_unavailable"
	// OutcomeReportingOff is a product that never asked for a sink. Nothing is
	// wrong with it, so supervision has nothing to do and says so.
	OutcomeReportingOff Outcome = "reporting_off"
)

// Supervision is what one pass did about one product's sink. Everything in it
// is safe to print: the names of the secrets it could not read, never a value.
type Supervision struct {
	Product domain.ProductID `json:"product"`
	Outcome Outcome          `json:"outcome"`
	PID     int              `json:"pid,omitempty"`
	Log     string           `json:"log,omitempty"`
	// Secrets names the stored items that could not be read, so an operator is
	// told which of the two to store rather than that something was missing.
	Secrets []string `json:"secrets,omitempty"`
	Detail  string   `json:"detail,omitempty"`
}

// Supervisor starts this product's sink if nothing is reporting for it.
//
// One supervisor is one product. A machine running several harnesses runs
// several of these, each against its own state, its own configuration, and its
// own stored pair, and none of them can see or interfere with another's sink.
type Supervisor struct {
	// Store is the product's sink state, which holds the lease that answers
	// whether a sink is running and is where a started sink's log goes.
	Store    *Store
	Secrets  SecretReader
	Launcher Launcher
	// Program is the binary to start, and Config the configuration file the
	// sink is told to read. The configuration is passed rather than left to be
	// discovered so that a sink started from a maintenance pass reads this
	// product's configuration wherever the pass happened to be run from.
	Program string
	Config  string
	Dir     string
	// Environ is the environment this process inherited, which the sink's is
	// built from rather than handed.
	Environ []string
}

// Ensure makes a sink be running for this product, and reports what it took.
func (s Supervisor) Ensure(ctx context.Context) (Supervision, error) {
	if s.Store == nil {
		return Supervision{}, errors.New("supervising a sink needs the product's own state")
	}
	if s.Secrets == nil || s.Launcher == nil {
		return Supervision{}, errors.New("supervising a sink needs somewhere to read its secrets and something to start it with")
	}
	product := s.Store.Product()
	running, err := s.Store.Running()
	if err != nil {
		return Supervision{}, err
	}
	if running {
		return Supervision{Product: product, Outcome: OutcomeRunning}, nil
	}

	bot, app, unavailable := s.pair(ctx, product)
	if len(unavailable) > 0 {
		return Supervision{
			Product: product,
			Outcome: OutcomeSecretsUnavailable,
			Secrets: names(unavailable),
			Detail:  detail(unavailable),
		}, nil
	}

	// The sink's own state directory, made before anything is confined to it: a
	// root that does not exist yet is not a root anything can be held inside.
	if err := s.Store.ensureRoot(); err != nil {
		return Supervision{}, err
	}
	logRoot, logPath := s.Store.SinkLog()
	pid, err := s.Launcher.Launch(Launch{
		Program: s.Program,
		Args:    s.arguments(),
		Dir:     s.Dir,
		Env:     Environment(s.Environ, product, bot, app),
		LogRoot: logRoot,
		Log:     logPath,
	})
	if err != nil {
		return Supervision{}, fmt.Errorf("start the Slack sink for %s: %w", product, err)
	}
	return Supervision{
		Product: product,
		Outcome: OutcomeStarted,
		PID:     pid,
		Log:     filepath.Join(logRoot, filepath.FromSlash(logPath)),
	}, nil
}

func (s Supervisor) arguments() []string {
	arguments := []string{"slack"}
	if strings.TrimSpace(s.Config) != "" {
		arguments = append(arguments, "--config", s.Config)
	}
	return arguments
}

// unreadable is one stored secret that could not be produced, named beside why.
type unreadable struct {
	name   string
	reason error
}

// pair reads this product's two tokens, naming the ones it could not read
// rather than stopping at the first. An operator told about one missing item,
// who stores it and finds the next pass tells them about the other, has been
// made to do the work twice.
func (s Supervisor) pair(ctx context.Context, product domain.ProductID) (bot, app string, unavailable []unreadable) {
	for _, name := range []string{BotSecret(product), AppSecret(product)} {
		value, err := s.Secrets.Secret(ctx, name)
		switch {
		case err != nil:
			unavailable = append(unavailable, unreadable{name: name, reason: err})
		case name == BotSecret(product):
			bot = value
		default:
			app = value
		}
	}
	return bot, app, unavailable
}

func names(unavailable []unreadable) []string {
	named := make([]string, 0, len(unavailable))
	for _, secret := range unavailable {
		named = append(named, secret.name)
	}
	return named
}

// detail says why the store would not answer, which separates a pair nobody has
// stored from one this process was refused. Only the store's own complaint is
// carried, which never contains a token: the value is asked for on standard
// output and a refusal is written on standard error.
//
// A complaint already said is not said again. Both items missing is one fact
// about the machine and the commonest one there is, and saying it twice reads
// as two problems.
func detail(unavailable []unreadable) string {
	reasons := make([]string, 0, len(unavailable))
	for _, secret := range unavailable {
		said := secret.reason.Error()
		if !slices.Contains(reasons, said) {
			reasons = append(reasons, said)
		}
	}
	return strings.Join(reasons, "; ")
}

// Environment is what the sink is started with: an inherited environment with
// every Slack variable taken out of it, and this product's own pair and
// namespace put back in.
//
// Taking them out first is the multi-product case, and it is not theoretical: a
// pass run from a shell that has a sibling product's tokens exported would
// otherwise hand them to this product's sink, which connects, authenticates,
// and posts this product's work into the sibling's workspace. The namespace
// goes in beside them so the sink records whose secrets it was launched with,
// which is what lets `yoyo doctor` tell a sink that is merely running from one
// that is running for this product.
func Environment(environ []string, product domain.ProductID, bot, app string) []string {
	constructed := make([]string, 0, len(environ)+3)
	for _, entry := range environ {
		switch name, _, _ := strings.Cut(entry, "="); name {
		case BotTokenVariable, AppTokenVariable, SecretNamespaceVariable:
			continue
		default:
			constructed = append(constructed, entry)
		}
	}
	return append(constructed,
		BotTokenVariable+"="+bot,
		AppTokenVariable+"="+app,
		SecretNamespaceVariable+"="+string(product),
	)
}

// Keychain reads a stored secret out of the macOS keychain, which is where a
// macOS installation keeps this product's pair encrypted at rest.
type Keychain struct {
	Runner  execution.ProcessRunner
	Timeout time.Duration
}

// Secret asks the keychain for one item's password.
//
// This is the one place in the harness that deliberately produces a token, and
// the command is given no observer for that reason: an observer is where output
// is logged from, and the value arrives on this command's standard output. What
// comes back goes to the caller and nowhere else.
func (k Keychain) Secret(ctx context.Context, name string) (string, error) {
	timeout := k.Timeout
	if timeout <= 0 {
		timeout = DefaultSecretTimeout
	}
	result, err := k.Runner.Run(ctx, execution.Command{
		Name:    "security",
		Args:    []string{"find-generic-password", "-s", name, "-a", KeychainAccount, "-w"},
		Timeout: timeout,
	}, nil)
	// Neither refusal names the item. What could not be read is the caller's to
	// say, and it is asking about two of them: an error that named each one
	// would report a keychain nobody has touched as two separate problems.
	if err != nil {
		return "", fmt.Errorf("%w: the keychain could not be asked: %v", ErrSecretUnavailable, err)
	}
	if result.Status != execution.ProcessSucceeded {
		return "", fmt.Errorf("%w: %s", ErrSecretUnavailable, complaint(result.Stderr))
	}
	// The keychain writes the password followed by a newline, and a token with
	// a newline on the end is a token Slack refuses.
	token := strings.Trim(result.Stdout, "\r\n")
	if token == "" {
		return "", fmt.Errorf("%w: it is stored with no value", ErrSecretUnavailable)
	}
	return token, nil
}

// complaint is what the keychain said, on one line, or a stand-in where it said
// nothing at all.
func complaint(stderr string) string {
	said := strings.TrimSpace(stderr)
	if said == "" {
		return "the keychain refused without saying why"
	}
	if line, _, found := strings.Cut(said, "\n"); found {
		return strings.TrimSpace(line)
	}
	return said
}
