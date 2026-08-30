package slack

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// The two tokens authenticate different things, and swapping them is the
// failure that looks like a workspace problem: posting with the app token is
// refused, and opening a connection with the bot token is refused, and neither
// says which token was wrong.
func TestEachCallCarriesTheTokenThatAuthenticatesIt(t *testing.T) {
	t.Parallel()

	var seen []string
	api := newTestAPI(t, func(writer http.ResponseWriter, request *http.Request) {
		seen = append(seen, request.URL.Path+" "+request.Header.Get("Authorization"))
		switch {
		case strings.HasSuffix(request.URL.Path, "chat.postMessage"):
			writeJSON(writer, map[string]any{"ok": true, "ts": "1755.0001"})
		case strings.HasSuffix(request.URL.Path, "apps.connections.open"):
			writeJSON(writer, map[string]any{"ok": true, "url": "wss://slack.test/link"})
		default:
			writeJSON(writer, map[string]any{"ok": true})
		}
	})

	if _, err := api.Post(context.Background(), Message{Channel: "C1", Text: "said"}); err != nil {
		t.Fatalf("Post() error = %v", err)
	}
	if _, err := api.OpenConnection(context.Background()); err != nil {
		t.Fatalf("OpenConnection() error = %v", err)
	}
	want := []string{"/api/chat.postMessage Bearer xoxb-test", "/api/apps.connections.open Bearer xapp-test"}
	if len(seen) != len(want) || seen[0] != want[0] || seen[1] != want[1] {
		t.Fatalf("calls = %q, want %q", seen, want)
	}
}

// A workspace that already agrees is not a refusal. Adding a mark that is
// already on a message and taking off one that is not both leave the message in
// exactly the state the caller asked for, and a sink interrupted between two
// reaction calls settles by repeating itself — so neither may be an error, and
// neither is worth a retry.
func TestAReactionTheWorkspaceAlreadyAgreesWithIsNotAFailure(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name    string
		code    string
		perform func(*API) error
	}{
		{
			name: "a mark that is already there",
			code: "already_reacted",
			perform: func(api *API) error {
				return api.React(context.Background(), "C1", "1755.0001", "eyes")
			},
		},
		{
			name: "a mark that is already off",
			code: "no_reaction",
			perform: func(api *API) error {
				return api.Unreact(context.Background(), "C1", "1755.0001", "eyes")
			},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			attempts := 0
			api := newTestAPI(t, func(writer http.ResponseWriter, _ *http.Request) {
				attempts++
				writeJSON(writer, map[string]any{"ok": false, "error": testCase.code})
			})
			if err := testCase.perform(api); err != nil {
				t.Fatalf("error = %v, want the workspace already agreeing read as success", err)
			}
			if attempts != 1 {
				t.Fatalf("attempts = %d, want an answer that cannot change asked for once", attempts)
			}
		})
	}
}

// A reaction names one message in one channel: without the timestamp there is no
// message to mark, and a mark on the channel is not a mark on the thread.
func TestAReactionNamesTheMessageItMarks(t *testing.T) {
	t.Parallel()

	var marked reactionRequest
	api := newTestAPI(t, func(writer http.ResponseWriter, incoming *http.Request) {
		body, _ := io.ReadAll(incoming.Body)
		if err := json.Unmarshal(body, &marked); err != nil {
			t.Errorf("Unmarshal() error = %v", err)
		}
		writeJSON(writer, map[string]any{"ok": true})
	})

	if err := api.React(context.Background(), "C1", "1755.0001", "white_check_mark"); err != nil {
		t.Fatalf("React() error = %v", err)
	}
	want := reactionRequest{Channel: "C1", Timestamp: "1755.0001", Name: "white_check_mark"}
	if marked != want {
		t.Fatalf("reaction = %#v, want %#v", marked, want)
	}
	if err := api.React(context.Background(), "C1", "", "white_check_mark"); err == nil {
		t.Fatal("React() with no message accepted, want a mark with nothing to go on refused")
	}
}

// A post carries the thread it belongs in and the identity it speaks under.
// Losing the thread timestamp is what turns one thread per topic into a channel
// of loose messages.
func TestAPostCarriesItsThreadAndItsSpeaker(t *testing.T) {
	t.Parallel()

	var request postRequest
	api := newTestAPI(t, func(writer http.ResponseWriter, incoming *http.Request) {
		body, _ := io.ReadAll(incoming.Body)
		if err := json.Unmarshal(body, &request); err != nil {
			t.Errorf("Unmarshal() error = %v", err)
		}
		if contentType := incoming.Header.Get("Content-Type"); !strings.HasPrefix(contentType, "application/json") {
			t.Errorf("Content-Type = %q, want JSON", contentType)
		}
		writeJSON(writer, map[string]any{"ok": true, "ts": "1755.0002"})
	})

	ts, err := api.Post(context.Background(), Message{
		Channel: "C1", Text: "the checks passed", ThreadTS: "1755.0001",
		Username: "developer", IconEmoji: ":hammer_and_wrench:",
	})
	if err != nil {
		t.Fatalf("Post() error = %v", err)
	}
	if ts != "1755.0002" {
		t.Fatalf("Post() = %q, want the timestamp Slack gave the message", ts)
	}
	if request.ThreadTS != "1755.0001" || request.Username != "developer" || request.IconEmoji != ":hammer_and_wrench:" {
		t.Fatalf("request = %#v, want the thread and the speaker carried", request)
	}
}

// A direct message is two calls, which is what Slack documents: the conversation
// with the member is opened, and the id it answers with is what is posted to.
// That id is not the member's own, so a client that skipped the open and posted
// to the member id would be relying on undocumented behaviour for the one message
// that exists because nobody is watching the channel.
func TestADirectConversationIsOpenedBeforeAnythingIsSaidInIt(t *testing.T) {
	t.Parallel()

	var opened openConversationRequest
	api := newTestAPI(t, func(writer http.ResponseWriter, incoming *http.Request) {
		body, _ := io.ReadAll(incoming.Body)
		if err := json.Unmarshal(body, &opened); err != nil {
			t.Errorf("Unmarshal() error = %v", err)
		}
		writeJSON(writer, map[string]any{"ok": true, "channel": map[string]any{"id": "D0OPERATOR"}})
	})

	conversation, err := api.OpenConversation(context.Background(), "U0OPERATOR")
	if err != nil {
		t.Fatalf("OpenConversation() error = %v", err)
	}
	if conversation != "D0OPERATOR" {
		t.Fatalf("OpenConversation() = %q, want the channel Slack answered with", conversation)
	}
	if opened.Users != "U0OPERATOR" {
		t.Fatalf("opened = %#v, want the member the conversation is with", opened)
	}
	if _, err := api.OpenConversation(context.Background(), " "); err == nil {
		t.Fatal("OpenConversation() with no member accepted, want a conversation with nobody refused")
	}
}

// A workspace that answers the open without a channel has given the caller
// nowhere to post, which must not read as a conversation that was opened.
func TestAnOpenThatNamesNoChannelIsNotAConversation(t *testing.T) {
	t.Parallel()

	api := newTestAPI(t, func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(writer, map[string]any{"ok": true})
	})
	if _, err := api.OpenConversation(context.Background(), "U0OPERATOR"); err == nil {
		t.Fatal("OpenConversation() = nil, want an answer with nowhere to post reported")
	}
}

// Slack refuses with HTTP 200 and `ok: false`. A client that read the status
// code alone would record every refusal as a message that was posted.
func TestARefusalIsNotASuccessfulPost(t *testing.T) {
	t.Parallel()

	api := newTestAPI(t, func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(writer, map[string]any{"ok": false, "error": "not_in_channel"})
	})
	_, err := api.Post(context.Background(), Message{Channel: "C1", Text: "said"})
	if err == nil {
		t.Fatal("Post() = nil, want the refusal reported")
	}
	// A channel the app was never invited to is not fixed by trying again, and
	// a sink that retried it would say the same thing every few seconds forever.
	if !PermanentError(err) {
		t.Fatalf("PermanentError(%v) = false, want a refusal an operator has to fix", err)
	}
}

// A rate limit is Slack asking for a pause rather than refusing the work, so it
// is waited out inside the call rather than turned into a failed pass.
func TestARateLimitIsWaitedOutRatherThanFailed(t *testing.T) {
	t.Parallel()

	attempts := 0
	api := newTestAPI(t, func(writer http.ResponseWriter, _ *http.Request) {
		attempts++
		if attempts == 1 {
			writer.Header().Set("Retry-After", "2")
			writer.WriteHeader(http.StatusTooManyRequests)
			return
		}
		writeJSON(writer, map[string]any{"ok": true, "ts": "1755.0003"})
	})
	var waited time.Duration
	api.sleep = func(_ context.Context, d time.Duration) error {
		waited += d
		return nil
	}

	if _, err := api.Post(context.Background(), Message{Channel: "C1", Text: "said"}); err != nil {
		t.Fatalf("Post() error = %v", err)
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d, want the call repeated once the wait was over", attempts)
	}
	if waited != 2*time.Second {
		t.Fatalf("waited = %s, want the wait Slack asked for", waited)
	}
	// A header asking for an hour must park the call rather than the process.
	if bounded := retryAfter("100000"); bounded != maxRetryAfter {
		t.Fatalf("retryAfter() = %s, want it bounded at %s", bounded, maxRetryAfter)
	}
}

// Slack answers some rate limits in the body rather than in the status line,
// with `ok` false and the wait beside it. It is the same instruction and it is
// honored the same way: a wait Slack asked for is the only one that is not a
// guess, and guessing short is how an application already being limited has its
// messages suppressed.
func TestARateLimitAnsweredInTheBodyIsWaitedOutForAsLongAsSlackAsked(t *testing.T) {
	t.Parallel()

	attempts := 0
	api := newTestAPI(t, func(writer http.ResponseWriter, _ *http.Request) {
		attempts++
		if attempts == 1 {
			writeJSON(writer, map[string]any{"ok": false, "error": "rate_limited", "retry_after": 7})
			return
		}
		writeJSON(writer, map[string]any{"ok": true, "ts": "1755.0004"})
	})
	var waited time.Duration
	api.sleep = func(_ context.Context, d time.Duration) error {
		waited += d
		return nil
	}

	if _, err := api.Post(context.Background(), Message{Channel: "C1", Text: "said"}); err != nil {
		t.Fatalf("Post() error = %v", err)
	}
	if waited != 7*time.Second {
		t.Fatalf("waited = %s, want the wait Slack asked for in the body", waited)
	}
	// A body asking for an hour is bounded exactly as a header asking for one is.
	if bounded := boundedWait(100000); bounded != maxRetryAfter {
		t.Fatalf("boundedWait() = %s, want it bounded at %s", bounded, maxRetryAfter)
	}
}

// An empty message says nothing, and a message with nowhere to go cannot be
// posted. Both are refused before a request is made rather than by Slack.
func TestNothingIsPostedWithoutAChannelAndSomethingToSay(t *testing.T) {
	t.Parallel()

	api := newTestAPI(t, func(writer http.ResponseWriter, _ *http.Request) {
		t.Error("no request should have been made")
		writeJSON(writer, map[string]any{"ok": true, "ts": "1"})
	})
	if _, err := api.Post(context.Background(), Message{Text: "said"}); err == nil {
		t.Fatal("Post() without a channel = nil, want a refusal")
	}
	if _, err := api.Post(context.Background(), Message{Channel: "C1", Text: "  "}); err == nil {
		t.Fatal("Post() with an empty body = nil, want a refusal")
	}
	if _, err := NewAPI("", "xapp-test"); err == nil {
		t.Fatal("NewAPI() without a bot token = nil, want a refusal")
	}
	if _, err := NewAPI("xoxb-test", ""); err == nil {
		t.Fatal("NewAPI() without an app token = nil, want a refusal")
	}
}

// newTestAPI builds a client whose requests are served by a handler in this
// process. The sandbox this suite runs in has no network at all, so the
// transport is replaced rather than the server bound to a port: the request
// shapes, the headers, and the decoding are all the real ones.
func newTestAPI(t *testing.T, handler http.HandlerFunc) *API {
	t.Helper()
	api, err := NewAPI("xoxb-test", "xapp-test")
	if err != nil {
		t.Fatalf("NewAPI() error = %v", err)
	}
	api.client = &http.Client{Transport: handlerTransport{handler: handler}}
	api.sleep = func(context.Context, time.Duration) error { return nil }
	return api
}

type handlerTransport struct {
	handler http.Handler
}

func (t handlerTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	recorder := httptest.NewRecorder()
	t.handler.ServeHTTP(recorder, request)
	return recorder.Result(), nil
}

func writeJSON(writer http.ResponseWriter, value any) {
	writer.Header().Set("Content-Type", "application/json")
	json.NewEncoder(writer).Encode(value)
}
