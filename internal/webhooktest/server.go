// Package webhooktest is a local receiver for testing Telesync's webhook
// delivery engine.
//
// It is a development tool and nothing else. It runs on its own port, knows
// nothing about the daemon, its database, its Telegram clients or its
// configuration, and it has exactly four paths — one per authentication mode
// the sender supports. Each checks the credential that mode uses and then
// answers with the request it received, byte for byte, so an operator can see
// what Telesync actually sent.
//
// That makes it deliberately unguarded about what it echoes: a request is
// reported with its authentication headers intact, which is the whole point.
// It binds to loopback for that reason, its saved records are private to their
// owner, and it is not a production endpoint.
package webhooktest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mirza/telesync/internal/automation/webhooks"
)

// The receiver's defaults. The host is loopback and stays loopback unless the
// operator asks otherwise, because an echo server that verifies nothing on one
// of its paths is not something to expose.
const (
	DefaultHost         = "127.0.0.1"
	DefaultPort         = 8091
	DefaultOutputDir    = "./temp/webhook-responses"
	DefaultMaxBodyBytes = 2 << 20

	readHeaderTimeout = 10 * time.Second
	shutdownTimeout   = 5 * time.Second
)

// Options is how the receiver is run.
type Options struct {
	// Host is the listen address. Empty means loopback.
	Host string
	// Port is the listen port. Zero takes the default.
	Port int
	// SaveResponses writes one JSON record per request to OutputDir.
	SaveResponses bool
	// OutputDir is where records go when SaveResponses is set.
	OutputDir string
	// MaxBodyBytes bounds how much of a request body is read. A larger request
	// is refused rather than read.
	MaxBodyBytes int64
	// Log is where the per-request console output goes. nil discards it.
	Log io.Writer
	// Now overrides the clock (tests).
	Now func() time.Time
}

// Server is one test receiver.
type Server struct {
	opts    Options
	auth    Auth
	store   *Store
	now     func() time.Time
	log     io.Writer
	handler http.Handler

	mu sync.Mutex
	ln net.Listener
}

// New validates the configuration and builds the receiver.
//
// Everything that can be known to be wrong is wrong here rather than at the
// first request: a port that cannot exist, or a custom header name the sender
// would refuse to use, is a typo in this command's own arguments.
func New(opts Options, auth Auth) (*Server, error) {
	if strings.TrimSpace(opts.Host) == "" {
		opts.Host = DefaultHost
	}
	if opts.Port == 0 {
		opts.Port = DefaultPort
	}
	if opts.Port < 0 || opts.Port > 65535 {
		return nil, fmt.Errorf("webhooktest: port %d is not a valid TCP port", opts.Port)
	}
	if opts.MaxBodyBytes <= 0 {
		opts.MaxBodyBytes = DefaultMaxBodyBytes
	}
	if opts.Log == nil {
		opts.Log = io.Discard
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	// The header name is checked against the sender's own rule, so this
	// receiver never waits on a header the sender would never put on a request.
	if name := strings.TrimSpace(auth.HeaderName); name != "" {
		if err := webhooks.ValidAuthHeaderName(name); err != nil {
			return nil, fmt.Errorf("webhooktest: %s: %w", EnvHeaderName, err)
		}
	}

	s := &Server{opts: opts, auth: auth, now: now, log: opts.Log}
	if opts.SaveResponses {
		if strings.TrimSpace(opts.OutputDir) == "" {
			return nil, errors.New("webhooktest: --save-response needs an output directory")
		}
		store, err := NewStore(opts.OutputDir, now)
		if err != nil {
			return nil, err
		}
		s.store = store
	}
	s.handler = s.routes()
	return s, nil
}

// Handler is the receiver's HTTP handler.
func (s *Server) Handler() http.Handler { return s.handler }

// Store is where records are written, or nil when saving is off.
func (s *Server) Store() *Store { return s.store }

// ListenAddr is the address Run binds.
func (s *Server) ListenAddr() string {
	return net.JoinHostPort(s.opts.Host, strconv.Itoa(s.opts.Port))
}

// routes builds the four paths and nothing else.
//
// One handler serves all four: the path chooses which credential is checked and
// the method changes nothing, so there is no "/webhook/hmac/post" and no way
// for the HTTP verb to decide how a request is authenticated.
func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/webhook/hmac", s.route(AuthHMAC))
	mux.HandleFunc("/webhook/bearer", s.route(AuthBearer))
	mux.HandleFunc("/webhook/header", s.route(AuthHeader))
	mux.HandleFunc("/webhook/none", s.route(AuthNone))
	// Everything else is not a webhook. It is reported like any other request
	// when records are being kept, and it is never authenticated.
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		body, truncated := s.read(r)
		s.serve(w, r, AuthUnknown, body, truncated, Result{
			Type:    AuthUnknown,
			Code:    CodeNotVerified,
			Message: "this receiver has /webhook/hmac, /webhook/bearer, /webhook/header and /webhook/none",
		}, http.StatusNotFound)
	})
	return mux
}

func (s *Server) route(mode AuthType) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, truncated := s.read(r)
		if truncated {
			// Nothing is authenticated against a body that was not whole: a
			// signature covers bytes this receiver did not keep.
			s.serve(w, r, mode, body, truncated, Result{
				Type:    mode,
				Code:    CodeNotVerified,
				Message: "the request body was larger than this receiver reads",
			}, http.StatusRequestEntityTooLarge)
			return
		}
		s.serve(w, r, mode, body, truncated, s.auth.Verify(mode, r, body, s.now()), 0)
	}
}

// read returns the body exactly as it arrived, bounded by MaxBodyBytes.
//
// The bound is on the read, not on what is stored: a request larger than the
// limit is refused with 413 and is never held in memory in full. The signature
// is checked against these bytes, so nothing is re-serialized before
// verification and nothing is discarded after it.
func (s *Server) read(r *http.Request) ([]byte, bool) {
	if r.Body == nil {
		return nil, false
	}
	defer r.Body.Close()
	captured, err := io.ReadAll(io.LimitReader(r.Body, s.opts.MaxBodyBytes+1))
	if err != nil && len(captured) == 0 {
		return nil, false
	}
	// One byte past the limit is what proves there was more; it is not part of
	// what is described, because a description of a truncated body would be a
	// description of something that does not match what was sent.
	if int64(len(captured)) > s.opts.MaxBodyBytes {
		return captured[:s.opts.MaxBodyBytes], true
	}
	return captured, false
}

// serve is the one place a reply is built, written and recorded. status is used
// only when it is non-zero; otherwise a verified request is answered 200 and a
// refused one 401.
func (s *Server) serve(w http.ResponseWriter, r *http.Request, mode AuthType,
	body []byte, truncated bool, result Result, status int) {

	if status == 0 {
		if result.Valid {
			status = http.StatusOK
		} else {
			status = http.StatusUnauthorized
		}
	}

	record := Describe(r, body, truncated)
	if truncated {
		// The body was refused, so its size is the limit rather than a count of
		// content the record does not carry.
		record.BodySize = int(s.opts.MaxBodyBytes)
	}

	payload := Response{
		Success:        result.Valid,
		ReceivedAt:     s.now(),
		Authentication: AuthStatus{Type: result.Type, Valid: result.Valid},
		Request:        record,
	}
	switch {
	case result.Valid:
	case status == http.StatusNotFound:
		payload.Error = &ErrorBody{Code: CodeNotFound, Message: "no such webhook test route"}
	case status == http.StatusRequestEntityTooLarge:
		payload.Error = &ErrorBody{
			Code: CodeBodyTooLarge,
			Message: fmt.Sprintf("the request body is larger than the %d byte limit",
				s.opts.MaxBodyBytes),
		}
	default:
		payload.Error = &ErrorBody{Code: result.Code, Message: result.Message}
	}

	encoded, err := json.Marshal(payload)
	if err != nil {
		http.Error(w, "the echo could not be encoded", http.StatusInternalServerError)
		return
	}

	// Content-Length is set so a HEAD request carries the headers of the reply
	// it would have had, with no body, exactly as HTTP semantics ask.
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Length", strconv.Itoa(len(encoded)))
	w.WriteHeader(status)
	if r.Method != http.MethodHead {
		_, _ = w.Write(encoded)
	}

	s.report(r, mode, result, status, record, encoded)
}

// report prints what happened and, when records are being kept, writes one.
//
// The console carries the method, the path, the verdict and the status. It
// carries no credential and no body: a secret in a log is a secret in a
// terminal scrollback, a screenshot and a bug report.
func (s *Server) report(r *http.Request, mode AuthType, result Result, status int,
	record RequestRecord, encoded []byte) {

	fmt.Fprintf(s.log, "[webhook-test] %s %s\n", r.Method, r.URL.Path)
	fmt.Fprintf(s.log, "[webhook-test] Authentication: %s (%s)\n", displayName(mode), verdict(result))
	fmt.Fprintf(s.log, "[webhook-test] Response: %d %s\n", status, http.StatusText(status))

	if s.store == nil {
		return
	}
	// A HEAD reply has no body, and the record says so rather than keeping the
	// payload that would have been sent: the record is of the exchange that
	// happened, not of the one that might have.
	response := ResponseRecord{
		Status:     status,
		StatusText: http.StatusText(status),
		Headers:    map[string][]string{"Content-Type": {"application/json"}},
		Body:       bodyValue(encoded),
		BodyText:   string(encoded),
		BodySize:   len(encoded),
	}
	if r.Method == http.MethodHead {
		response.Body = nil
		response.BodyText = ""
		response.BodySize = 0
		response.BodyOmitted = true
	}

	saved, err := s.store.Save(Record{
		Route:          r.URL.Path,
		AuthType:       mode,
		Received:       record,
		Authentication: result,
		Response:       response,
	})
	if err != nil {
		// A record that could not be written is reported as such. The receiver
		// stays up — it is still answering — and it never claims a file exists.
		fmt.Fprintf(s.log, "[webhook-test] save failed: %v\n", err)
		return
	}
	fmt.Fprintf(s.log, "[webhook-test] Saved: %s\n", saved)
}

// bodyValue is the response body parsed, when it is JSON.
func bodyValue(encoded []byte) json.RawMessage {
	if !json.Valid(encoded) {
		return nil
	}
	return json.RawMessage(append([]byte(nil), encoded...))
}

// AuthStatus is the authentication result as a response reports it: which mode
// was checked and whether it passed. Why it did not is the error beside it.
type AuthStatus struct {
	Type  AuthType `json:"type"`
	Valid bool     `json:"valid"`
}

// ErrorBody is why a request was refused.
type ErrorBody struct {
	Code    ErrCode `json:"code"`
	Message string  `json:"message"`
}

// Response is the whole answer to one request.
//
// The request is echoed on every answer, refused ones included: an operator
// whose delivery failed is exactly the one who needs to see what was sent.
type Response struct {
	Success        bool          `json:"success"`
	ReceivedAt     time.Time     `json:"received_at"`
	Authentication AuthStatus    `json:"authentication"`
	Request        RequestRecord `json:"request"`
	Error          *ErrorBody    `json:"error,omitempty"`
}

// WriteBanner prints what the receiver is about to do, and nothing secret.
func (s *Server) WriteBanner(w io.Writer) {
	fmt.Fprintln(w, "Telesync Webhook Test Receiver")
	fmt.Fprintln(w, "────────────────────────────────────────")
	fmt.Fprintln(w)
	fmt.Fprintf(w, "Listening: http://%s\n", s.ListenAddr())
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Endpoints:")
	fmt.Fprintln(w, "  ANY /webhook/hmac     HMAC-SHA256")
	fmt.Fprintln(w, "  ANY /webhook/bearer   Bearer Token")
	fmt.Fprintln(w, "  ANY /webhook/header   Custom Header")
	fmt.Fprintln(w, "  ANY /webhook/none     No Authentication")
	fmt.Fprintln(w)

	if !s.opts.SaveResponses {
		fmt.Fprintln(w, "Save responses: disabled")
	} else {
		fmt.Fprintln(w, "Save responses: enabled")
		fmt.Fprintf(w, "Output directory: %s\n", s.opts.OutputDir)
		fmt.Fprintln(w)
		fmt.Fprintln(w, "Warning: saved records echo the request as it arrived, authentication")
		fmt.Fprintln(w, "headers included. Use test-only credentials and do not commit them.")
	}
	if !isLoopbackHost(s.opts.Host) {
		fmt.Fprintln(w)
		fmt.Fprintf(w, "Warning: %s is not a loopback address.\n", s.opts.Host)
		fmt.Fprintln(w, "This receiver echoes everything it is sent and verifies nothing on")
		fmt.Fprintln(w, "/webhook/none. It is a development tool: do not expose it publicly.")
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Press Ctrl+C to stop.")
}

// Run serves until ctx ends, then finishes the request it is on and stops.
func (s *Server) Run(ctx context.Context) error {
	ln, err := net.Listen("tcp", s.ListenAddr())
	if err != nil {
		return fmt.Errorf("webhooktest: listen on %s: %w", s.ListenAddr(), err)
	}
	s.mu.Lock()
	s.ln = ln
	s.mu.Unlock()

	s.WriteBanner(s.log)

	srv := &http.Server{
		Handler:           s.handler,
		ReadHeaderTimeout: readHeaderTimeout,
	}
	served := make(chan error, 1)
	go func() { served <- srv.Serve(ln) }()

	select {
	case err := <-served:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		fmt.Fprintln(s.log, "[webhook-test] stopping")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("webhooktest: shut down: %w", err)
		}
		<-served
		return nil
	}
}

// displayName is how a mode is spelled in the console.
func displayName(mode AuthType) string {
	switch mode {
	case AuthHMAC:
		return "HMAC"
	case AuthBearer:
		return "Bearer"
	case AuthHeader:
		return "Custom Header"
	case AuthNone:
		return "None"
	default:
		return "Unknown"
	}
}

// verdict is how a result is spelled in the console. A request refused before
// its credentials were looked at is "not verified", not "invalid": it is a
// distinction an operator reading the log needs.
func verdict(result Result) string {
	switch {
	case result.Valid:
		return "valid"
	case result.Code == CodeNotVerified:
		return "not verified"
	default:
		return "invalid"
	}
}

// isLoopbackHost reports whether host is one an address on this machine would
// have. The warning is about exposure, so a name that resolves elsewhere is
// treated as elsewhere.
func isLoopbackHost(host string) bool {
	switch strings.ToLower(strings.TrimSpace(host)) {
	case "localhost", "127.0.0.1", "::1", "0:0:0:0:0:0:0:1":
		return true
	}
	return false
}
