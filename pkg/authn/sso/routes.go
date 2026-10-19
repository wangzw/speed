package sso

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authn/internal/mount"
	speedhttp "github.com/vislake/speed/pkg/http"
)

// typePrefix starts the Problem.Type of every failure this capability's
// handlers write, the code following it being one of mount's closed table.
const typePrefix = "urn:speed:authn:"

// authnFailure is a refusal of one of the capability's problem codes: the entry
// point writes it as a problem document of the code's status and type.
func authnFailure(status int, code string) error {
	return speedhttp.Fault{Status: status, Type: typePrefix + code}
}

// mountRoutes registers the discovery route through the mounter as a public one:
// it answers which sign-in entries exist for an email's domain and nothing about
// whether an account does. The route is registered from the operation that
// declares it, so the handler's input and answer types, the fragment and the
// route are one source. The declaration writes the route against the default
// mount base, and the mounter joins its own base to the relative form, so a
// configured base moves the route without moving the contract.
func (p *product) mountRoutes() {
	rel, err := mount.RelativePath(mount.DefaultBasePath, ssoSpec.ResolvePath(discoverOperation.Path))
	if err != nil {
		panic(diagnosticPrefix + ModuleName + ": " + err.Error())
	}
	p.mounter.Public(http.MethodPost, rel, capabilityDecoded(discoverOperation.Handle(p.handleDiscover)))
}

// capabilityDecoded answers a body the entry point's binding would refuse with
// the capability's own problem codes. The entry point binds the same body again
// after this check and reads it from the copy put back; its own refusal names no
// problem type, while the capability's codes are part of the failure set its
// contract publishes.
//
// The check is the capability's decode rule: application/json with no parameter
// or with charset=utf-8, a body no larger than mount.MaxBodyBytes, a single JSON
// object of the declared shape, no member the declaration does not carry, and
// nothing after the value. A body over the capability's limit is a bad request,
// the answer the capability's decoder gave before the route was declared as an
// operation. A request that declares a length over the endpoint's own limit is
// refused by the body limit layer before this handler runs with its 413, which
// is a platform-wide answer and carries no capability code; that is why the
// route declares 413.
func capabilityDecoded(next gin.HandlerFunc) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !isJSONContentType(c.GetHeader("Content-Type")) {
			mount.Fail(c, mount.CodeUnsupportedMediaType)
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, mount.MaxBodyBytes))
		if err != nil {
			mount.Fail(c, mount.CodeBadRequest)
			return
		}
		if !isJSONObject(body) {
			mount.Fail(c, mount.CodeBadRequest)
			return
		}
		var probe discoverRequest
		dec := json.NewDecoder(bytes.NewReader(body))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&probe); err != nil {
			mount.Fail(c, mount.CodeBadRequest)
			return
		}
		if _, err := dec.Token(); !errors.Is(err, io.EOF) {
			mount.Fail(c, mount.CodeBadRequest)
			return
		}
		c.Request.Body = io.NopCloser(bytes.NewReader(body))
		next(c)
	}
}

// isJSONContentType reports whether a Content-Type value is application/json,
// with no parameter or with charset=utf-8 as its only one.
func isJSONContentType(value string) bool {
	media, params, err := mime.ParseMediaType(value)
	if err != nil || media != "application/json" {
		return false
	}
	for k, v := range params {
		if k != "charset" || !strings.EqualFold(v, "utf-8") {
			return false
		}
	}
	return true
}

// isJSONObject reports whether a body begins with an object. The decoder reads a
// literal null into the input without an error, so a body that is not an object
// is refused here rather than passed to the entry point's binder, which reads
// null as a refusal with no code.
func isJSONObject(body []byte) bool {
	trimmed := bytes.TrimSpace(body)
	return len(trimmed) > 0 && trimmed[0] == '{'
}

// handleDiscover answers the sign-in entries for an email's domain. The local
// part is never looked at, so the answer does not leak whether an account
// exists; a request whose domain cannot be reduced to a domain is a 400, and one
// whose domain no provider has verified is the same empty list as one that has
// providers disabled.
func (p *product) handleDiscover(ctx context.Context, in discoverRequest) (discoverResponse, error) {
	providers, err := p.Discover(ctx, emailDomain(in.Email))
	if err != nil {
		if errors.Is(err, errInvalidDomain) {
			return discoverResponse{}, authnFailure(http.StatusBadRequest, mount.CodeBadRequest)
		}
		return discoverResponse{}, authnFailure(http.StatusServiceUnavailable, mount.CodeUnavailable)
	}
	out := discoverResponse{Providers: make([]AuthnSsoProvider, 0, len(providers))}
	for _, prov := range providers {
		out.Providers = append(out.Providers, AuthnSsoProvider{ID: prov.ID, Name: prov.Name, Protocol: prov.Protocol})
	}
	return out, nil
}

// emailDomain is the domain of an email address: everything after the last at
// sign. An address with no at sign has no domain and returns the empty string,
// which the discovery refuses as not a domain.
func emailDomain(email string) string {
	at := strings.LastIndex(email, "@")
	if at < 0 {
		return ""
	}
	return email[at+1:]
}
