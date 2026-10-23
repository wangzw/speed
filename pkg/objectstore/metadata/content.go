package metadata

import (
	"net/url"
	"strings"
)

// The content route's fixed shape. It is one path and one query parameter: the
// token is a credential and belongs in the query string, because the entry
// point's access log records the path unconditionally (pkg/http/accesslog.go)
// and a credential in a path segment would be recorded with it. A query string
// is not.
const (
	contentPath       = "/content"
	contentTokenParam = "t"
)

// fallbackContentType is the media type an object's bytes are answered with when
// the object records no MIME. An upload records the declared content type, so
// the fallback only covers a completed row that carries none.
const fallbackContentType = "application/octet-stream"

// mimeOrDefault answers the media type to serve an object's bytes under.
func mimeOrDefault(mediaType string) string {
	if strings.TrimSpace(mediaType) == "" {
		return fallbackContentType
	}
	return mediaType
}

// contentURL builds the address a token-carrying grant is used through.
//
// The external origin is the configured public-url, with trailing slashes
// removed. An empty public-url answers a root-relative address, which a
// same-origin deployment reaches directly; a cross-origin deployment has to set
// it, or its frontend will request the content route on its own origin.
func (p *product) contentURL(token string) string {
	base := strings.TrimRight(p.publicURL, "/")
	query := url.Values{contentTokenParam: {token}}
	return base + contentPath + "?" + query.Encode()
}
