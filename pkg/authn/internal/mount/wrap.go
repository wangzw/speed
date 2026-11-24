package mount

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// Wrap is the one wrapper every route registration goes through. It puts
// Cache-Control: no-store on the response before the handler runs, so the body a
// handler writes and the 401 it writes for a failed cross-origin check carry it,
// and a handler that has a reason to say otherwise, the key set, sets its own
// over it. A panic is not answered here: it leaves the response to the entry
// point's fallback, whose defect response carries the headers the entry gives it,
// and the headers this wrapper put on are among those the fallback decides on. A
// request is put to the cross-origin check before the handler: one that does not
// pass is answered with the 401 of Unauthorized, with no code, and the handler is
// not called.
//
// The handler runs with the response's header map in its request context, which
// is how a typed handler writes a header its answer body does not express; see
// SetResponseHeader and AddResponseHeader.
//
// The check is csrf.Check, which lets the safe methods through by itself and
// judges the others by Sec-Fetch-Site and, when that is missing, by Origin
// against Host. A route that an external party sends the browser back to from
// another site is let through by a bypass pattern registered on csrf with
// AllowCallback and not by a flag here, so the bypass holds for the pattern that
// was registered and for no other route.
//
// A nil csrf panics: a wrapper that checked nothing would read as a check.
func Wrap(csrf *http.CrossOriginProtection, realm string, h gin.HandlerFunc) gin.HandlerFunc {
	if csrf == nil {
		panic("authn/mount: Wrap needs a cross-origin check")
	}
	if h == nil {
		panic("authn/mount: Wrap needs a handler")
	}
	return func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		if err := csrf.Check(c.Request); err != nil {
			Unauthorized(c, realm, "")
			return
		}
		c.Request = c.Request.WithContext(withResponseHeaders(c.Request.Context(), c.Writer.Header()))
		h(c)
	}
}

// AllowCallback registers the bypass of the cross-origin check for the routes
// an external party redirects the browser back to: one pattern per method, for
// the full path of the route written as the entry point writes it, where
// ":name" is one segment and "*name" is the rest. The bypass lives on csrf, the
// Mounter's own instance, and is never put on the gateway's, so a callback is
// not a hole in the gateway's check.
func AllowCallback(csrf *http.CrossOriginProtection, methods []string, fullPath string) {
	pattern := servePattern(fullPath)
	for _, m := range methods {
		csrf.AddInsecureBypassPattern(m + " " + pattern)
	}
}

func servePattern(path string) string {
	segments := strings.Split(path, "/")
	for i, s := range segments {
		switch {
		case strings.HasPrefix(s, ":"):
			segments[i] = "{" + s[1:] + "}"
		case strings.HasPrefix(s, "*"):
			segments[i] = "{" + s[1:] + "...}"
		}
	}
	return strings.Join(segments, "/")
}
