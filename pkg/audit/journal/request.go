package journal

import (
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
				next.ServeHTTP(rec, r)
				j.submit(r.Context(), audit.SourceRequest, audit.Entry{
					Action:  r.Method,
					Object:  pattern,
					Outcome: strconv.Itoa(rec.status),
					Detail: map[string]string{
						"endpoint": endpoint,
						"path":     r.URL.Path,
					},
				})
			})
		},
	}
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
