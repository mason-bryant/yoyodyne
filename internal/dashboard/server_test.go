package dashboard

// These tests are the security evidence the dashboard hands its reviewer. Each
// convention the design established is driven from the outside of the handler:
// a request with the wrong provenance or no credential is refused with nothing
// of the read model on it, every response carries the policy, and a value that
// reaches HTML reaches it as text.
//
// They drive the handler in process rather than over a socket, as every other
// HTTP test in the repository does, because the sandbox a check runs in does
// not grant a listener. The one test that binds skips where it is refused.

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/readmodel"
)

// injected is the text every test plants where an unescaped value would run.
const injected = `<script>alert("owned")</script>`

// port is the port the handler is told it is bound on.
const port = "45123"

// stubReader is a read model that answers with what the test put in it.
type stubReader struct {
	ready    error
	standing readmodel.Standing
	failure  error
}

func (r stubReader) Ready(context.Context) error { return r.ready }

func (r stubReader) Standing(context.Context) (readmodel.Standing, error) {
	return r.standing, r.failure
}

// world is one server, bound in name only, and the handler that answers for it.
type world struct {
	t       *testing.T
	server  *Server
	handler http.Handler
}

func serve(t *testing.T, reader Reader) *world {
	t.Helper()
	server, err := New("yoyodyne", reader)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	server.bound(45123)
	return &world{t: t, server: server, handler: server.Handler()}
}

// request makes one request with the bound Host, shaped as the test wants, and
// returns the response with its body read.
func (w *world) request(method, path string, body io.Reader, shape func(*http.Request)) (*http.Response, string) {
	w.t.Helper()
	request := httptest.NewRequest(method, "http://127.0.0.1:"+port+path, body)
	if shape != nil {
		shape(request)
	}
	recorder := httptest.NewRecorder()
	w.handler.ServeHTTP(recorder, request)
	response := recorder.Result()
	defer response.Body.Close()
	read, err := io.ReadAll(response.Body)
	if err != nil {
		w.t.Fatalf("read %s: %v", path, err)
	}
	return response, string(read)
}

func (w *world) get(path string, shape func(*http.Request)) (*http.Response, string) {
	w.t.Helper()
	return w.request(http.MethodGet, path, nil, shape)
}

func bearer(token string) func(*http.Request) {
	return func(request *http.Request) { request.Header.Set("Authorization", "Bearer "+token) }
}

func withHost(host string) func(*http.Request) {
	return func(request *http.Request) { request.Host = host }
}

func withOrigin(origin string) func(*http.Request) {
	return func(request *http.Request) { request.Header.Set("Origin", origin) }
}

func all(shapes ...func(*http.Request)) func(*http.Request) {
	return func(request *http.Request) {
		for _, shape := range shapes {
			shape(request)
		}
	}
}

// standingWith is a read model carrying the given text in every place a value
// a person wrote can reach: a title, a refusal, and an attention entry.
func standingWith(text string) readmodel.Standing {
	return readmodel.Standing{
		ObservedAt:   time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC),
		Running:      []readmodel.RunningRun{},
		Working:      []readmodel.WorkingTurn{},
		NotStartable: []readmodel.Refused{{WorkItemID: "yoyodyne-ifd.1", Title: text, Reason: text}},
		NeedsHuman:   []readmodel.Attention{{What: text, Whose: "the operator's"}},
	}
}

// The verb's whole contract from the outside: a token is generated, and with
// it the read model is served as JSON and as the page shell.
func TestServesTheReadModelAsJSONAndAsTheShellToTheToken(t *testing.T) {
	t.Parallel()
	w := serve(t, stubReader{standing: standingWith("ordinary title")})

	if len(w.server.Token()) != tokenBytes*2 {
		t.Fatalf("token %q is not %d hex characters", w.server.Token(), tokenBytes*2)
	}

	response, body := w.get("/api/standing", bearer(w.server.Token()))
	if response.StatusCode != http.StatusOK {
		t.Fatalf("JSON with the token: %d %s", response.StatusCode, body)
	}
	if !strings.HasPrefix(response.Header.Get("Content-Type"), "application/json") {
		t.Fatalf("JSON content type %q", response.Header.Get("Content-Type"))
	}
	if !strings.Contains(body, `"observed_at":"2026-09-18T12:00:00Z"`) || !strings.Contains(body, "ordinary title") {
		t.Fatalf("JSON does not carry the standing: %s", body)
	}

	response, body = w.get("/", bearer(w.server.Token()))
	if response.StatusCode != http.StatusOK {
		t.Fatalf("shell with the token: %d %s", response.StatusCode, body)
	}
	if !strings.Contains(body, `<main data-state="loading">`) || !strings.Contains(body, `/assets/dashboard.js`) {
		t.Fatalf("shell is not the page with its states: %s", body)
	}
	// The shell is a shell: nothing of the read model is in it, so a page served
	// to the wrong hands by some later mistake would still carry nothing.
	if strings.Contains(body, "ordinary title") {
		t.Fatalf("the shell carries read-model text: %s", body)
	}
	for _, asset := range []string{"/assets/dashboard.js", "/assets/dashboard.css"} {
		if response, body := w.get(asset, bearer(w.server.Token())); response.StatusCode != http.StatusOK {
			t.Fatalf("%s: %d %s", asset, response.StatusCode, body)
		}
	}
}

// Listen binds loopback and reports a URL that carries no token. The bind is
// what a sandbox refuses, so this skips rather than fails where it is refused.
func TestListenBindsLoopbackWithoutTheTokenInTheURL(t *testing.T) {
	t.Parallel()
	server, err := New("yoyodyne", stubReader{})
	if err != nil {
		t.Fatal(err)
	}
	bound, err := server.Listen(0)
	if err != nil {
		if strings.Contains(err.Error(), "operation not permitted") {
			t.Skipf("this environment grants no listener: %v", err)
		}
		t.Fatalf("Listen: %v", err)
	}
	defer server.listener.Close()
	parsed, err := url.Parse(bound)
	if err != nil {
		t.Fatalf("parse %q: %v", bound, err)
	}
	if parsed.Hostname() != "127.0.0.1" || parsed.Port() == "" || parsed.Port() == "0" || strings.Contains(bound, server.Token()) {
		t.Fatalf("URL %q is not loopback on a real port without the token", bound)
	}
	if !server.hosts["127.0.0.1:"+parsed.Port()] || !server.origins["http://localhost:"+parsed.Port()] {
		t.Fatalf("the bound names do not follow the port: %v %v", server.hosts, server.origins)
	}
}

// A request with no token is refused, on every route that serves anything. The
// page's refusal is the sign-in form, because that is how a browser gets a
// token; the API's is JSON; neither carries a byte of the read model.
func TestRefusesAMissingToken(t *testing.T) {
	t.Parallel()
	w := serve(t, stubReader{standing: standingWith("secret title")})

	response, body := w.get("/api/standing", nil)
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("JSON without a token: %d %s", response.StatusCode, body)
	}
	if !strings.Contains(body, `"error"`) || strings.Contains(body, "secret title") {
		t.Fatalf("JSON refusal is not a bare refusal: %s", body)
	}

	response, body = w.get("/", nil)
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("shell without a token: %d %s", response.StatusCode, body)
	}
	if !strings.Contains(body, `action="/session"`) || strings.Contains(body, "dashboard.js") || strings.Contains(body, "secret title") {
		t.Fatalf("shell refusal is not the sign-in form alone: %s", body)
	}

	if response, _ := w.get("/assets/dashboard.js", nil); response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("script without a token: %d", response.StatusCode)
	}
	// A wrong token is a missing one. Same length as the real one, so the
	// refusal is not a length check.
	wrong := strings.Repeat("0", len(w.server.Token()))
	if response, _ := w.get("/api/standing", bearer(wrong)); response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("JSON with a wrong token: %d", response.StatusCode)
	}
	// Nor does the token work in the URL, which is where it must never be.
	if response, _ := w.get("/api/standing?token="+w.server.Token(), nil); response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("JSON with the token in the URL: %d", response.StatusCode)
	}
}

// A Host that is not the bound address is refused before anything else is
// looked at, token or no token.
func TestRefusesAForeignHost(t *testing.T) {
	t.Parallel()
	w := serve(t, stubReader{standing: standingWith("secret title")})

	for _, host := range []string{"dashboard.example.com", "127.0.0.1:1", "127.0.0.1", "evil.test:" + port, ""} {
		for _, path := range []string{"/", "/api/standing"} {
			response, body := w.get(path, all(bearer(w.server.Token()), withHost(host)))
			if response.StatusCode != http.StatusForbidden {
				t.Fatalf("Host %q on %s: %d %s", host, path, response.StatusCode, body)
			}
			if strings.Contains(body, "secret title") || (host != "" && strings.Contains(body, host)) {
				t.Fatalf("Host %q on %s: refusal carries the read model or reflects the host: %s", host, path, body)
			}
		}
	}
	// The two names for the bound address are both the bound address.
	for _, host := range []string{"127.0.0.1:" + port, "localhost:" + port} {
		response, body := w.get("/api/standing", all(bearer(w.server.Token()), withHost(host)))
		if response.StatusCode != http.StatusOK {
			t.Fatalf("Host %q: %d %s", host, response.StatusCode, body)
		}
	}
}

// An Origin that is not this origin is a page elsewhere scripting requests at
// this port, and is refused whatever credential rides along with it.
func TestRefusesAForeignOrigin(t *testing.T) {
	t.Parallel()
	w := serve(t, stubReader{standing: standingWith("secret title")})

	for _, origin := range []string{"http://evil.test", "https://127.0.0.1:" + port, "http://127.0.0.1:1", "null"} {
		response, body := w.get("/api/standing", all(bearer(w.server.Token()), withOrigin(origin)))
		if response.StatusCode != http.StatusForbidden {
			t.Fatalf("Origin %q: %d %s", origin, response.StatusCode, body)
		}
		if strings.Contains(body, "secret title") || strings.Contains(body, origin) {
			t.Fatalf("Origin %q: refusal carries the read model or reflects the origin: %s", origin, body)
		}
	}
	response, body := w.get("/api/standing", all(bearer(w.server.Token()), withOrigin("http://127.0.0.1:"+port)))
	if response.StatusCode != http.StatusOK {
		t.Fatalf("own origin: %d %s", response.StatusCode, body)
	}
}

// Every response carries the policy, and the policy allows nothing from
// anywhere but this origin, no inline script, and no framing — refusals
// included, because a refusal is a page a browser renders too.
func TestEveryResponseCarriesTheContentSecurityPolicy(t *testing.T) {
	t.Parallel()
	w := serve(t, stubReader{standing: standingWith("title")})

	for _, probe := range []struct {
		path  string
		shape func(*http.Request)
	}{
		{"/", bearer(w.server.Token())},
		{"/api/standing", bearer(w.server.Token())},
		{"/", nil},
		{"/api/standing", nil},
		{"/assets/dashboard.css", nil},
		{"/nothing-here", bearer(w.server.Token())},
		{"/", withHost("evil.test")},
	} {
		response, _ := w.get(probe.path, probe.shape)
		csp := response.Header.Get("Content-Security-Policy")
		for _, directive := range []string{"default-src 'none'", "script-src 'self'", "frame-ancestors 'none'", "base-uri 'none'"} {
			if !strings.Contains(csp, directive) {
				t.Fatalf("%s (%d): policy %q lacks %q", probe.path, response.StatusCode, csp, directive)
			}
		}
		for _, forbidden := range []string{"unsafe-inline", "unsafe-eval", "https:", "http:", "cdn"} {
			if strings.Contains(csp, forbidden) {
				t.Fatalf("%s: policy %q allows %q", probe.path, csp, forbidden)
			}
		}
		if response.Header.Get("X-Content-Type-Options") != "nosniff" || response.Header.Get("Cache-Control") != "no-store" {
			t.Fatalf("%s: headers %v", probe.path, response.Header)
		}
	}
}

// The shell carries no inline script and no inline style, because the policy
// would refuse them and a page that depended on either would be blank.
func TestTheShellNeedsNothingThePolicyRefuses(t *testing.T) {
	t.Parallel()
	w := serve(t, stubReader{standing: standingWith("title")})
	for _, page := range []struct {
		path  string
		shape func(*http.Request)
	}{{"/", bearer(w.server.Token())}, {"/", nil}} {
		_, body := w.get(page.path, page.shape)
		if strings.Contains(body, "<script>") || strings.Contains(body, "<style") || strings.Contains(body, " style=") || strings.Contains(body, "onload=") || strings.Contains(body, "https://") {
			t.Fatalf("the page depends on something the policy refuses:\n%s", body)
		}
	}
}

// A value that reaches HTML reaches it as text. The product id is repository
// text, the unreadable-state message is whatever a store said, and the JSON
// carries work-item text; the injected script appears in none of them as
// markup.
func TestEscapesEveryValueReachingHTML(t *testing.T) {
	t.Parallel()
	w := serve(t, stubReader{standing: standingWith(injected)})
	w.server.Product = injected

	response, body := w.get("/", bearer(w.server.Token()))
	if response.StatusCode != http.StatusOK {
		t.Fatalf("shell: %d", response.StatusCode)
	}
	if strings.Contains(body, injected) || !strings.Contains(body, "&lt;script&gt;") {
		t.Fatalf("the product id reached the shell unescaped:\n%s", body)
	}
	if _, body = w.get("/", nil); strings.Contains(body, injected) {
		t.Fatalf("the product id reached the sign-in page unescaped:\n%s", body)
	}

	_, body = w.get("/api/standing", bearer(w.server.Token()))
	if strings.Contains(body, "<script>") || !strings.Contains(body, `\`+`u003cscript\`+`u003e`) {
		t.Fatalf("the JSON carries a raw tag:\n%s", body)
	}

	broken := serve(t, stubReader{ready: errors.New("open run store: " + injected)})
	response, body = broken.get("/", bearer(broken.server.Token()))
	if response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("unreadable state: %d", response.StatusCode)
	}
	if strings.Contains(body, injected) || !strings.Contains(body, "open run store: &lt;script&gt;") {
		t.Fatalf("the failure reached the refusal unescaped:\n%s", body)
	}

	// The sign-in's own note is the one message a browser's post can provoke,
	// and the form field it came from is never echoed into it.
	form := url.Values{"token": {injected}}
	response, body = w.request(http.MethodPost, "/session", strings.NewReader(form.Encode()), all(
		withOrigin("http://127.0.0.1:"+port),
		func(r *http.Request) { r.Header.Set("Content-Type", "application/x-www-form-urlencoded") },
	))
	if response.StatusCode != http.StatusUnauthorized || strings.Contains(body, injected) {
		t.Fatalf("a wrong token was reflected: %d\n%s", response.StatusCode, body)
	}
}

// Durable state that cannot be read is a refusal, on the page and on the API,
// never a shell over records nothing can read.
func TestRefusesUnreadableState(t *testing.T) {
	t.Parallel()
	w := serve(t, stubReader{
		ready:   errors.New("the state root could not be resolved"),
		failure: errors.New("the state root could not be resolved"),
	})

	response, body := w.get("/", bearer(w.server.Token()))
	if response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("shell over unreadable state: %d %s", response.StatusCode, body)
	}
	if strings.Contains(body, "dashboard.js") || !strings.Contains(body, "the state root could not be resolved") {
		t.Fatalf("refusal is a partial page or names no cause: %s", body)
	}
	response, body = w.get("/api/standing", bearer(w.server.Token()))
	if response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("JSON over unreadable state: %d %s", response.StatusCode, body)
	}
	if !strings.Contains(body, `"error":"the state root could not be resolved"`) || strings.Contains(body, "observed_at") {
		t.Fatalf("JSON refusal is not a bare refusal: %s", body)
	}
}

// The sign-in is the one thing a browser may post. With the token it sets a
// cookie confined to this origin and kept from script, and the cookie is then
// the credential; without an Origin, or with the wrong token, it sets nothing.
func TestSignInSetsTheCookieThatThenPresentsTheToken(t *testing.T) {
	t.Parallel()
	w := serve(t, stubReader{standing: standingWith("title")})
	own := "http://127.0.0.1:" + port

	post := func(token string, shape func(*http.Request)) *http.Response {
		t.Helper()
		form := url.Values{"token": {token}}
		response, _ := w.request(http.MethodPost, "/session", strings.NewReader(form.Encode()), all(
			func(r *http.Request) { r.Header.Set("Content-Type", "application/x-www-form-urlencoded") },
			func(r *http.Request) {
				if shape != nil {
					shape(r)
				}
			},
		))
		return response
	}

	if response := post(w.server.Token(), nil); response.StatusCode != http.StatusForbidden || len(response.Cookies()) != 0 {
		t.Fatalf("sign-in with no Origin: %d, cookies %v", response.StatusCode, response.Cookies())
	}
	if response := post(w.server.Token(), withOrigin("http://evil.test")); response.StatusCode != http.StatusForbidden || len(response.Cookies()) != 0 {
		t.Fatalf("sign-in from a foreign origin: %d, cookies %v", response.StatusCode, response.Cookies())
	}
	wrong := strings.Repeat("0", len(w.server.Token()))
	if response := post(wrong, withOrigin(own)); response.StatusCode != http.StatusUnauthorized || len(response.Cookies()) != 0 {
		t.Fatalf("sign-in with the wrong token: %d, cookies %v", response.StatusCode, response.Cookies())
	}

	response := post(w.server.Token(), withOrigin(own))
	if response.StatusCode != http.StatusSeeOther || response.Header.Get("Location") != "/" {
		t.Fatalf("sign-in: %d to %q", response.StatusCode, response.Header.Get("Location"))
	}
	cookies := response.Cookies()
	if len(cookies) != 1 || cookies[0].Name != cookieName || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode || cookies[0].MaxAge != 0 || cookies[0].Path != "/" {
		t.Fatalf("cookie %+v", cookies)
	}
	present := func(r *http.Request) { r.AddCookie(cookies[0]) }
	if response, body := w.get("/api/standing", present); response.StatusCode != http.StatusOK || !strings.Contains(body, "observed_at") {
		t.Fatalf("JSON on the cookie: %d %s", response.StatusCode, body)
	}
	if response, body := w.get("/", present); response.StatusCode != http.StatusOK || !strings.Contains(body, "dashboard.js") {
		t.Fatalf("shell on the cookie: %d %s", response.StatusCode, body)
	}
	// A cookie carrying some other value is no credential.
	stale := func(r *http.Request) { r.AddCookie(&http.Cookie{Name: cookieName, Value: wrong}) }
	if response, _ := w.get("/api/standing", stale); response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("JSON on a stale cookie: %d", response.StatusCode)
	}
}

// The dashboard is read-only: every method but the sign-in's post is refused.
func TestRefusesEveryWrite(t *testing.T) {
	t.Parallel()
	w := serve(t, stubReader{standing: standingWith("title")})
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch} {
		response, _ := w.request(method, "/api/standing", nil, bearer(w.server.Token()))
		if response.StatusCode != http.StatusMethodNotAllowed {
			t.Fatalf("%s: %d", method, response.StatusCode)
		}
	}
}
