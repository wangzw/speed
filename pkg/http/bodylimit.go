package http

import (
	"errors"
	"io"
	nethttp "net/http"
	"sync"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/i18n"
	"github.com/vislake/speed/pkg/log"
)

const bodyLimitName = "http:body-limit"

func bodyLimit(endpoint string, maxBodyBytes int64, localizer i18n.Localizer) gin.HandlerFunc {
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
			// The error is recorded for the observability layer to read; its return is the entry
			// gin appended and nothing here reads it back.
			_ = c.Error(cause)
			Fail(c, Problem{
				Status: nethttp.StatusRequestEntityTooLarge,
				Title:  refusalTitle(c.Request.Context(), localizer, nethttp.StatusRequestEntityTooLarge),
				Cause:  cause,
			})
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
