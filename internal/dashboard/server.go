// Package dashboard serves the read model over HTTP, to a browser on this
// machine and to nothing else.
//
// It is the repository's first web service, and the observability-and-dashboard
// design establishes the conventions it holds to here, so that the next web
// surface starts from them rather than rediscovering each one:
//
//   - It binds loopback only, and loopback alone is not trusted: every request
//     carries a bearer token the process generated at start and printed once,
//     presented in a header or in a cookie and never in a URL, where it would
//     reach a browser history, a referrer, and every log a proxy keeps.
//   - The Host and Origin headers are validated against the address it bound,
//     and anything else is refused. A page on some other origin that scripts a
//     request at this port is refused on the Origin; a DNS name a browser is
//     pointed at that resolves here is refused on the Host.
//   - Every response carries a content-security policy that allows nothing but
//     this origin's own script and style, so nothing is loaded from a CDN and no
//     inline script runs — including one that reached the page through a value
//     that was not escaped.
//   - Every value that reaches HTML goes through html/template, so work-item
//     text, an error message, and a product id render as text.
//   - Every failure fails closed. A missing or wrong token, a foreign Host, a
//     foreign Origin, and durable state that cannot be read each produce a
//     refusal that carries no part of the read model, never a page with a
//     quarter of the answer on it.
//
// It is a projection, never an engine: it owns no workflow, conversation,
// provider, or configuration state, and offers no write of any kind. The one
// thing it holds is the token, in memory, for exactly as long as the process
// runs. Restarting it changes nothing about the harness and loses no history,
// because the history is in the durable records it reads.
package dashboard

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/readmodel"
)

//go:embed assets
var assets embed.FS

// shell is the page and the two refusals a browser is shown, parsed once. Every
// value a template is handed is escaped by the package on the way into HTML,
// which is the whole reason the pages are templates rather than strings.
var shell = template.Must(template.ParseFS(assets, "assets/*.html"))

// cookieName is the cookie the token is presented in once the page has it. It is
// a session cookie on purpose: the token is printed once and lives as long as
// the process, and a browser closed and opened again is asked for it again.
const cookieName = "yoyo_dashboard"

// stylesheet is the page's style, and the one path served without a token; see
// the route for why.
const stylesheet = "/assets/dashboard.css"

// tokenBytes is the entropy behind one token. Thirty-two bytes is more than any
// guess on a loopback port could ever cover, and it renders as sixty-four hex
// characters, which is short enough to paste.
const tokenBytes = 32

// shutdownGrace bounds how long a stop waits for requests in flight. A reading
// is bounded by the tracker timeout, so anything still open past this is stuck
// rather than working.
const shutdownGrace = 5 * time.Second

// policy is the content-security policy every response carries, refusals
// included. Nothing is allowed from anywhere but this origin, and inline script
// and style are not allowed at all: a script that reaches the page through an
// unescaped value has nowhere to run.
const policy = "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self'; connect-src 'self'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'"

// Reader is the read model as the server is handed it. It is an interface so
// the security conventions can be driven without a state directory, which is
// the only way a refusal nobody may weaken gets a test that holds it.
type Reader interface {
	// Ready says whether the durable records the read model is served from can
	// be opened now. An error is why they cannot, and the page is refused on it
	// rather than served over state nothing can read.
	Ready(ctx context.Context) error
	// Standing reads the read model. An error is a refusal of the whole answer,
	// never a partial one: what the read model could answer with a source missing
	// it says inside the Standing, line by line.
	Standing(ctx context.Context) (readmodel.Standing, error)
}

// Server is one dashboard process: the token it generated, the address it bound,
// and the read model it projects.
type Server struct {
	// Product is the product id, for the page's title. It is repository-supplied
	// text and is escaped like everything else.
	Product string
	reader  Reader
	token   string

	listener net.Listener
	// hosts is every Host header that names the bound address, and origins every
	// Origin header that does. Both are fixed at Listen and read on every request.
	hosts   map[string]bool
	origins map[string]bool
}

// New makes a server holding a fresh token for the read model it is handed. The
// token is the process's whole credential, so a source of randomness that fails
// is a server that does not start.
func New(product string, reader Reader) (*Server, error) {
	if reader == nil {
		return nil, errors.New("dashboard: no read model to serve")
	}
	raw := make([]byte, tokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return nil, fmt.Errorf("dashboard: generate token: %w", err)
	}
	return &Server{Product: product, reader: reader, token: hex.EncodeToString(raw)}, nil
}

// Token is the credential every request has to present. It is for the process
// that started the server to print once; nothing here writes it anywhere.
func (s *Server) Token() string { return s.token }

// Listen binds loopback on the port asked for, or on one the operating system
// chooses when the port is zero, and returns the URL a browser is pointed at.
// It binds 127.0.0.1 by name rather than "localhost", which on some machines
// resolves to an IPv6 address the listener is not on.
func (s *Server) Listen(port int) (string, error) {
	if port < 0 || port > 65535 {
		return "", fmt.Errorf("dashboard: port %d is not a port", port)
	}
	listener, err := net.Listen("tcp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		return "", fmt.Errorf("dashboard: bind loopback: %w", err)
	}
	s.listener = listener
	s.bound(listener.Addr().(*net.TCPAddr).Port)
	return s.URL(), nil
}

// bound fixes the Host and Origin values that name this server, from the port
// it is on. It is what Listen calls, and what a test calls in place of a
// listener the sandbox it runs in will not grant.
func (s *Server) bound(port int) {
	bound := strconv.Itoa(port)
	s.hosts = map[string]bool{"127.0.0.1:" + bound: true, "localhost:" + bound: true}
	s.origins = map[string]bool{"http://127.0.0.1:" + bound: true, "http://localhost:" + bound: true}
}

// URL is where the server is listening, once it is. It carries no token: the
// token is presented in a header or a cookie, never in a URL.
func (s *Server) URL() string {
	if s.listener == nil {
		return ""
	}
	return "http://" + s.listener.Addr().String() + "/"
}

// Serve answers requests until the context is cancelled, and then stops,
// waiting shutdownGrace for anything in flight. It returns nil on a stop that
// was asked for and the failure otherwise.
func (s *Server) Serve(ctx context.Context) error {
	if s.listener == nil {
		return errors.New("dashboard: Serve before Listen")
	}
	server := &http.Server{
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		// The server's own log is left where it defaults, standard error, and
		// nothing here logs a request: a request line is where a token would
		// otherwise end up.
	}
	stopped := make(chan error, 1)
	go func() { stopped <- server.Serve(s.listener) }()
	select {
	case err := <-stopped:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		grace, cancel := context.WithTimeout(context.Background(), shutdownGrace)
		defer cancel()
		if err := server.Shutdown(grace); err != nil {
			return fmt.Errorf("dashboard: stop: %w", err)
		}
		<-stopped
		return nil
	}
}

// Handler is every route, behind the checks every request passes first.
func (s *Server) Handler() http.Handler {
	return http.HandlerFunc(s.serve)
}

// serve is the one entry every request takes. The order is deliberate: the
// headers that make a refusal safe are set before anything can be refused, the
// request's provenance is checked before its credential, and its credential is
// checked before anything is read.
func (s *Server) serve(writer http.ResponseWriter, request *http.Request) {
	header := writer.Header()
	header.Set("Content-Security-Policy", policy)
	header.Set("X-Content-Type-Options", "nosniff")
	header.Set("X-Frame-Options", "DENY")
	header.Set("Referrer-Policy", "no-referrer")
	header.Set("Cache-Control", "no-store")

	// A Host that is not the bound address is a request that was not addressed
	// here — a name a browser was pointed at that happens to resolve to loopback,
	// or a rebinding attack that relies on exactly that. The refusal reflects
	// nothing back, because the one thing known about the request is that it
	// came from somewhere it should not have.
	if !s.hosts[request.Host] {
		refuse(writer, request, http.StatusForbidden, "this dashboard answers only to the address it was started on")
		return
	}
	// An Origin is sent by a browser making a request from a page, and one from
	// any origin but this one is a page elsewhere scripting requests at this
	// port. A request with no Origin is a navigation or a tool, and the token
	// decides those.
	if origin := request.Header.Get("Origin"); origin != "" && !s.origins[origin] {
		refuse(writer, request, http.StatusForbidden, "this dashboard refuses requests from any other origin")
		return
	}

	switch {
	case request.URL.Path == "/session" && request.Method == http.MethodPost:
		s.serveSession(writer, request)
	case request.Method != http.MethodGet && request.Method != http.MethodHead:
		refuse(writer, request, http.StatusMethodNotAllowed, "this dashboard is read-only")
	case request.URL.Path == stylesheet:
		// The one route without a token: the sign-in page is shown to a browser
		// that has none yet, and the policy lets it take style from this origin
		// only. The stylesheet is text compiled into the binary and reads nothing.
		s.serveAsset(writer, request)
	case !s.presented(request):
		s.refuseToken(writer, request, "")
	case request.URL.Path == "/":
		s.servePage(writer, request)
	case request.URL.Path == "/api/standing":
		s.serveStanding(writer, request)
	case strings.HasPrefix(request.URL.Path, "/assets/"):
		s.serveAsset(writer, request)
	default:
		refuse(writer, request, http.StatusNotFound, "nothing is served at that path")
	}
}

// presented says whether the request carries the token, in the header a tool
// sends or in the cookie the page has once it signed in. The comparison is
// constant-time, because a comparison that stops at the first wrong byte says
// how many bytes were right.
func (s *Server) presented(request *http.Request) bool {
	if candidate, found := strings.CutPrefix(request.Header.Get("Authorization"), "Bearer "); found {
		if s.matches(strings.TrimSpace(candidate)) {
			return true
		}
	}
	if cookie, err := request.Cookie(cookieName); err == nil && s.matches(cookie.Value) {
		return true
	}
	return false
}

func (s *Server) matches(candidate string) bool {
	return subtle.ConstantTimeCompare([]byte(candidate), []byte(s.token)) == 1
}

// serveSession is the one thing a browser may post: the token, from the sign-in
// form, which becomes a cookie so the page can fetch without holding the token
// in script. A browser sends an Origin with every form post, so one without is
// not a browser's form and is refused; one with a foreign Origin was refused
// before this. The cookie is confined to this origin, kept from script, and sent
// on no cross-site request at all.
func (s *Server) serveSession(writer http.ResponseWriter, request *http.Request) {
	if request.Header.Get("Origin") == "" {
		refuse(writer, request, http.StatusForbidden, "a sign-in has to come from the dashboard's own page")
		return
	}
	if err := request.ParseForm(); err != nil {
		refuse(writer, request, http.StatusBadRequest, "the sign-in form could not be read")
		return
	}
	if !s.matches(strings.TrimSpace(request.PostForm.Get("token"))) {
		s.refuseToken(writer, request, "that is not the token this dashboard printed when it started")
		return
	}
	http.SetCookie(writer, &http.Cookie{
		Name:     cookieName,
		Value:    s.token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})
	http.Redirect(writer, request, "/", http.StatusSeeOther)
}

// servePage is the shell: the page with its states and nothing of the read model
// in it, which the page's own script then fetches. It is refused outright when
// the durable records cannot be opened, because a shell whose every fetch will
// fail is a page that looks like a dashboard and is not one.
func (s *Server) servePage(writer http.ResponseWriter, request *http.Request) {
	if err := s.reader.Ready(request.Context()); err != nil {
		s.renderPage(writer, http.StatusServiceUnavailable, "unreadable.html", err.Error())
		return
	}
	s.renderPage(writer, http.StatusOK, "shell.html", "")
}

// serveStanding is the read model as JSON, whole or refused. What the model
// could not read it says line by line inside the answer; what stops the model
// being read at all is a refusal carrying the reason and nothing else.
func (s *Server) serveStanding(writer http.ResponseWriter, request *http.Request) {
	standing, err := s.reader.Standing(request.Context())
	if err != nil {
		refuse(writer, request, http.StatusServiceUnavailable, err.Error())
		return
	}
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.WriteHeader(http.StatusOK)
	if request.Method == http.MethodHead {
		return
	}
	encoder := json.NewEncoder(writer)
	// The JSON is read by script and never written into HTML as markup, but a
	// "<" in a title escaped here costs nothing and closes the case where some
	// later reader does.
	encoder.SetEscapeHTML(true)
	_ = encoder.Encode(standing)
}

// serveAsset is the page's own script and style, from the binary. They are the
// only script and style the policy allows. The script is behind the token like
// everything else; the stylesheet is the one exception, above, and is the only
// unauthenticated route there is.
func (s *Server) serveAsset(writer http.ResponseWriter, request *http.Request) {
	name := strings.TrimPrefix(request.URL.Path, "/")
	content, err := fs.ReadFile(assets, name)
	if err != nil || strings.HasSuffix(name, ".html") {
		refuse(writer, request, http.StatusNotFound, "nothing is served at that path")
		return
	}
	switch {
	case strings.HasSuffix(name, ".css"):
		writer.Header().Set("Content-Type", "text/css; charset=utf-8")
	case strings.HasSuffix(name, ".js"):
		writer.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	default:
		writer.Header().Set("Content-Type", "application/octet-stream")
	}
	writer.WriteHeader(http.StatusOK)
	if request.Method != http.MethodHead {
		_, _ = writer.Write(content)
	}
}

// refuseToken is the refusal for a request with no valid token. A browser asking
// for the page is shown the sign-in form under the refusal's status, because the
// form is how it gets a token; everything else is told in the form it asked for.
func (s *Server) refuseToken(writer http.ResponseWriter, request *http.Request, note string) {
	if request.URL.Path == "/" || request.URL.Path == "/session" {
		s.renderPage(writer, http.StatusUnauthorized, "signin.html", note)
		return
	}
	refuse(writer, request, http.StatusUnauthorized, "this dashboard requires the token it printed when it started, as a bearer token")
}

// renderPage writes one of the templates with the product and a message, both
// escaped by the template on the way in.
func (s *Server) renderPage(writer http.ResponseWriter, status int, name, message string) {
	writer.Header().Set("Content-Type", "text/html; charset=utf-8")
	writer.WriteHeader(status)
	_ = shell.ExecuteTemplate(writer, name, struct {
		Product string
		Message string
	}{Product: s.Product, Message: message})
}

// refuse is every refusal that is not a page: JSON for a caller that asked for
// it or is at an API path, and plain text otherwise. It reflects nothing from
// the request.
func refuse(writer http.ResponseWriter, request *http.Request, status int, reason string) {
	if strings.HasPrefix(request.URL.Path, "/api/") || strings.Contains(request.Header.Get("Accept"), "application/json") {
		writer.Header().Set("Content-Type", "application/json; charset=utf-8")
		writer.WriteHeader(status)
		_ = json.NewEncoder(writer).Encode(map[string]string{"error": reason})
		return
	}
	writer.Header().Set("Content-Type", "text/plain; charset=utf-8")
	writer.WriteHeader(status)
	_, _ = fmt.Fprintln(writer, reason)
}
