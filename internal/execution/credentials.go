package execution

// The reporting sink's credentials, and keeping them out of the environments
// this package builds for other people's processes.
//
// The Slack design's rule is that one separate process posts and no agent ever
// holds a token, and until now that rule was held by how an operator launched
// their sink: the tokens go on the `exec` line, so the shell the harness runs in
// never has them, so nothing the harness starts inherits them. That is true of a
// machine set up the way the setup document teaches and false the moment
// somebody exports the pair to try something — at which point every subprocess
// the harness starts, an agent's included, has a Slack token in its environment
// and nothing says so.
//
// So the absence is made structural here, in the layer that builds the
// environment a subprocess is given, because that is the only place it can be
// one. An environment assembled by inheritance is an environment whose contents
// nobody decided; naming this one is deciding it.
//
// Nothing about the credential model changes: agents still never hold the token
// and the sink is still the only process that does. What changes is that the
// rule is now enforced where it was previously only described, which is a
// strengthening of the boundary rather than a revision of it.

import (
	"os"
	"strings"
)

// SlackBotTokenVariable and SlackAppTokenVariable are the two variables the
// reporting sink reads its credentials from. They are declared here rather than
// beside the sink because this is the package that has to recognize them, and
// internal/slack names them from here so the harness has one spelling of each
// rather than two that can drift apart.
const (
	SlackBotTokenVariable = "SLACK_BOT_TOKEN"
	SlackAppTokenVariable = "SLACK_APP_TOKEN"
)

// WithoutSinkCredentials returns environment with the reporting sink's two
// credentials removed. A nil environment starts from this process's own, which
// is what a command that would otherwise have inherited it is actually given —
// and that inheritance is the case this exists for, because it is the one where
// the tokens arrive without anybody having passed them.
//
// It is unconditional and takes nothing to configure. An agent invocation has no
// use for a Slack token under any arrangement — what the harness posts, it posts
// from the sink — so there is nothing here to opt out of and nothing to decide
// per run.
//
// Everything else is left exactly as it was. A process given only the variables
// somebody thought of has no PATH and no HOME and runs nothing, so this removes
// two names rather than admitting a list.
func WithoutSinkCredentials(environment []string) []string {
	if environment == nil {
		environment = os.Environ()
	}
	kept := make([]string, 0, len(environment))
	for _, entry := range environment {
		name, _, found := strings.Cut(entry, "=")
		if found && (name == SlackBotTokenVariable || name == SlackAppTokenVariable) {
			continue
		}
		kept = append(kept, entry)
	}
	return kept
}
