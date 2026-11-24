package otel

import (
	"strings"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/contrib/instrumentation/github.com/gin-gonic/gin/otelgin"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/observability"
)

// attrEndpointName is the one attribute the server span carries that the
// upstream instrumentation does not: the endpoint name the configuration lists,
// the only place the span records which endpoint a request arrived on.
var attrEndpointName = attribute.Key("speed.endpoint.name")

var attrURLPath = attribute.Key("url.path")

// credentialSegmentPrefix begins every opaque credential the
// authentication capability issues: a refresh token, a service secret and
// an invitation's acceptance token.
const credentialSegmentPrefix = "spd_"

// redactedSegment is what a credential path segment is replaced with. The rest of
// the path is left as it was, so a span still names the route shape without the
// credential.
const redactedSegment = "[REDACTED]"

// redactPath replaces each path segment that begins with the credential marker,
// leaving every other segment intact. A path with no such segment is returned
// unchanged, so the instrumentation's own attribute is not rewritten needlessly.
func redactPath(path string) string {
	if !strings.Contains(path, credentialSegmentPrefix) {
		return path
	}
	segments := strings.Split(path, "/")
	changed := false
	for i, segment := range segments {
		if strings.HasPrefix(segment, credentialSegmentPrefix) {
			segments[i] = redactedSegment
			changed = true
		}
	}
	if !changed {
		return path
	}
	return strings.Join(segments, "/")
}

// layers describes the two layers this module registers for one endpoint,
// in the order they are registered: the instrumentation, then the guard
// inside it.
func (p *providers) layers(endpoint string) []http.Middleware {
	return []http.Middleware{p.instrumentation(), p.guard(endpoint)}
}

func (p *providers) attach(ep http.Endpoint, endpoint string) {
	for _, layer := range p.layers(endpoint) {
		ep.Use(layer)
	}
}

func (p *providers) instrumentation() http.Middleware {
	return http.Middleware{
		Name: ModuleName,
		Provides: []core.Token{
			(*observability.TracerProvider)(nil),
			(*observability.MeterProvider)(nil),
		},
		Stage: http.StageObserve,
		Order: http.OrderOutermost,
		Handler: otelgin.Middleware("",
			otelgin.WithTracerProvider(p.tp),
			otelgin.WithMeterProvider(p.mp),
			otelgin.WithPropagators(observability.Propagator()),
		),
	}
}

func (p *providers) guard(endpoint string) http.Middleware {
	return http.Middleware{
		Name:  ModuleName + ":guard",
		Stage: http.StageObserve,
		After: []core.Token{(*observability.TracerProvider)(nil)},
		Handler: func(c *gin.Context) {
			ctx := c.Request.Context()
			span := trace.SpanFromContext(ctx)
			span.SetAttributes(attrEndpointName.String(endpoint))
			if path := c.Request.URL.Path; path != "" {
				if redacted := redactPath(path); redacted != path {
					span.SetAttributes(attrURLPath.String(redacted))
				}
			}
			ctx = log.WithAttrs(ctx,
				observability.TraceIDKey, span.SpanContext().TraceID().String(),
				observability.SpanIDKey, span.SpanContext().SpanID().String(),
			)
			c.Request = c.Request.WithContext(ctx)

			c.Next()
		},
	}
}
