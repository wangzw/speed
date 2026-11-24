package metadata

import (
	"net/url"
	"strings"
)

const (
	contentPath       = "/content"
	contentTokenParam = "t"
)

const fallbackContentType = "application/octet-stream"

func mimeOrDefault(mediaType string) string {
	if strings.TrimSpace(mediaType) == "" {
		return fallbackContentType
	}
	return mediaType
}

func (p *product) contentURL(token string) string {
	base := strings.TrimRight(p.publicURL, "/")
	query := url.Values{contentTokenParam: {token}}
	return base + contentPath + "?" + query.Encode()
}
