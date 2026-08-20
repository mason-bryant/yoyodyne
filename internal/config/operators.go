package config

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// Operator is one human the project recognizes: the identifier namespaces they
// are known by, and what they may do. The authority is attached to the human
// rather than to any one of those identifiers, so an act is authorized by
// resolving whichever namespace it arrived through to a person and then asking
// what that person may do.
//
// No identity machinery is invented here, and none is needed. Git and Dolt
// authorship are the assertion a human makes about which entry below they are,
// and the forge's push authentication is the proof at the one boundary that is
// shared. What this mapping adds is the join between namespaces that otherwise
// have nothing to do with each other.
//
// It is top level rather than under the surface an act happens to arrive on.
// The moment one human binds three namespaces, filing them under any one of the
// three is a category error: an allow-list of Slack ids stops being something
// somebody authors and becomes a derivation from the grants here.
type Operator struct {
	// GitEmail is the address this human's commits and tracker writes are
	// authored with. It is what git and Dolt record, and therefore the whole of
	// what a change asserts about who made it.
	GitEmail string `yaml:"git_email,omitempty" json:"git_email,omitempty"`
	// ForgeAccount is this human's account on the remote the project publishes
	// to. It is the one namespace with proof behind it rather than an assertion,
	// because a push is authenticated by the forge before the harness sees it.
	ForgeAccount string `yaml:"forge_account,omitempty" json:"forge_account,omitempty"`
	// SlackMemberID is this human's member id in the workspace the sink reports
	// into. It lives here rather than in the environment for the reason the rest
	// of this does: a member id is identity, not a secret.
	SlackMemberID string `yaml:"slack_member_id,omitempty" json:"slack_member_id,omitempty"`
	// Grants is what this human may do, whichever namespace they arrive through.
	// It defaults to empty, so binding somebody's identifiers records who they
	// are without giving them anything: recognizing a human and authorizing one
	// are two decisions, and a mapping that ran them together would authorize by
	// introduction.
	Grants []Grant `yaml:"grants,omitempty" json:"grants,omitempty"`
}

// Grant is one authority a human holds. The set is fixed in the harness for the
// reason the set of agent roles is: everything downstream turns on it, and a
// typo that loaded as an unknown grant would be an authority nobody has and no
// message anybody sees.
type Grant string

const (
	// GrantOwnIntent is the authority over what the product is for: stating the
	// brief, the goals, and the non-goals, and approving them. At most one human
	// holds it, because the coordination design assumes intent that cannot
	// conflict with itself — several humans amending goals concurrently is
	// conflict machinery nobody has designed (docs/team-mode-scope.md).
	GrantOwnIntent Grant = "own-intent"
	// GrantDirectWork is the authority to steer work already in flight: the
	// directives that reach a run, and the thread replies the Slack sink acts on
	// once the inbound half exists. Every collaborator who runs their own harness
	// holds it; owning intent is the narrower thing.
	GrantDirectWork Grant = "direct-work"
)

// Grants lists every grant the harness knows, in a stable order.
func Grants() []Grant {
	return []Grant{GrantOwnIntent, GrantDirectWork}
}

func (g Grant) Valid() bool {
	switch g {
	case GrantOwnIntent, GrantDirectWork:
		return true
	default:
		return false
	}
}

// Namespace names one identifier system a human is known by. An authority check
// says which one an act arrived through, because that is the only thing the act
// itself knows: a commit carries an address, a push carries a forge account, a
// thread reply carries a member id, and none of them carries a person.
type Namespace string

const (
	NamespaceGitEmail     Namespace = "git_email"
	NamespaceForgeAccount Namespace = "forge_account"
	NamespaceSlackMember  Namespace = "slack_member_id"
)

// Namespaces lists every namespace an operator may bind, in a stable order.
func Namespaces() []Namespace {
	return []Namespace{NamespaceGitEmail, NamespaceForgeAccount, NamespaceSlackMember}
}

// OperatorNames lists the configured humans in a stable order, so resolution,
// derivation, and refusals all read the mapping the same way round.
func (c Config) OperatorNames() []string {
	names := make([]string, 0, len(c.Operators))
	for name := range c.Operators {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// ResolveOperator finds the human an identifier in one namespace belongs to. An
// identifier bound to nobody resolves to nobody rather than to a default, which
// is what makes the mapping the whole of who the project recognizes.
func (c Config) ResolveOperator(namespace Namespace, identifier string) (string, Operator, bool) {
	wanted := foldIdentifier(namespace, identifier)
	if wanted == "" {
		return "", Operator{}, false
	}
	for _, name := range c.OperatorNames() {
		operator := c.Operators[name]
		if foldIdentifier(namespace, operator.binding(namespace)) == wanted {
			return name, operator, true
		}
	}
	return "", Operator{}, false
}

// OperatorHolds is the whole of an authority check: resolve the human from
// whichever namespace the act arrived through, then ask what that human may do.
// An unbound identifier holds nothing, so the check defaults closed.
func (c Config) OperatorHolds(namespace Namespace, identifier string, grant Grant) bool {
	_, operator, found := c.ResolveOperator(namespace, identifier)
	return found && operator.Holds(grant)
}

// Holds reports whether this human was granted an authority.
func (o Operator) Holds(grant Grant) bool {
	for _, held := range o.Grants {
		if held == grant {
			return true
		}
	}
	return false
}

// SlackOperators is the allow-list of Slack member ids whose thread replies the
// harness will act on: every human granted direct-work who has bound a member
// id, and nobody else. It is derived rather than authored, which is the point of
// the mapping above — a list somebody maintains beside the grants is a list that
// disagrees with them, and the disagreement is silent and about authority.
//
// A human granted direct-work who has bound no member id is simply not on it.
// They hold the authority; Slack is not a boundary they can reach it through.
func (c Config) SlackOperators() []string {
	var allowed []string
	for _, name := range c.OperatorNames() {
		operator := c.Operators[name]
		member := strings.TrimSpace(operator.SlackMemberID)
		if member == "" || !operator.Holds(GrantDirectWork) {
			continue
		}
		allowed = append(allowed, member)
	}
	return allowed
}

// binding is what this human bound in one namespace, empty when they bound
// nothing there.
func (o Operator) binding(namespace Namespace) string {
	switch namespace {
	case NamespaceGitEmail:
		return o.GitEmail
	case NamespaceForgeAccount:
		return o.ForgeAccount
	case NamespaceSlackMember:
		return o.SlackMemberID
	default:
		return ""
	}
}

// clone copies an entry deeply enough that the resolved configuration does not
// share a grants slice with the document it was decoded from.
func (o Operator) clone() Operator {
	copied := o
	copied.Grants = append([]Grant(nil), o.Grants...)
	return copied
}

// foldIdentifier is how one namespace's identifiers are compared. A git address
// and a forge account are case-insensitive where they live — nobody's commits
// stop being theirs for capitalizing their own address — so they are folded, and
// folding them is also what lets the duplicate check below catch two entries
// that differ only in case. A Slack member id is compared exactly: it is an
// opaque uppercase id the workspace issued rather than something a person types.
func foldIdentifier(namespace Namespace, value string) string {
	trimmed := strings.TrimSpace(value)
	if namespace == NamespaceSlackMember {
		return trimmed
	}
	return strings.ToLower(trimmed)
}

// MaxOperatorIdentifierBytes bounds one bound identifier so it stays an
// identifier rather than a document smuggled into an authority check. It is the
// longest address SMTP carries, which is the longest of the three namespaces.
const MaxOperatorIdentifierBytes = 254

// What each namespace's identifiers look like. The addresses are deliberately
// loose — an address is whatever the mail system it lives in accepts, and a
// pattern strict enough to be interesting would refuse somebody's real one —
// so this checks only that it is one address rather than a list, a display
// name, or something with a space in it. A forge account is the shape every
// forge agrees on. A Slack member id reuses the workspace's own shape, which is
// checked here for the reason it was checked under `slack` before: a display
// name is not identity, and an allow-list keyed on something that moves quietly
// stops matching the person it was written for.
var (
	gitEmailPattern     = regexp.MustCompile(`^[^\s@]+@[^\s@]+$`)
	forgeAccountPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
)

// problem reports what makes one bound identifier unusable in this namespace,
// as the tail of a message the caller heads with the operator and the key.
func (n Namespace) problem(value string) string {
	if len(value) > MaxOperatorIdentifierBytes {
		return fmt.Sprintf("is %d bytes, limit is %d", len(value), MaxOperatorIdentifierBytes)
	}
	switch n {
	case NamespaceGitEmail:
		if !gitEmailPattern.MatchString(value) {
			return fmt.Sprintf("%q must be a single email address", value)
		}
	case NamespaceForgeAccount:
		if !forgeAccountPattern.MatchString(value) {
			return fmt.Sprintf("%q must be a forge account name", value)
		}
	case NamespaceSlackMember:
		if !slackUserPattern.MatchString(value) {
			return fmt.Sprintf("%q must be a Slack member id", value)
		}
	}
	return ""
}

// operatorProblems reports what makes the mapping unusable. Everything is
// checked whether or not anything reads a given namespace yet, for the reason
// the Slack section is checked whether or not reporting is switched on: a typo
// in an authority record is found now, or it is found as an act that silently
// resolved to nobody.
func (c Config) operatorProblems() []string {
	var problems []string
	// Which human each identifier is already spoken for by, per namespace. An
	// identifier that resolves to two humans resolves to neither, so it is caught
	// here rather than becoming an authority check whose answer depends on the
	// order a map happened to be walked in.
	bound := make(map[Namespace]map[string]string, len(Namespaces()))
	for _, namespace := range Namespaces() {
		bound[namespace] = map[string]string{}
	}
	var intentOwners []string

	for _, name := range c.OperatorNames() {
		if err := domain.ValidateIdentifier("operator name", name); err != nil {
			problems = append(problems, err.Error())
		}
		operator := c.Operators[name]

		namespacesBound := 0
		for _, namespace := range Namespaces() {
			value := strings.TrimSpace(operator.binding(namespace))
			if value == "" {
				continue
			}
			namespacesBound++
			if problem := namespace.problem(value); problem != "" {
				problems = append(problems, fmt.Sprintf("operators.%s.%s %s", name, namespace, problem))
				continue
			}
			folded := foldIdentifier(namespace, value)
			if owner, taken := bound[namespace][folded]; taken {
				problems = append(problems, fmt.Sprintf("operators.%s.%s %q is already bound to operator %q; an identifier that resolves to two humans resolves to neither", name, namespace, value, owner))
				continue
			}
			bound[namespace][folded] = name
		}
		// A human bound to nothing is authority attached to nobody: no act can
		// arrive carrying an identifier that reaches them, so whatever they were
		// granted is unreachable rather than merely unused.
		if namespacesBound == 0 {
			problems = append(problems, fmt.Sprintf("operator %q binds no identifier namespace; authority attaches to a human an act can be recognized as", name))
		}

		seen := make(map[Grant]bool, len(operator.Grants))
		for index, grant := range operator.Grants {
			switch {
			case !grant.Valid():
				problems = append(problems, fmt.Sprintf("operators.%s.grants[%d] is unknown grant %q; grants are %s", name, index, grant, describeGrants()))
			case seen[grant]:
				problems = append(problems, fmt.Sprintf("operators.%s.grants[%d] repeats %q", name, index, grant))
			default:
				seen[grant] = true
			}
		}
		if seen[GrantOwnIntent] {
			intentOwners = append(intentOwners, strconv.Quote(name))
		}
	}

	// Intent has one owner. Two humans holding it is not a stricter arrangement
	// than one: it is concurrent amendment of the goals everything else traces
	// to, which is the case the coordination design does not cover.
	if len(intentOwners) > 1 {
		problems = append(problems, fmt.Sprintf("operators %s each hold %q, and intent has one owner", strings.Join(intentOwners, ", "), GrantOwnIntent))
	}
	return problems
}

// describeGrants lists the harness's grants the way an operator would read them
// back into the file they mistyped.
func describeGrants() string {
	names := make([]string, 0, len(Grants()))
	for _, grant := range Grants() {
		names = append(names, strconv.Quote(string(grant)))
	}
	return strings.Join(names, ", ")
}
