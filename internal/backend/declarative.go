package backend

// A dialect delivered as data.
//
// This is the answer to how a user-supplied provider plugin arrives, and the
// trade it makes is stated in docs/provider-plugins.md rather than left to be
// inferred. What it buys: a plugin that is data cannot decide anything. There is
// no field here in which to write a duration, a retry count, or a budget, and
// nothing on the other side of this file runs, so the property the design
// demands -- a plugin describes what a provider said and never decides whether
// or how long to wait -- is structural rather than asked for. It also needs no
// fork, no vendoring, and no rebuild of the harness. What it costs: it covers a
// provider whose reports differ from another's in spelling, and covers nothing
// else. A provider whose dialect needs code is a built-in.
//
// Rules are ordered and the first match wins, which is what lets a narrower
// reading stand in front of a broader one -- a limit already being served under
// an overage allowance ahead of the same event read as a limit refusing work.

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// The reset formats a declarative rule may name. They are the shapes providers
// actually quote a reset time in; a provider that states one some other way
// needs a dialect that is code, and the harness's answer for a reset time it
// cannot read is the same either way -- no reset time at all, which is unknown
// rather than fatal.
const (
	ResetFormatUnixSeconds = "unix-seconds"
	ResetFormatUnixMillis  = "unix-millis"
	ResetFormatRFC3339     = "rfc3339"
)

// resetFormats is every format a rule may name, for a refusal that shows the
// choices.
var resetFormats = []string{ResetFormatUnixSeconds, ResetFormatUnixMillis, ResetFormatRFC3339}

// DialectSpec is a provider dialect written as configuration.
type DialectSpec struct {
	// Rules are tried in the order they are written and the first one that
	// matches decides the answer. An event no rule matches is one the dialect
	// says nothing about, which leaves the invocation's outcome where it was.
	Rules []DialectRule `yaml:"rules" json:"rules"`
}

// DialectRule is one reading of a provider event: what has to be true of the
// event, and which of the contract's answers that makes it.
//
// Every field but Answer is a condition or a piece of evidence to carry. There
// is deliberately nowhere here to say how long to wait.
type DialectRule struct {
	// Answer is which of the contract's answers a matching event is. It is
	// required, and it is checked against the contract when the dialect is
	// built rather than when an event arrives.
	Answer Answer `yaml:"answer" json:"answer"`
	// Type and Subtype match the provider's own names for the event exactly. An
	// empty one is not a condition.
	Type    string `yaml:"type,omitempty" json:"type,omitempty"`
	Subtype string `yaml:"subtype,omitempty" json:"subtype,omitempty"`
	// Terminal and Failed match the two facts the adapter states about an event
	// rather than the provider. They are pointers so that "not a condition" and
	// "must be false" stay different things.
	Terminal *bool `yaml:"terminal,omitempty" json:"terminal,omitempty"`
	Failed   *bool `yaml:"failed,omitempty" json:"failed,omitempty"`
	// Match is a regular expression the event's prose must contain. It is how a
	// provider that reports a limit in a sentence is read at all.
	Match string `yaml:"match,omitempty" json:"match,omitempty"`
	// Fields are dotted paths into the event's payload that must equal the
	// stated value. A path the payload does not carry matches nothing, so a
	// field the provider omits is absent rather than false.
	Fields map[string]string `yaml:"fields,omitempty" json:"fields,omitempty"`
	// Kind and KindField are the provider's own name for the limit, stated
	// outright or read from the payload. Only one of them may be given, and
	// only on a limit-reached rule.
	Kind      string `yaml:"kind,omitempty" json:"kind,omitempty"`
	KindField string `yaml:"kind_field,omitempty" json:"kind_field,omitempty"`
	// ResetField and ResetMatch are where the reset time is: a dotted path into
	// the payload, or a regular expression over the prose with exactly one
	// capturing group. Only one of them may be given, and only on a
	// limit-reached rule.
	ResetField string `yaml:"reset_field,omitempty" json:"reset_field,omitempty"`
	ResetMatch string `yaml:"reset_match,omitempty" json:"reset_match,omitempty"`
	// ResetFormat says how to read whatever was found. It is required when
	// either of the two above is given, because a number with no unit is not a
	// time and guessing the unit is how a five-hour wait becomes five days.
	ResetFormat string `yaml:"reset_format,omitempty" json:"reset_format,omitempty"`
}

// NewDeclarativeDialect builds a dialect from a written spec, or reports
// everything wrong with it at once. It is strict on purpose: a rule that would
// never match, or one that names a reset time it has no way to read, is a
// provider plugin that silently does nothing on the day the limit it was written
// for actually fires.
func NewDeclarativeDialect(name string, spec DialectSpec) (Dialect, error) {
	dialect := declarativeDialect{name: strings.TrimSpace(name)}
	var problems []string
	if dialect.name == "" {
		problems = append(problems, "a dialect must be named")
	}
	if len(spec.Rules) == 0 {
		problems = append(problems, "a dialect with no rules reads nothing a provider says")
	}
	for index, rule := range spec.Rules {
		compiled, ruleProblems := compileRule(rule)
		for _, problem := range ruleProblems {
			problems = append(problems, fmt.Sprintf("rule %d %s", index+1, problem))
		}
		if len(ruleProblems) == 0 {
			dialect.rules = append(dialect.rules, compiled)
		}
	}
	if len(problems) > 0 {
		return nil, fmt.Errorf("provider dialect %q: %s", name, strings.Join(problems, "; "))
	}
	return dialect, nil
}

type declarativeDialect struct {
	name  string
	rules []compiledRule
}

func (d declarativeDialect) Name() string { return d.name }

func (d declarativeDialect) Observe(event ProviderEvent) (Observation, bool) {
	for _, rule := range d.rules {
		if !rule.matches(event) {
			continue
		}
		observation := Observation{Answer: rule.spec.Answer}
		switch rule.spec.Answer {
		case AnswerLimitReached:
			observation.Kind = rule.limitKind(event)
			observation.ResetsAt = rule.resetsAt(event)
		case AnswerUnavailable, AnswerInterrupted, AnswerRefused:
			// The provider's own account of the ending, bounded. The category
			// alone does not say what happened, and the message beside it is the
			// difference between a record somebody can act on and three runs
			// that all read the same word.
			observation.Detail = DescribeFailure(event.Subtype, event.Text)
		}
		return observation, true
	}
	return Observation{}, false
}

type compiledRule struct {
	spec       DialectRule
	match      *regexp.Regexp
	resetMatch *regexp.Regexp
}

// compileRule checks one rule and compiles what it can, reporting every problem
// with it rather than the first.
func compileRule(rule DialectRule) (compiledRule, []string) {
	compiled := compiledRule{spec: rule}
	var problems []string
	if !rule.Answer.Valid() {
		problems = append(problems, fmt.Sprintf("names answer %q, which is not one of %s", rule.Answer, DescribeAnswers()))
	}
	if !rule.conditional() {
		problems = append(problems, "states no condition, so it would answer for every event the provider emits")
	}
	if rule.Match != "" {
		expression, err := regexp.Compile(rule.Match)
		if err != nil {
			problems = append(problems, fmt.Sprintf("has an unusable match expression: %v", err))
		}
		compiled.match = expression
	}
	for path, expected := range rule.Fields {
		if strings.TrimSpace(path) == "" {
			problems = append(problems, fmt.Sprintf("matches a field with no path against %q", expected))
		}
	}
	problems = append(problems, rule.limitProblems()...)
	if rule.ResetMatch != "" {
		expression, err := regexp.Compile(rule.ResetMatch)
		switch {
		case err != nil:
			problems = append(problems, fmt.Sprintf("has an unusable reset_match expression: %v", err))
		case expression.NumSubexp() != 1:
			problems = append(problems, fmt.Sprintf("has a reset_match with %d capturing groups, and the reset time is the one it captures",
				expression.NumSubexp()))
		default:
			compiled.resetMatch = expression
		}
	}
	return compiled, problems
}

// limitProblems reports what a rule says about a limit that it has no business
// saying. Everything about a reset time and the provider's name for a limit
// belongs to the one answer that carries them; anywhere else it is a field
// nothing would ever read, which is worth refusing rather than ignoring.
func (r DialectRule) limitProblems() []string {
	var problems []string
	named := r.Kind != "" || r.KindField != "" || r.ResetField != "" || r.ResetMatch != "" || r.ResetFormat != ""
	if named && r.Answer != AnswerLimitReached {
		return append(problems, fmt.Sprintf("describes a limit but answers %q, and only %q carries a limit's name and reset time",
			r.Answer, AnswerLimitReached))
	}
	if r.Kind != "" && r.KindField != "" {
		problems = append(problems, "states the limit's name and also reads it from the payload")
	}
	if r.ResetField != "" && r.ResetMatch != "" {
		problems = append(problems, "reads the reset time from the payload and also from the prose")
	}
	statesReset := r.ResetField != "" || r.ResetMatch != ""
	switch {
	case statesReset && r.ResetFormat == "":
		problems = append(problems, fmt.Sprintf("reads a reset time without saying how to read it; reset_format is one of %s",
			strings.Join(resetFormats, ", ")))
	case statesReset && !knownResetFormat(r.ResetFormat):
		problems = append(problems, fmt.Sprintf("names reset_format %q, which is not one of %s", r.ResetFormat,
			strings.Join(resetFormats, ", ")))
	case !statesReset && r.ResetFormat != "":
		problems = append(problems, "names a reset_format and nothing to read with it")
	}
	return problems
}

// conditional reports a rule that says anything at all about which events it is
// for. A rule with no condition answers for the whole stream, which is never
// what somebody meant to write.
func (r DialectRule) conditional() bool {
	return r.Type != "" || r.Subtype != "" || r.Terminal != nil || r.Failed != nil || r.Match != "" || len(r.Fields) > 0
}

func knownResetFormat(format string) bool {
	for _, known := range resetFormats {
		if format == known {
			return true
		}
	}
	return false
}

func (c compiledRule) matches(event ProviderEvent) bool {
	if c.spec.Type != "" && c.spec.Type != event.Type {
		return false
	}
	if c.spec.Subtype != "" && c.spec.Subtype != event.Subtype {
		return false
	}
	if c.spec.Terminal != nil && *c.spec.Terminal != event.Terminal {
		return false
	}
	if c.spec.Failed != nil && *c.spec.Failed != event.Failed {
		return false
	}
	if c.match != nil && !c.match.MatchString(event.Text) {
		return false
	}
	for path, expected := range c.spec.Fields {
		value, found := lookupField(event.Payload, path)
		if !found || value != expected {
			return false
		}
	}
	return true
}

func (c compiledRule) limitKind(event ProviderEvent) string {
	if c.spec.KindField == "" {
		return c.spec.Kind
	}
	value, found := lookupField(event.Payload, c.spec.KindField)
	if !found {
		return ""
	}
	return value
}

// resetsAt reads the instant the limit lifts, and answers the zero time for
// anything it cannot read. That is not a failure: a limit whose reset time is
// unreadable is still a limit, and it has to reach the caller as one. What the
// harness refuses is guessing the wait, not noticing the refusal.
func (c compiledRule) resetsAt(event ProviderEvent) time.Time {
	var raw string
	switch {
	case c.spec.ResetField != "":
		value, found := lookupField(event.Payload, c.spec.ResetField)
		if !found {
			return time.Time{}
		}
		raw = value
	case c.resetMatch != nil:
		captured := c.resetMatch.FindStringSubmatch(event.Text)
		if captured == nil {
			return time.Time{}
		}
		raw = captured[1]
	default:
		return time.Time{}
	}
	return parseReset(raw, c.spec.ResetFormat)
}

func parseReset(raw, format string) time.Time {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return time.Time{}
	}
	switch format {
	case ResetFormatUnixSeconds:
		seconds, err := strconv.ParseInt(trimmed, 10, 64)
		if err != nil || seconds <= 0 {
			return time.Time{}
		}
		return time.Unix(seconds, 0).UTC()
	case ResetFormatUnixMillis:
		millis, err := strconv.ParseInt(trimmed, 10, 64)
		if err != nil || millis <= 0 {
			return time.Time{}
		}
		return time.UnixMilli(millis).UTC()
	case ResetFormatRFC3339:
		parsed, err := time.Parse(time.RFC3339, trimmed)
		if err != nil {
			return time.Time{}
		}
		return parsed.UTC()
	default:
		return time.Time{}
	}
}

// lookupField reads a dotted path out of an event's payload and renders it as
// the string a rule compares against. A payload that will not decode, a path
// that is not there, and a value that is neither text, a number, nor a boolean
// all answer the same way: absent. A rule matching on something absent matches
// nothing, which is the safe direction -- what it costs is a refusal the harness
// keeps failing on, and what the other direction costs is a wait nobody can
// justify.
func lookupField(payload json.RawMessage, path string) (string, bool) {
	if len(payload) == 0 || strings.TrimSpace(path) == "" {
		return "", false
	}
	var value any
	if err := json.Unmarshal(payload, &value); err != nil {
		return "", false
	}
	for _, segment := range strings.Split(path, ".") {
		object, isObject := value.(map[string]any)
		if !isObject {
			return "", false
		}
		next, present := object[segment]
		if !present {
			return "", false
		}
		value = next
	}
	return renderField(value)
}

func renderField(value any) (string, bool) {
	switch typed := value.(type) {
	case string:
		return typed, true
	case bool:
		return strconv.FormatBool(typed), true
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64), true
	case json.Number:
		return typed.String(), true
	default:
		return "", false
	}
}
