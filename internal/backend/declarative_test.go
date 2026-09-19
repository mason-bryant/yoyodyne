package backend

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// truth is a pointer to a boolean, which is how a rule says "must be true"
// rather than "not a condition".
func truth(value bool) *bool { return &value }

// A second provider is added without changing harness code. The dialect below is
// the whole of it: a project writes this, names it in its agents, and the
// harness reads its rate limits, its retries, and its reset times without a line
// of Go changing.
func TestASecondProviderIsAddedWithoutChangingHarnessCode(t *testing.T) {
	t.Parallel()

	dialect, err := NewDeclarativeDialect("my-openai-harness", DialectSpec{Rules: []DialectRule{
		{Answer: AnswerRetrying, Type: "retry"},
		// A limit already being served under an overage allowance is still
		// serving, and the narrower reading stands in front of the broader one.
		{Answer: AnswerServed, Type: "quota", Fields: map[string]string{"using_overage": "true"}},
		{
			Answer:      AnswerLimitReached,
			Type:        "quota",
			Fields:      map[string]string{"state": "exceeded"},
			KindField:   "window",
			ResetField:  "resets_at",
			ResetFormat: ResetFormatUnixSeconds,
		},
		{Answer: AnswerServed, Type: "quota"},
		{Answer: AnswerUnavailable, Terminal: truth(true), Failed: truth(true), Match: `(?i)\b503\b`},
		{Answer: AnswerInterrupted, Terminal: truth(true), Failed: truth(true), Match: `(?i)connection reset`},
		{Answer: AnswerRefused, Terminal: truth(true), Failed: truth(true)},
	}})
	if err != nil {
		t.Fatalf("NewDeclarativeDialect() error = %v", err)
	}
	if dialect.Name() != "my-openai-harness" {
		t.Fatalf("Name() = %q", dialect.Name())
	}

	exhausted, said := dialect.Observe(ProviderEvent{
		Type:    "quota",
		Payload: json.RawMessage(`{"state":"exceeded","window":"daily","resets_at":1787000000}`),
	})
	if !said || exhausted.Answer != AnswerLimitReached {
		t.Fatalf("observation = %#v, said = %t, want an exhausted limit", exhausted, said)
	}
	if exhausted.Kind != "daily" {
		t.Fatalf("Kind = %q, want the provider's own name for the window", exhausted.Kind)
	}
	if want := time.Unix(1787000000, 0).UTC(); !exhausted.ResetsAt.Equal(want) {
		t.Fatalf("ResetsAt = %s, want %s", exhausted.ResetsAt, want)
	}

	// The same event with overage serving it is not a refusal at all, because
	// the narrower rule is written first.
	serving, said := dialect.Observe(ProviderEvent{
		Type:    "quota",
		Payload: json.RawMessage(`{"state":"exceeded","using_overage":true,"resets_at":1787000000}`),
	})
	if !said || serving.Answer != AnswerServed {
		t.Fatalf("observation = %#v, said = %t, want a limit still being served", serving, said)
	}

	overloaded, said := dialect.Observe(ProviderEvent{
		Type: "response", Terminal: true, Failed: true, Subtype: "http_error", Text: "503 upstream unavailable",
	})
	if !said || overloaded.Answer != AnswerUnavailable {
		t.Fatalf("observation = %#v, said = %t, want a transient overload", overloaded, said)
	}

	died, said := dialect.Observe(ProviderEvent{
		Type: "response", Terminal: true, Failed: true, Subtype: "transport", Text: "connection reset by peer",
	})
	if !said || died.Answer != AnswerInterrupted {
		t.Fatalf("observation = %#v, said = %t, want a transient death", died, said)
	}
	if !strings.Contains(died.Detail, "connection reset by peer") {
		t.Fatalf("Detail = %q, want the provider's own words", died.Detail)
	}

	refused, said := dialect.Observe(ProviderEvent{
		Type: "response", Terminal: true, Failed: true, Subtype: "invalid_request", Text: "unknown model",
	})
	if !said || refused.Answer != AnswerRefused {
		t.Fatalf("observation = %#v, said = %t, want a refusal that stands", refused, said)
	}

	// Most of a provider's stream is prose and tool calls, and a dialect says
	// nothing about any of it. Saying nothing leaves the invocation's outcome
	// where it was, which is the safe answer and the common one.
	if _, said := dialect.Observe(ProviderEvent{Type: "message", Text: "thinking"}); said {
		t.Fatal("the dialect answered for an event none of its rules describe")
	}
}

// The property the design demands is structural rather than asked for: there is
// nowhere in a declared rule to write a duration, a retry count, or a budget, so
// a plugin cannot decide to keep waiting. This test states it against the
// decoder, which is where somebody would find out.
func TestADeclaredDialectCannotStateAWait(t *testing.T) {
	t.Parallel()

	var rule DialectRule
	decoder := json.NewDecoder(strings.NewReader(`{"answer":"limit-reached","type":"quota","wait":"30m"}`))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&rule); err == nil {
		t.Fatal("a rule accepted a wait, and a plugin that can decide to keep waiting can spend an account")
	}
}

// A rule that would never match, or one naming a reset time it has no way to
// read, is a plugin that loads and then does nothing on the day the limit it was
// written for actually fires. Every problem is reported at once.
func TestADialectThatWouldSilentlyDoNothingIsRefused(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name string
		rule DialectRule
		want string
	}{
		{
			name: "an answer the contract does not name",
			rule: DialectRule{Answer: "wait-a-bit", Type: "quota"},
			want: "not one of",
		},
		{
			name: "a rule that answers for everything",
			rule: DialectRule{Answer: AnswerRefused},
			want: "states no condition",
		},
		{
			name: "a match expression that will not compile",
			rule: DialectRule{Answer: AnswerRefused, Match: "("},
			want: "unusable match expression",
		},
		{
			name: "a reset time with no unit",
			rule: DialectRule{Answer: AnswerLimitReached, Type: "quota", ResetField: "resets_at"},
			want: "without saying how to read it",
		},
		{
			name: "a reset format nothing reads",
			rule: DialectRule{Answer: AnswerLimitReached, Type: "quota", ResetField: "resets_at", ResetFormat: "iso"},
			want: "not one of",
		},
		{
			name: "a reset time on an answer that carries none",
			rule: DialectRule{Answer: AnswerUnavailable, Type: "quota", ResetField: "resets_at", ResetFormat: ResetFormatUnixSeconds},
			want: "only \"limit-reached\" carries",
		},
		{
			name: "a reset expression that captures nothing",
			rule: DialectRule{Answer: AnswerLimitReached, Type: "quota", ResetMatch: `resets soon`, ResetFormat: ResetFormatRFC3339},
			want: "capturing groups",
		},
		{
			name: "a reset time read from two places",
			rule: DialectRule{
				Answer: AnswerLimitReached, Type: "quota",
				ResetField: "resets_at", ResetMatch: `at (.+)$`, ResetFormat: ResetFormatRFC3339,
			},
			want: "also from the prose",
		},
		{
			name: "a channel the contract does not name",
			rule: DialectRule{Answer: AnswerUnauthenticated, Channel: "tty", Match: "not logged in"},
			want: "names channel \"tty\", which is not one of",
		},
		{
			// Stderr has no type, no subtype, and no payload, so a rule reading it
			// with no match expression answers for whatever the process said.
			name: "a stderr rule with nothing to match",
			rule: DialectRule{Answer: AnswerUnauthenticated, Channel: string(domain.ProviderChannelStderr), Terminal: truth(true)},
			want: "reads stderr without a match expression",
		},
		{
			// Plain stdout is prose exactly as stderr is, and is refused on the
			// same terms.
			name: "a plain-stdout rule with nothing to match",
			rule: DialectRule{Answer: AnswerUnauthenticated, Channel: string(domain.ProviderChannelStdout), Terminal: truth(true)},
			want: "reads stdout without a match expression",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := NewDeclarativeDialect("declared", DialectSpec{Rules: []DialectRule{test.rule}})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("NewDeclarativeDialect() error = %v, want it to contain %q", err, test.want)
			}
		})
	}

	if _, err := NewDeclarativeDialect("declared", DialectSpec{}); err == nil {
		t.Fatal("a dialect with no rules reads nothing a provider says, and was accepted")
	}
}

// A reset time in a shape the dialect cannot read is no reset time at all, which
// is unknown rather than fatal: the limit still reaches the caller as a limit,
// and the harness asks again on the interval it states. What is refused is
// guessing the wait, not noticing the refusal.
func TestAnUnreadableResetTimeStillReportsTheLimit(t *testing.T) {
	t.Parallel()

	dialect, err := NewDeclarativeDialect("declared", DialectSpec{Rules: []DialectRule{{
		Answer:      AnswerLimitReached,
		Type:        "quota",
		ResetField:  "resets_at",
		ResetFormat: ResetFormatRFC3339,
	}}})
	if err != nil {
		t.Fatalf("NewDeclarativeDialect() error = %v", err)
	}
	for _, payload := range []string{
		`{"resets_at":"half past four"}`,
		`{"resets_at":null}`,
		`{}`,
		`not json at all`,
	} {
		observation, said := dialect.Observe(ProviderEvent{Type: "quota", Payload: json.RawMessage(payload)})
		if !said || observation.Answer != AnswerLimitReached {
			t.Fatalf("payload %s: observation = %#v, said = %t, want the limit reported", payload, observation, said)
		}
		if !observation.ResetsAt.IsZero() {
			t.Fatalf("payload %s: ResetsAt = %s, want no reset time rather than a guessed one", payload, observation.ResetsAt)
		}
	}
}

func TestResetFormatsAreReadInTheUnitsProvidersQuote(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name   string
		format string
		raw    string
		want   time.Time
	}{
		{name: "unix seconds", format: ResetFormatUnixSeconds, raw: "1787000000", want: time.Unix(1787000000, 0).UTC()},
		{name: "unix millis", format: ResetFormatUnixMillis, raw: "1787000000000", want: time.UnixMilli(1787000000000).UTC()},
		{
			name:   "rfc3339",
			format: ResetFormatRFC3339,
			raw:    "2026-08-27T20:30:00Z",
			want:   time.Date(2026, 8, 27, 20, 30, 0, 0, time.UTC),
		},
		// A number with no unit is not a time, and guessing the unit is how a
		// five-hour wait becomes five days. Anything unreadable is no reset time.
		{name: "seconds read as millis", format: ResetFormatRFC3339, raw: "1787000000"},
		{name: "an epoch of nothing", format: ResetFormatUnixSeconds, raw: "0"},
		{name: "a negative epoch", format: ResetFormatUnixSeconds, raw: "-1"},
		{name: "empty", format: ResetFormatUnixSeconds, raw: ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got := parseReset(test.raw, test.format)
			if !got.Equal(test.want) {
				t.Fatalf("parseReset(%q, %q) = %s, want %s", test.raw, test.format, got, test.want)
			}
		})
	}
}

// A field the provider omits is absent rather than false, and a rule matching
// something absent matches nothing. That is the safe direction: what it costs is
// a refusal the harness keeps failing on, and what the other direction costs is
// a wait nobody can justify.
func TestAnAbsentFieldMatchesNothing(t *testing.T) {
	t.Parallel()

	if value, found := lookupField(json.RawMessage(`{"a":{"b":"c"}}`), "a.b"); !found || value != "c" {
		t.Fatalf("lookupField() = %q, %t, want the nested value", value, found)
	}
	if value, found := lookupField(json.RawMessage(`{"n":5}`), "n"); !found || value != "5" {
		t.Fatalf("lookupField() = %q, %t, want a whole number rendered whole", value, found)
	}
	if value, found := lookupField(json.RawMessage(`{"flag":false}`), "flag"); !found || value != "false" {
		t.Fatalf("lookupField() = %q, %t, want the boolean rendered", value, found)
	}
	for _, path := range []string{"missing", "a.missing", "a.b.c", ""} {
		if _, found := lookupField(json.RawMessage(`{"a":{"b":"c"}}`), path); found {
			t.Errorf("lookupField(%q) found something that is not there", path)
		}
	}
	if _, found := lookupField(nil, "a"); found {
		t.Error("lookupField() found a field in an event with no payload")
	}
}

// Stderr reaches only a rule that asked for it. A rule written before there was
// a second channel keeps matching exactly what it did — the envelope — so a
// declared dialect's refusal rule cannot start answering for a process's
// diagnostics; and a rule that names stderr reads the refusal a provider makes
// before it writes any envelope, which is the one case stderr is handed over
// for.
func TestStderrReachesOnlyARuleThatAskedForIt(t *testing.T) {
	t.Parallel()

	dialect, err := NewDeclarativeDialect("declared", DialectSpec{Rules: []DialectRule{
		{Answer: AnswerUnauthenticated, Channel: string(domain.ProviderChannelStderr), Match: "(?i)not signed in"},
		{Answer: AnswerRefused, Match: "(?i)not signed in"},
	}})
	if err != nil {
		t.Fatalf("NewDeclarativeDialect() error = %v", err)
	}
	stderr := ProviderEvent{Channel: domain.ProviderChannelStderr, Text: "fatal: not signed in; run my-harness login"}
	observation, said := dialect.Observe(stderr)
	if !said || observation.Answer != AnswerUnauthenticated {
		t.Fatalf("Observe(stderr) = %#v, %t, want the stderr rule's answer", observation, said)
	}
	envelope := ProviderEvent{Type: "error", Text: "not signed in", Terminal: true, Failed: true}
	observation, said = dialect.Observe(envelope)
	if !said || observation.Answer != AnswerRefused {
		t.Fatalf("Observe(envelope) = %#v, %t, want the envelope rule's answer rather than the stderr rule's", observation, said)
	}

	// The same envelope rule alone says nothing about stderr, however well the
	// words match: it never asked for that channel.
	envelopeOnly, err := NewDeclarativeDialect("declared", DialectSpec{Rules: []DialectRule{
		{Answer: AnswerRefused, Match: "(?i)not signed in"},
	}})
	if err != nil {
		t.Fatalf("NewDeclarativeDialect() error = %v", err)
	}
	if observation, said := envelopeOnly.Observe(stderr); said {
		t.Fatalf("Observe(stderr) = %#v, said, want a rule that named no channel to read the envelope alone", observation)
	}
	// Plain stdout is its own channel: the stderr rule does not read it, and a
	// rule that names it does. The two are prose under the same rule, but which
	// one a refusal came on is the record's evidence, so a rule says which it
	// reads.
	stdout := ProviderEvent{Channel: domain.ProviderChannelStdout, Text: "fatal: not signed in; run my-harness login"}
	if observation, said := dialect.Observe(stdout); said {
		t.Fatalf("Observe(stdout) = %#v, said, want a stderr rule to leave plain stdout alone", observation)
	}
	stdoutRule, err := NewDeclarativeDialect("declared", DialectSpec{Rules: []DialectRule{
		{Answer: AnswerUnauthenticated, Channel: string(domain.ProviderChannelStdout), Match: "(?i)not signed in"},
	}})
	if err != nil {
		t.Fatalf("NewDeclarativeDialect() error = %v", err)
	}
	if observation, said := stdoutRule.Observe(stdout); !said || observation.Answer != AnswerUnauthenticated {
		t.Fatalf("Observe(stdout) = %#v, %t, want a rule naming plain stdout to read it", observation, said)
	}
	if observation, said := stdoutRule.Observe(stderr); said {
		t.Fatalf("Observe(stderr) = %#v, said, want a plain-stdout rule to leave stderr alone", observation)
	}
	// A rule that spells the envelope out is the same rule as one that named
	// none.
	spelled, err := NewDeclarativeDialect("declared", DialectSpec{Rules: []DialectRule{
		{Answer: AnswerRefused, Channel: string(domain.ProviderChannelEnvelope), Match: "(?i)not signed in"},
	}})
	if err != nil {
		t.Fatalf("NewDeclarativeDialect() error = %v", err)
	}
	if observation, said := spelled.Observe(envelope); !said || observation.Answer != AnswerRefused {
		t.Fatalf("Observe(envelope) = %#v, %t, want a rule naming the envelope to read it", observation, said)
	}
}
