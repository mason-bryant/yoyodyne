package conformance

// The providers this build ships an adapter for, and each provider's own words
// for the conditions the suite names.
//
// The samples are this provider's spelling and not the harness's: what an
// adapter is being asked is whether it turns the words its provider actually
// uses into the answer every other adapter reaches for the same condition. Where
// a provider says one condition two ways — a status on one run and prose on the
// next — both shapes are here, because a condition read one way and not the
// other is a divergence inside a single adapter and it costs exactly what a
// divergence between two of them costs.
//
// Provenance is the same as the dialects', and no better: the shapes below are
// the ones those dialects were written against, read from a recorded run where
// there is one and from the provider's documented answers where there is not.
// The first recorded occurrence of a shape that is here on documentation alone
// is the evidence that should replace it.

import (
	"encoding/json"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// BuiltInAdapters is every adapter this build ships, with its cases. An adapter
// added to the registry and not to this list is what Uncovered reports.
func BuiltInAdapters() []Adapter {
	return []Adapter{claudeCodeAdapter(), codexAdapter()}
}

// claudeCodeAdapter is Claude Code's side of the suite.
func claudeCodeAdapter() Adapter {
	descriptor, _ := backend.BuiltInDescriptor(domain.BackendClaudeCode)
	return Adapter{
		Descriptor: descriptor,
		Samples: map[Condition][]Sample{
			CapacityExhausted: {{
				// This provider reports its limits on an event of their own rather
				// than on the terminal, so the limit is said before the invocation
				// ends and the ending says nothing about it. The stream here stops
				// after the event and the process exits non-zero, which is the shape
				// that assumes least about what the CLI does next: whatever terminal
				// it does or does not write, the window is what the harness has to be
				// left holding.
				Name:     "a five-hour window that is refusing work",
				Stream:   lines(claudeInit, claudeRateLimit(`{"status":"rejected","rateLimitType":"five_hour","resetsAt":1788000000}`)),
				ExitCode: 1,
			}},
			ModelUnavailable: {{
				// The API's own not-found answer, whose body names the model, carried
				// onto the CLI's terminal API-error message the way the overload's is.
				Name: "a pinned version this provider has not got",
				Stream: lines(claudeInit, claudeTerminal("api_error",
					`API Error: 404 {"type":"error","error":{"type":"not_found_error","message":"model: claude-opus-5-20260401"}}`)),
				ExitCode: 1,
			}},
			AuthenticationRejected: {
				{
					Name:     "an account the API would not accept, as a status",
					Stream:   lines(claudeInit, claudeTerminal("api_error", "API Error: 401 Unauthorized")),
					ExitCode: 1,
				},
				{
					// The same condition in the CLI's own words, which quote no status
					// at all. It is the shape a logged-out account actually produces,
					// and reading it as weather is a run relaunching into an answer no
					// relaunch can change.
					Name:     "an account that is not logged in, in the CLI's own words",
					Stream:   lines(claudeInit, claudeTerminal("api_error", "Not logged in")),
					ExitCode: 1,
				},
				{
					// The one recorded shape: a home nobody has logged into, asked on
					// 2026-09-19 with Claude Code 2.1.276, ends on a terminal with
					// terminal_reason "api_error", the title paired with its remedy,
					// and nothing on stderr.
					Name:     "an account that is not logged in, as the CLI actually reports it",
					Stream:   lines(claudeInit, claudeTerminal("api_error", "Not logged in · Please run /login")),
					ExitCode: 1,
				},
				{
					// The shape yoyodyne-ifd.377 could not see: a CLI that refuses the
					// login before it writes any envelope has only prose to say so
					// with, here on stderr with a stream that has nothing in it.
					// Read from documentation rather than a recording — the recorded
					// refusal above arrived on a terminal — because the cost of
					// missing it is the 2026-09-17 stall replayed: a process failure
					// nobody classified is relaunched into the same login until the
					// budget is spent.
					Name:     "a login refused on stderr before any envelope was written",
					Stderr:   "Not logged in · Please run /login\n",
					ExitCode: 1,
				},
				{
					// The same refusal written to stdout as plain text, where the
					// stream should have been: the gap yoyodyne-ifd.393 reported
					// after closing the stderr one. Until yoyodyne-ifd.400 this was
					// a stream the adapter could not read at all, which failed the
					// invocation before stderr was consulted.
					Name:     "a login refused as plain text on stdout before any envelope was written",
					Stream:   "Not logged in · Please run /login\n",
					ExitCode: 1,
				},
			},
			ProviderUnreachable: {
				{
					// The CLI's own words when nothing answers at the API, recorded on
					// the runs that died in the 2026-09-15..18 outage. It quotes no
					// status because nothing answered, which is what separates it from
					// the mid-reply drop below: that one reached the provider.
					Name:     "nothing answering at the API, in the CLI's own words",
					Stream:   lines(claudeInit, claudeTerminal("api_error", "API Error: Can't reach the API server")),
					ExitCode: 1,
				},
				{
					// The transport's own words for the same condition, which the CLI
					// carries onto its terminal when Node reports the failure itself.
					Name:     "a name that does not resolve",
					Stream:   lines(claudeInit, claudeTerminal("api_error", "API Error: getaddrinfo ENOTFOUND api.anthropic.com")),
					ExitCode: 1,
				},
				{
					// The same words on stderr with no envelope before them, for the
					// reason the login has a stderr sample: a process that died
					// before writing a terminal has said what it had to say there.
					Name:     "nothing answering, said on stderr before any envelope was written",
					Stderr:   "API Error: Can't reach the API server\n",
					ExitCode: 1,
				},
				{
					Name:     "nothing answering, said as plain text on stdout before any envelope was written",
					Stream:   "API Error: Can't reach the API server\n",
					ExitCode: 1,
				},
			},
			NetworkFailure: {{
				// Byte for byte what the provider CLI wrote on the run that died
				// developing yoyodyne-ifd.68.2 on 2026-08-19. It quotes no status
				// because nothing answered.
				Name: "a connection that went away mid-reply",
				Stream: lines(claudeInit, claudeTerminal("api_error",
					"API Error: Connection closed mid-response. The response above may be incomplete.")),
				ExitCode: 1,
			}},
			WorkRefused: {{
				// An ending that is not an API error at all is this provider having
				// read the request and declined it, which stands however often it is
				// asked.
				Name:     "an ending that judged the work rather than the environment",
				Stream:   lines(claudeInit, claudeTerminal("refusal", "I cannot help with that")),
				ExitCode: 1,
			}},
		},
	}
}

// codexAdapter is Codex's side of the suite. This provider has one error channel
// in its stream and says everything on it, which is why nearly every sample
// below is the same envelope carrying different words — and why the words are
// the whole of what its dialect has to read. The exceptions are the refusals a
// CLI can make before it writes any event, which arrive as prose on stderr or
// on stdout and are sampled on both.
func codexAdapter() Adapter {
	descriptor, _ := backend.BuiltInDescriptor(domain.BackendCodex)
	return Adapter{
		Descriptor: descriptor,
		Samples: map[Condition][]Sample{
			CapacityExhausted: {
				{
					Name:     "a usage limit naming when it lifts",
					Stream:   lines(codexSessionConfigured, codexError("You've hit your usage limit. Try again after 2027-01-04T18:00:00Z.")),
					ExitCode: 1,
				},
				{
					// The same condition reported as a status. 429 is a client status
					// as well as the shape a limit arrives in, so an adapter that read
					// the class before the limit would answer this as a refusal that
					// stands and never wait at all.
					Name:     "a usage limit reported as a status",
					Stream:   lines(codexSessionConfigured, codexError("request failed with 429 Too Many Requests")),
					ExitCode: 1,
				},
			},
			ModelUnavailable: {{
				Name:     "a model this provider has not got",
				Stream:   lines(codexSessionConfigured, codexError(`404 {"error":{"code":"model_not_found","message":"model: gpt-5-2026-01-01"}}`)),
				ExitCode: 1,
			}},
			AuthenticationRejected: {
				{
					Name:     "an account the API would not accept",
					Stream:   lines(codexSessionConfigured, codexError("401 Unauthorized: check your credentials")),
					ExitCode: 1,
				},
				{
					// A CLI that refuses the login before it writes any event has
					// only prose to say so with. No recorded Codex process has done
					// so; the words are the CLI's title for the condition and the
					// remedy it names, and the cost of not sampling them is the
					// 2026-09-17 stall replayed on this provider.
					Name:     "a login refused on stderr before any event was written",
					Stderr:   "Not logged in. Run `codex login` to authenticate.\n",
					ExitCode: 1,
				},
				{
					Name:     "a login refused as plain text on stdout before any event was written",
					Stream:   "Not logged in. Run `codex login` to authenticate.\n",
					ExitCode: 1,
				},
			},
			ProviderUnreachable: {
				{
					// This provider has one error channel, so the transport's own words
					// arrive on it exactly as an API's do. No recorded Codex stream carries
					// one; the shape is the transport's documented answer for a name that
					// does not resolve.
					Name:     "a name that does not resolve",
					Stream:   lines(codexSessionConfigured, codexError("error sending request: dns error: failed to lookup address information")),
					ExitCode: 1,
				},
				{
					// The same words as prose on either plain channel, for the
					// reason the login has them: a process that died before writing
					// an event has said what it had to say there.
					Name:     "nothing answering, said on stderr before any event was written",
					Stderr:   "error sending request: dns error: failed to lookup address information\n",
					ExitCode: 1,
				},
				{
					Name:     "nothing answering, said as plain text on stdout before any event was written",
					Stream:   "error sending request: dns error: failed to lookup address information\n",
					ExitCode: 1,
				},
			},
			NetworkFailure: {{
				Name:     "a stream that stopped before the response completed",
				Stream:   lines(codexSessionConfigured, codexError("connection closed before the response completed")),
				ExitCode: 1,
			}},
			WorkRefused: {{
				// The provider read the request and would not serve it, which it
				// reports as a status describing the request rather than as anything
				// about its own servers.
				Name: "a request the provider read and would not serve",
				Stream: lines(codexSessionConfigured, codexError(
					`400 {"error":{"type":"invalid_request_error","message":"your request was rejected as a result of our safety system"}}`)),
				ExitCode: 1,
			}},
		},
	}
}

// claudeInit is the event this provider opens a stream with, carried on every
// sample so that each one is a stream an invocation could actually have produced
// rather than a single line in isolation.
const claudeInit = `{"type":"system","subtype":"init","session_id":"session-1","model":"claude-test"}`

// codexSessionConfigured is the same thing in the other provider's vocabulary.
const codexSessionConfigured = `{"id":"0","msg":{"type":"session_configured","session_id":"session-1","model":"gpt-5"}}`

// claudeTerminal is the envelope this provider ends a failed invocation with.
// It is encoded rather than written out because a provider message carrying
// quotes — the not-found body does — has to be escaped the way the CLI escapes
// it rather than by hand.
func claudeTerminal(reason, message string) string {
	return encode(map[string]any{
		"type":            "result",
		"subtype":         "error",
		"session_id":      "session-1",
		"is_error":        true,
		"terminal_reason": reason,
		"result":          message,
	})
}

// claudeRateLimit is the event this provider reports a limit on, carrying the
// payload whole the way the CLI does.
func claudeRateLimit(info string) string {
	return encode(map[string]any{
		"type":            "rate_limit_event",
		"session_id":      "session-1",
		"rate_limit_info": json.RawMessage(info),
	})
}

// codexError is this provider's whole error channel: one envelope carrying an
// event that ends the invocation, with the provider's prose as the only evidence
// of what happened.
func codexError(message string) string {
	return encode(map[string]any{
		"id":  "0",
		"msg": map[string]any{"type": "error", "message": message},
	})
}

// encode writes one stream line. Nothing built above can fail to encode, and a
// line that somehow did would be an empty one the runner skips rather than a
// sample that quietly asserts something else.
func encode(envelope map[string]any) string {
	line, err := json.Marshal(envelope)
	if err != nil {
		return ""
	}
	return string(line)
}

func lines(envelopes ...string) string {
	return strings.Join(envelopes, "\n") + "\n"
}
