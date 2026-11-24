package mount

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// Wrap puts Cache-Control: no-store on the response, then puts the request to
// csrf.Check before the handler and answers a failed check with Unauthorized's
// 401 and no code; a nil csrf or handler panics.
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

// AllowCallback registers the cross-origin check's bypass for the routes an
// external party redirects the browser back to: one pattern per method, for the
// full path, where ":name" is one segment and "*name" the rest.
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
