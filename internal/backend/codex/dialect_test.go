package codex

import (
	"testing"
	"time"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
)

// Each of the contract's answers this provider can give, and the evidence it
// gives it on. The distinctions are the ones the contract draws rather than ones
// this dialect invented: a retry the provider is taking itself earns the harness
// nothing, an exhausted limit is a wait on a deadline, an overloaded server is a
// far shorter wait, a model the provider has not got is answered by asking for
// another one, a status describing the request is a refusal that stands, and a
// death that judged nothing about the work asks for another attempt.
func TestObserveAnswersWhatTheProviderSaid(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name   string
		event  backendapi.ProviderEvent
		want   backendapi.Answer
		said   bool
		resets time.Time
	}{
		{
			name:  "a stream the provider is retrying itself",
			event: backendapi.ProviderEvent{Type: eventStreamError, Text: "stream disconnected; retrying"},
			want:  backendapi.AnswerRetrying,
			said:  true,
		},
		{
			name:   "a usage limit naming when it lifts",
			event:  failedTerminal("You've hit your usage limit. Try again after 2026-08-27T18:00:00Z."),
			want:   backendapi.AnswerLimitReached,
			said:   true,
			resets: time.Date(2026, 8, 27, 18, 0, 0, 0, time.UTC),
		},
		{
			// A limit that named no reset time this dialect can read is still a
			// limit. What the harness makes of the missing time is the contract's
			// answer in one place for every provider, not a wait guessed here.
			name:  "a usage limit naming no reset this dialect can read",
			event: failedTerminal("You've hit your usage limit. Try again in about three hours."),
			want:  backendapi.AnswerLimitReached,
			said:  true,
		},
		{
			// 429 is a client status as well as the shape a limit arrives in, so
			// the limit is read first or every exhausted account would be a
			// refusal that stands.
			name:  "a limit reported as a status",
			event: failedTerminal("request failed with 429 Too Many Requests"),
			want:  backendapi.AnswerLimitReached,
			said:  true,
		},
		{
			name:  "the provider's own servers, transiently",
			event: failedTerminal("unexpected status 503 Service Unavailable"),
			want:  backendapi.AnswerUnavailable,
			said:  true,
		},
		{
			// A model the provider has not got is the one refusal the caller can
			// answer by changing the request rather than by waiting, so it is read
			// ahead of the client-error class it belongs to.
			name:  "a model this provider has not got",
			event: failedTerminal(`404 {"error":{"code":"model_not_found"}}`),
			want:  backendapi.AnswerModelUnavailable,
			said:  true,
		},
		{
			// Both halves of the match are narrow on purpose: a not-found that
			// names no model is left where it was, a refusal that stands, rather
			// than becoming a silent move to another model.
			name:  "a not-found that names no model",
			event: failedTerminal("404 Not Found"),
			want:  backendapi.AnswerRefused,
			said:  true,
		},
		{
			name:  "a status describing the request",
			event: failedTerminal("401 Unauthorized: check your credentials"),
			want:  backendapi.AnswerRefused,
			said:  true,
		},
		{
			name:  "a death that judged nothing about the work",
			event: failedTerminal("connection closed before the response completed"),
			want:  backendapi.AnswerInterrupted,
			said:  true,
		},
		{
			// A completed task says nothing about capacity, so reading one as
			// evidence that a limit has lifted would be inventing a fact the
			// provider never stated.
			name:  "a terminal that succeeded",
			event: backendapi.ProviderEvent{Type: eventTaskComplete, Text: "done", Terminal: true},
		},
		{
			name:  "the ordinary prose of a run",
			event: backendapi.ProviderEvent{Type: eventAgentMessage, Text: "working on it"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			observation, said := Dialect{}.Observe(test.event)
			if said != test.said {
				t.Fatalf("Observe() said = %t, want %t", said, test.said)
			}
			if !said {
				return
			}
			if observation.Answer != test.want {
				t.Fatalf("Observe() answer = %q, want %q", observation.Answer, test.want)
			}
			if !observation.ResetsAt.Equal(test.resets) {
				t.Fatalf("Observe() resets at %s, want %s", observation.ResetsAt, test.resets)
			}
		})
	}
}

// Every answer this dialect can give is one the contract names. A dialect that
// answered anything else would be describing something the harness has no
// response to.
func TestEveryAnswerIsOneTheContractNames(t *testing.T) {
	t.Parallel()

	for _, message := range []string{
		"You've hit your usage limit",
		"503 Service Unavailable",
		"404 model_not_found",
		"400 Bad Request",
		"the connection went away",
	} {
		observation, said := Dialect{}.Observe(failedTerminal(message))
		if !said || !observation.Answer.Valid() {
			t.Fatalf("Observe(%q) = %#v, want one of %s", message, observation, backendapi.DescribeAnswers())
		}
	}
	if (Dialect{}).Name() != sourceName {
		t.Fatalf("Name() = %q, want %q", (Dialect{}).Name(), sourceName)
	}
}

// What the dialect answered has to reach the result, because a refusal nothing
// wrote down is one the harness responds to as if the provider had said nothing.
func TestTheAnswerReachesTheInvocationsResult(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name    string
		message string
		holds   func(backendapi.RunResult) bool
	}{
		{
			name:    "a limit the run waits on",
			message: "You've hit your usage limit. Try again after 2026-08-27T18:00:00Z.",
			holds:   func(r backendapi.RunResult) bool { return r.UsageLimit != nil && !r.UsageLimit.ResetsAt.IsZero() },
		},
		{
			name:    "a model the caller answers by asking for another",
			message: `404 {"error":{"code":"model_not_found"}}`,
			holds:   func(r backendapi.RunResult) bool { return r.ModelUnavailable != nil },
		},
		{
			name:    "a death the harness relaunches into",
			message: "connection closed before the response completed",
			holds:   func(r backendapi.RunResult) bool { return r.TransientFailure != nil },
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var result backendapi.RunResult
			observation, said := Dialect{}.Observe(failedTerminal(test.message))
			if !said {
				t.Fatalf("Observe(%q) said nothing", test.message)
			}
			observation.Record(&result)
			if !test.holds(result) {
				t.Fatalf("the answer to %q did not reach the result: %#v", test.message, result)
			}
		})
	}
}

func failedTerminal(message string) backendapi.ProviderEvent {
	return backendapi.ProviderEvent{
		Type:     eventError,
		Subtype:  eventError,
		Text:     message,
		Terminal: true,
		Failed:   true,
	}
}
