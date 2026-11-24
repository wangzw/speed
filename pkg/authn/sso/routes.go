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

const typePrefix = "urn:speed:authn:"

func authnFailure(status int, code string) error {
	return speedhttp.Fault{Status: status, Type: typePrefix + code}
}

func (p *product) mountRoutes() {
	rel, err := mount.RelativePath(mount.DefaultBasePath, ssoSpec.ResolvePath(discoverOperation.Path))
	if err != nil {
		panic(diagnosticPrefix + ModuleName + ": " + err.Error())
	}
	mount.Declared(p.mounter, discoverOperation, rel, capabilityDecoded(discoverOperation.Handle(p.handleDiscover)))
}

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

func isJSONObject(body []byte) bool {
	trimmed := bytes.TrimSpace(body)
	return len(trimmed) > 0 && trimmed[0] == '{'
}

func (p *product) handleDiscover(ctx context.Context, in discoverRequest) (discoverResponse, error) {
	disabled := map[string]bool{}
	if p.loginSwitch != nil {
		names, err := p.loginSwitch.DisabledMethods()
		if err != nil {
			return discoverResponse{}, authnFailure(http.StatusServiceUnavailable, mount.CodeUnavailable)
		}
		for _, name := range names {
			disabled[name] = true
		}
	}
	providers, err := p.Discover(ctx, emailDomain(in.Email))
	if err != nil {
		if errors.Is(err, errInvalidDomain) {
			return discoverResponse{}, authnFailure(http.StatusBadRequest, mount.CodeBadRequest)
		}
		return discoverResponse{}, authnFailure(http.StatusServiceUnavailable, mount.CodeUnavailable)
	}
	out := discoverResponse{Providers: make([]AuthnSsoProvider, 0, len(providers))}
	for _, prov := range providers {
		if !p.delivered[prov.Protocol] || disabled[prov.Protocol] {
			continue
		}
		out.Providers = append(out.Providers, AuthnSsoProvider{ID: prov.ID, Name: prov.Name, Protocol: prov.Protocol})
	}
	return out, nil
}

func emailDomain(email string) string {
	at := strings.LastIndex(email, "@")
	if at < 0 {
		return ""
	}
	return email[at+1:]
}
