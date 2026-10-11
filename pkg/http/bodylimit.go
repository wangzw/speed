package http

import (
	"errors"
	"io"
	nethttp "net/http"
	"sync"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/log"
)

// bodyLimitName is the request body limit layer as it appears in the assembled
// chain's description.
const bodyLimitName = "http:body-limit"

// bodyLimit is the built-in layer that bounds the request body. It is
// registered after every guard layer and before the identify stage, so a body
// is bounded from the identify stage inwards: an identify, throttle, record or
// authorize layer, and the handler, read a bounded body and learn the limit
// through the *http.MaxBytesError their own read ends with.
//
// The guard stage is outside it on purpose. A guard layer refuses on what it
// can see without reading the body — an address, a header, a CORS preflight —
// so it never needs the bound, and imposing the limit in front of it would
// answer an oversize request before the address limit had counted it. It is
// also what keeps CORS, a guard layer, outside this layer, which is what the
// design requires of every layer that refuses: without it the 413 below goes
// out with no Access-Control-Allow-Origin on it, and a browser cannot read the
// refusal.
//
// A request that declares a Content-Length over the limit is refused outright:
// the answer is a 413 written through Fail, in the shape the entry point's
// ErrorResponder decides, with the cause recorded in the gin context's error
// list so the observability layer sees it. A request whose length is unknown —
// a chunked upload is the common one — is bounded instead by wrapping its body,
// and the limit is enforced at the read that crosses it.
//
// The status code is not uniform across the second case, and that boundary is
// deliberate: a request that declares its length gets a deterministic 413, while
// a chunked one is answered by whatever the handler does with the read that
// failed — a handler that binds through gin answers 400, one that reads the body
// itself and checks the error answers 413. What this layer guarantees is that
// the read is bounded, that the cause is recorded, and that the access log and
// the observability layer see a request that was stopped rather than one that
// succeeded on a truncated body. It does not panic out of Read to interrupt the
// handler: that would pierce the handler's own recovery, and a gateway that
// recovers would record it as a 500.
//
// maxBodyBytes at 0 or below is no limit, which is the switch configuration
// gives.
func bodyLimit(endpoint string, maxBodyBytes int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		if maxBodyBytes <= 0 || c.Request.Body == nil {
			c.Next()
			return
		}
		if c.Request.ContentLength > maxBodyBytes {
			cause := &nethttp.MaxBytesError{Limit: maxBodyBytes}
			log.FromContext(c.Request.Context()).Warn("a request body over the endpoint's limit was refused",
				"endpoint", endpoint,
				"limit", maxBodyBytes,
				"content-length", c.Request.ContentLength,
			)
			// The error is recorded for the observability layer to read; its
			// return is the entry gin appended and nothing here reads it back.
			_ = c.Error(cause)
			Fail(c, Problem{Status: nethttp.StatusRequestEntityTooLarge, Cause: cause})
			return
		}
		c.Request.Body = &limitedBody{
			ReadCloser: nethttp.MaxBytesReader(c.Writer, c.Request.Body, maxBodyBytes),
			c:          c,
			limit:      maxBodyBytes,
		}
		c.Next()
	}
}

// limitedBody wraps the body of a request whose length is unknown, so that the
// first read that reaches the limit records the cause once.
//
// The MaxBytesReader underneath is what enforces the limit; this read only
// notices what it returned. The cause goes into the gin context's error list so
// the observability layer marks the span failed with an error.type, and a log
// record is written so the request leaves a trace even where no layer reads the
// error list. sync.Once keeps it to one record per request however many reads
// hit the limit: a handler that reads the body in chunks gets the error on the
// first over-limit read and again on every read after it, and a record per read
// would turn one oversized upload into a burst of identical lines.
type limitedBody struct {
	io.ReadCloser
	c     *gin.Context
	limit int64
	once  sync.Once
}

func (b *limitedBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	var tooLarge *nethttp.MaxBytesError
	if errors.As(err, &tooLarge) {
		b.once.Do(func() {
			_ = b.c.Error(err)
			log.FromContext(b.c.Request.Context()).Warn(
				"a request body reached the endpoint's limit while being read",
				"limit", b.limit,
			)
		})
	}
	return n, err
}
