// Package slack is the sink: one separate, long-running process that holds the
// connection to a Slack workspace, reads what the harness durably recorded, and
// posts it into one thread per topic.
//
// It is one process rather than a call from each run, and that is a credential
// boundary rather than a tidiness preference. Runs are ephemeral and concurrent,
// so posting from each of them would mean N racing writers to one thread map and
// a Slack token in every run's environment — and therefore in every agent's
// subprocess tree. With a sink, the harness posts and agents cannot, structurally
// rather than because they were asked not to.
//
// Nothing here may gate a run. Slack being down, slow, or misconfigured changes
// nothing about any work: the sink reads durable records from its own cursors, so
// an outage delays messages rather than losing them, and a crash between a post
// and a cursor advance repeats a message rather than dropping it. A rare
// duplicate is the right side of that trade, because the durable record is
// authoritative and Slack is a view of it.
package slack

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// DefaultBaseURL is the Slack Web API. It is a field on the client rather than a
// constant in the calls so tests exercise the real request shapes against a
// local server instead of mocking the transport.
const DefaultBaseURL = "https://slack.com/api"

const (
	// maxResponseBytes bounds what one API response may be read to. Slack's own
	// answers are small; anything near this bound is something other than Slack.
	maxResponseBytes = 1 << 20
	// requestTimeout bounds one call. The sink has nothing to gain by waiting
	// longer: what it did not post now it posts on its next pass, from the same
	// cursor.
	requestTimeout = 30 * time.Second
	// maxAttempts bounds the retries one call makes for a transient refusal —
	// a rate limit, or Slack's own server error. Beyond it the call fails and
	// the sink's loop backs off, which is the same waiting with the cursor
	// safely on disk.
	maxAttempts = 3
	// maxRetryAfter bounds how long a Retry-After is honored inside one call, so
	// a header asking for an hour parks the call rather than the process.
	maxRetryAfter = 2 * time.Minute
)

// API is the Slack Web API as the sink uses it. It holds both tokens because
// they authenticate different things — the bot token posts, the app token opens
// the Socket Mode connection — and neither is ever logged, recorded, or put on a
// message.
type API struct {
	botToken string
	appToken string
	baseURL  string
	client   *http.Client
	// sleep is how the client waits out a rate limit. It is injected so a test
	// can exercise the retry without spending the time.
	sleep func(ctx context.Context, d time.Duration) error
}

// NewAPI builds the client. Both tokens are required: a sink that cannot post is
// not a sink, and one that cannot open its connection cannot receive the replies
// the inbound half will read.
func NewAPI(botToken, appToken string) (*API, error) {
	if strings.TrimSpace(botToken) == "" {
		return nil, errors.New("SLACK_BOT_TOKEN is required")
	}
	if strings.TrimSpace(appToken) == "" {
		return nil, errors.New("SLACK_APP_TOKEN is required")
	}
	return &API{
		botToken: strings.TrimSpace(botToken),
		appToken: strings.TrimSpace(appToken),
		baseURL:  DefaultBaseURL,
		client:   &http.Client{Timeout: requestTimeout},
		sleep:    sleepContext,
	}, nil
}

// Identity is who the workspace says the sink is. It is what the sink reports on
// startup, because "connected, as this app, to this workspace" is the whole of
// what somebody following the setup document needs to see to know it worked.
type Identity struct {
	Team   string `json:"team"`
	TeamID string `json:"team_id"`
	User   string `json:"user"`
	UserID string `json:"user_id"`
}

// Identify asks the workspace who this token belongs to.
func (a *API) Identify(ctx context.Context) (Identity, error) {
	var response struct {
		apiResponse
		Identity
	}
	if err := a.call(ctx, "auth.test", a.botToken, nil, &response); err != nil {
		return Identity{}, err
	}
	return response.Identity, nil
}

// Message is one post. Username and the two icon fields are the per-message
// display identity that lets each persona speak under its own name in one
// channel, which is how a reader tells the speakers apart without every message
// having to name itself.
//
// The icon is two fields because Slack takes it as two: a shortcode goes in
// icon_emoji and an image goes in icon_url, and a call that sets both is a call
// that has said the same thing twice. Exactly one of them is set for any
// message; which one is decided where an avatar is turned into a post.
type Message struct {
	Channel   string
	Text      string
	ThreadTS  string
	Username  string
	IconEmoji string
	IconURL   string
}

// postRequest is the wire shape of chat.postMessage. The fields are omitted
// when empty so a top-level message is a top-level message rather than a reply
// to an empty thread.
type postRequest struct {
	Channel   string `json:"channel"`
	Text      string `json:"text"`
	ThreadTS  string `json:"thread_ts,omitempty"`
	Username  string `json:"username,omitempty"`
	IconEmoji string `json:"icon_emoji,omitempty"`
	IconURL   string `json:"icon_url,omitempty"`
}

// Post sends one message and reports the timestamp Slack gave it. That
// timestamp is what makes a thread: the first message for a topic is the thread
// the rest of that topic replies into.
func (a *API) Post(ctx context.Context, message Message) (string, error) {
	if strings.TrimSpace(message.Channel) == "" {
		return "", errors.New("a channel is required to post")
	}
	if strings.TrimSpace(message.Text) == "" {
		return "", errors.New("an empty message says nothing and is not posted")
	}
	var response struct {
		apiResponse
		TS string `json:"ts"`
	}
	request := postRequest{
		Channel:   message.Channel,
		Text:      message.Text,
		ThreadTS:  message.ThreadTS,
		Username:  message.Username,
		IconEmoji: message.IconEmoji,
		IconURL:   message.IconURL,
	}
	if err := a.call(ctx, "chat.postMessage", a.botToken, request, &response); err != nil {
		return "", err
	}
	if strings.TrimSpace(response.TS) == "" {
		return "", errors.New("chat.postMessage returned no timestamp, so nothing could be threaded onto it")
	}
	return response.TS, nil
}

// OpenConnection asks Slack for a Socket Mode websocket URL. The URL is
// single-use and short-lived by design, so it is obtained again on every
// reconnection rather than kept.
func (a *API) OpenConnection(ctx context.Context) (string, error) {
	var response struct {
		apiResponse
		URL string `json:"url"`
	}
	if err := a.call(ctx, "apps.connections.open", a.appToken, nil, &response); err != nil {
		return "", err
	}
	if strings.TrimSpace(response.URL) == "" {
		return "", errors.New("apps.connections.open returned no url")
	}
	return response.URL, nil
}

// apiResponse is the envelope every Slack method answers in. An `ok` of false
// is a refusal with a machine-readable reason, and it arrives with HTTP 200, so
// a client that only checked the status code would treat every refusal as a
// success.
type apiResponse struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
	// Warning is Slack's advice about a call that worked. It is carried so a
	// caller can report it and deliberately never treated as a failure.
	Warning string `json:"warning,omitempty"`
	// RetryAfter is how many seconds Slack says to wait, where it answers a rate
	// limit in the body rather than in the status line. It is read for the same
	// reason the header is: a wait Slack asked for is the only wait that is not a
	// guess, and guessing short is how an application that is already being
	// limited gets suppressed.
	RetryAfter int `json:"retry_after,omitempty"`
}

// Error is a refusal Slack named. It is a type rather than a string because the
// sink treats one of them differently from the rest: an invalid token or a
// channel the app is not in is a configuration problem no amount of retrying
// fixes, and saying so once is worth more than saying it every few seconds.
type Error struct {
	Method string
	Code   string
}

func (e Error) Error() string {
	return fmt.Sprintf("slack refused %s: %s", e.Method, e.Code)
}

// Permanent reports whether this refusal will still be a refusal on the next
// attempt. The set is deliberately small and every member of it names something
// an operator has to change: a token, an installation, a channel, a scope.
func (e Error) Permanent() bool {
	switch e.Code {
	case "invalid_auth", "not_authed", "account_inactive", "token_revoked",
		"token_expired", "missing_scope", "not_in_channel", "channel_not_found",
		"is_archived", "restricted_action", "org_login_required":
		return true
	default:
		return false
	}
}

// PermanentError reports whether an error is one Slack will keep giving. It is
// the question both loops ask before deciding how loudly to keep trying: the
// connection backs off to its longest wait, and the delivery loop says it once
// and then waits it out quietly. Neither stops, because what clears one of
// these is a person doing something in the workspace, and reporting has to
// resume by itself when they do.
func PermanentError(err error) bool {
	var refusal Error
	return errors.As(err, &refusal) && refusal.Permanent()
}

func (a *API) call(ctx context.Context, method, token string, body any, response any) error {
	var encoded []byte
	if body != nil {
		var err error
		encoded, err = json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode %s request: %w", method, err)
		}
	}

	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		wait, err := a.attempt(ctx, method, token, encoded, response)
		if err == nil {
			return nil
		}
		lastErr = err
		if wait <= 0 || attempt == maxAttempts {
			return err
		}
		if err := a.sleep(ctx, wait); err != nil {
			return errors.Join(lastErr, err)
		}
	}
	return lastErr
}

// attempt makes one call and reports how long to wait before repeating it. A
// zero wait means the failure is not one waiting helps with.
func (a *API) attempt(ctx context.Context, method, token string, encoded []byte, response any) (time.Duration, error) {
	var payload io.Reader
	if encoded != nil {
		payload = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, a.baseURL+"/"+method, payload)
	if err != nil {
		return 0, fmt.Errorf("build %s request: %w", method, err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	if encoded != nil {
		request.Header.Set("Content-Type", "application/json; charset=utf-8")
	}

	result, err := a.client.Do(request)
	if err != nil {
		// A transport failure is the network rather than Slack's answer, and it
		// is exactly what an outage looks like from here, so it is retried.
		return time.Second, fmt.Errorf("call %s: %w", method, err)
	}
	defer result.Body.Close()

	if result.StatusCode == http.StatusTooManyRequests {
		return retryAfter(result.Header.Get("Retry-After")), Error{Method: method, Code: "rate_limited"}
	}
	if result.StatusCode >= 500 {
		return time.Second, fmt.Errorf("call %s: slack returned %s", method, result.Status)
	}
	if result.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("call %s: slack returned %s", method, result.Status)
	}

	decoded, err := io.ReadAll(io.LimitReader(result.Body, maxResponseBytes))
	if err != nil {
		return time.Second, fmt.Errorf("read %s response: %w", method, err)
	}
	if err := json.Unmarshal(decoded, response); err != nil {
		return 0, fmt.Errorf("decode %s response: %w", method, err)
	}
	var envelope apiResponse
	if err := json.Unmarshal(decoded, &envelope); err != nil {
		return 0, fmt.Errorf("decode %s response: %w", method, err)
	}
	if !envelope.OK {
		code := envelope.Error
		if strings.TrimSpace(code) == "" {
			code = "unknown"
		}
		refusal := Error{Method: method, Code: code}
		if refusal.Permanent() {
			return 0, refusal
		}
		if code == "rate_limited" && envelope.RetryAfter > 0 {
			return boundedWait(envelope.RetryAfter), refusal
		}
		return time.Second, refusal
	}
	return 0, nil
}

// retryAfter reads Slack's own answer to how long to wait, bounded. A missing
// or unreadable header is a second, which is long enough not to hammer and
// short enough that the sink is not parked on a guess.
func retryAfter(header string) time.Duration {
	seconds, err := strconv.Atoi(strings.TrimSpace(header))
	if err != nil {
		return time.Second
	}
	return boundedWait(seconds)
}

// boundedWait is a wait Slack asked for, in seconds, held inside what one call
// may be parked for. A wait it did not ask for at all is a second.
func boundedWait(seconds int) time.Duration {
	if seconds <= 0 {
		return time.Second
	}
	wait := time.Duration(seconds) * time.Second
	if wait > maxRetryAfter {
		return maxRetryAfter
	}
	return wait
}

func sleepContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
