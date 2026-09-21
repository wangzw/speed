package journal

import (
	"bufio"
	"net"
	nethttp "net/http"
	"strconv"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/core"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/tenant"
)

// middlewareFor builds the capture layer registered on one endpoint.
//
// Provides carries the same token the module descriptor delivers the capability
// under. That is the only anchor another layer has for placing itself inside or
// outside audit, which makes it this module's contract surface on the chain.
//
// After points at authentication and at tenant resolution, so the layer sits
// inside both and the identity and the tenant it records are the ones those two
// settled. Those are constraints and not guarantees: the entry point drops an
// edge whose capability no layer stands for and lists the drop in the startup
// diagnostics, so an assembly without authentication runs this layer with an
// entry whose actor — and, without tenant resolution, whose tenant — is empty.
func (j *journal) middlewareFor(endpoint string) speedhttp.Middleware {
	return speedhttp.Middleware{
		Name:     ModuleName,
		Provides: []core.Token{(*audit.Audit)(nil)},
		After:    []core.Token{(*authn.Gate)(nil), (*tenant.Resolution)(nil)},
		Order:    middlewareOrder,
		Wrap: func(next nethttp.Handler) nethttp.Handler {
			return nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
				pattern, matched := j.matcher.match(r)
				if !matched {
					next.ServeHTTP(w, r)
					return
				}
				// The row is written after the inner chain has returned,
				// because the outcome does not exist before then. A layer that
				// wrote on the way in could fill that column with a constant
				// and nothing else, and a result column holding a constant is
				// worth nothing to whoever reads it.
				rec := &statusRecorder{ResponseWriter: w, status: nethttp.StatusOK}
				outcome := ""
				defer func() {
					// A handler that panics never returns, and a panic is the
					// request an audit trail least wants to lose. The value is
					// recorded and then thrown again rather than swallowed:
					// this layer's job is to record what happened, not to
					// change what happens.
					if thrown := recover(); thrown != nil {
						outcome = panicOutcome
						j.recordRequest(r, endpoint, pattern, outcome)
						panic(thrown)
					}
					j.recordRequest(r, endpoint, pattern, outcome)
				}()
				next.ServeHTTP(rec, r)
				outcome = strconv.Itoa(rec.status)
			})
		},
	}
}

// panicOutcome is what the result column holds for a request whose handler
// panicked. The status never arrived, so a status code would be a guess.
const panicOutcome = "panic"

// recordRequest writes the row for one matched request.
//
// The endpoint name goes into the detail rather than into a column of its own:
// it is not one of the entry's fields, and the layer does know which endpoint
// it was registered on.
func (j *journal) recordRequest(r *nethttp.Request, endpoint, pattern, outcome string) {
	j.submit(r.Context(), audit.SourceRequest, audit.Entry{
		Action:  r.Method,
		Object:  pattern,
		Outcome: outcome,
		Detail: map[string]string{
			"endpoint": endpoint,
			"path":     r.URL.Path,
		},
	})
}

// statusRecorder remembers the status the inner chain answered with.
//
// A handler that never calls WriteHeader answers 200, which is why that is the
// initial value rather than something read off a zero int.
type statusRecorder struct {
	nethttp.ResponseWriter
	status  int
	written bool
}

func (s *statusRecorder) WriteHeader(code int) {
	if !s.written {
		s.status = code
		s.written = true
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	s.written = true
	return s.ResponseWriter.Write(b)
}

// Unwrap lets http.ResponseController reach the writer underneath, so a handler
// that flushes or hijacks the connection still can.
func (s *statusRecorder) Unwrap() nethttp.ResponseWriter { return s.ResponseWriter }

// Flush forwards to the writer underneath.
//
// Wrapping a writer hides whatever optional interfaces it carries, and a
// handler that reaches for one by type assertion is the ordinary way to write a
// streaming response. Without this, a server-sent-events route on a guarded
// endpoint would buffer every frame until the handler returned — the client
// would see nothing until the end, and nothing would say why. The two that
// change what a client receives are forwarded here; the rest are reachable
// through Unwrap.
func (s *statusRecorder) Flush() {
	if flusher, ok := s.ResponseWriter.(nethttp.Flusher); ok {
		flusher.Flush()
	}
}

// Hijack forwards to the writer underneath, which is what an upgrade to a
// protocol of its own needs.
func (s *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if hijacker, ok := s.ResponseWriter.(nethttp.Hijacker); ok {
		return hijacker.Hijack()
	}
	return nil, nil, nethttp.ErrNotSupported
}
