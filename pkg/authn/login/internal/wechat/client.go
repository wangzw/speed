// Package wechat is the thin client the two WeChat login entries share. It
// trades an authorization code for the identity it names and builds the address
// the user agent is sent to for authorization; it is written here rather than
// taken from a library, for the reasons the WeChat ADR records, and it binds no
// WeChat SDK.
//
// # The secret never leaves this package
//
// WeChat's token exchange carries the application secret in the query string, so
// the URL of the request is itself a credential. The one invariant of this
// package is that a URL carrying the secret never leaves it: every error it
// returns drops the request URL before it is handed back, and the *url.Error
// net/http returns — whose text carries the whole URL — is unwrapped rather than
// passed on.
//
// The client the default path builds wraps its transport in the trace and
// logging transports. The logging transport records the method, host, path,
// status, outcome and duration of each exchange and never the query, where the
// secret and the code travel; a transport failure is recorded through the cause
// underneath net/http's *url.Error, so the URL is dropped there too. A new
// outbound path has to hold the same invariant, and the tests assert it for
// every error each one can produce.
//
// # The outbound client
//
// The client sends through the restricted egress client by default, so a
// destination that is not a public https address is refused before a secret is
// put on the wire. A host does not configure this; a test names its own server
// and client through [Options]. A client a test injects is used as it stands
// and is not wrapped: the record is a property of the client this package
// builds.
package wechat

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/vislake/speed/pkg/authn/internal/egress"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/observability"
)

// The provider's endpoints. They are constants of the code: a deployment does
// not point WeChat at another host, and the two browser-authorization endpoints
// are the two ways a WeChat login may begin.
const (
	// exchangeEndpoint trades an authorization code for an identity. The
	// secret travels in its query string.
	exchangeEndpoint = "https://api.weixin.qq.com/sns/oauth2/access_token"
	// AuthorizeQRConnect is the website-application endpoint, the one that
	// shows a QR code a phone scans. Its scope is snsapi_login.
	AuthorizeQRConnect = "https://open.weixin.qq.com/connect/qrconnect"
	// AuthorizeWebAuth is the official-account web-authorization endpoint,
	// the one an account's own browser is sent to. Its scope is
	// snsapi_userinfo.
	AuthorizeWebAuth = "https://open.weixin.qq.com/connect/oauth2/authorize"
	// authorizeFragment is the fragment both browser-authorization endpoints
	// require at the end of the address.
	authorizeFragment = "wechat_redirect"
)

// maxResponseBytes caps a token response. WeChat's answer is a small JSON
// object; the cap is the same one the restricted client applies, repeated here
// so the guarantee holds under a client a test injects.
const maxResponseBytes = egress.MaxBodyBytes

// The non-zero errcode values the token endpoint answers with that name a
// definite kind of refusal. Every other value is a refusal too, but one whose
// cause this side does not name further.
const (
	errCodeInvalidSecret    = 40001 // invalid credential, in practice a wrong secret
	errCodeInvalidAppID     = 40013 // invalid appid
	errCodeInvalidCode      = 40029 // the code is not valid
	errCodeCodeUsed         = 40163 // the code was already used
	errCodeInvalidAppSecret = 40125 // invalid appsecret
	errCodeMissingAppID     = 41002
	errCodeMissingAppSecret = 41004
)

// The sentinels an exchange can fail with. A refusal is a failure the provider
// stated; it is distinguishable from a provider that could not be reached,
// which the caller answers an outage rather than a refusal.
var (
	// ErrCodeRejected says the authorization code is not valid, has expired or
	// was already used.
	ErrCodeRejected = errors.New("wechat: the authorization code was rejected")
	// ErrCredentialRejected says the application identifier or secret was
	// rejected.
	ErrCredentialRejected = errors.New("wechat: the application credentials were rejected")
	// ErrProviderRefused says the provider stated a failure this side does not
	// name further.
	ErrProviderRefused = errors.New("wechat: the provider refused the exchange")
	// ErrProviderFailed says the provider did not answer a usable token
	// response, whether it could not be reached, answered another status or
	// answered a body that is not one. The caller reads it as an outage.
	ErrProviderFailed = errors.New("wechat: the provider did not answer a usable token response")
)

// RefusalError is a failure the provider stated: a non-zero errcode. Its Class
// is one of the sentinels above, so a caller tells a rejected code from rejected
// credentials with errors.Is, and the errcode reaches the log. The text carries
// no part of the request.
type RefusalError struct {
	// Code is the provider's non-zero errcode.
	Code  int
	class error
}

// Error names the errcode and the kind of refusal; it carries no request text.
func (e *RefusalError) Error() string {
	return fmt.Sprintf("wechat: the provider refused the exchange (errcode %d): %s", e.Code, e.class)
}

// Unwrap exposes the class, so errors.Is reports it.
func (e *RefusalError) Unwrap() error { return e.class }

// Options configures a [Client].
type Options struct {
	// AppID is the application identifier the request names and the namespace
	// an open id is bound under.
	AppID string
	// AppSecret is the application secret. It travels in the token request's
	// query string and is a credential.
	AppSecret string
	// TokenEndpoint overrides the token endpoint; a test names its mock
	// server. Empty takes the provider's endpoint.
	TokenEndpoint string
	// HTTP is the outbound client. Nil takes the restricted egress client. A
	// client given here is used as it is, so a test may point it at a loopback
	// server its own roots trust.
	HTTP *http.Client
	// RootCAs and AllowLoopback shape the restricted client built when HTTP is
	// nil, for a deployment that terminates its own certificate or a
	// development run against a local stub.
	RootCAs *x509.CertPool
	// AllowLoopback lets the restricted client reach a loopback address, which a
	// development run against a local stub needs.
	AllowLoopback bool
}

// Client is a WeChat implementation of the token exchange and the authorization
// address. Both entries share one per configured application.
type Client struct {
	appID     string
	appSecret string
	endpoint  string
	http      *http.Client
}

// New builds the client for one application.
func New(o Options) *Client {
	endpoint := o.TokenEndpoint
	if endpoint == "" {
		endpoint = exchangeEndpoint
	}
	client := o.HTTP
	if client == nil {
		client = egress.New(egress.Options{AllowLoopback: o.AllowLoopback, RootCAs: o.RootCAs})
		// The restricted client is wrapped from outside, so the exchange carries
		// the span of the call's context across the hop, and inside it is the
		// logging transport, so the exchange leaves one record. The record names
		// the method, host, path, status, outcome and time and never the query,
		// where the secret and the code travel; a failure is recorded through the
		// cause under net/http's *url.Error, which drops the URL with it.
		client.Transport = observability.Transport(log.Transport(client.Transport))
	}
	return &Client{
		appID:     o.AppID,
		appSecret: o.AppSecret,
		endpoint:  endpoint,
		http:      client,
	}
}

// AppID reports the application identifier, the namespace an open id is bound
// under when there is no union id.
func (c *Client) AppID() string { return c.appID }

// AuthorizeURL builds the address the user agent is sent to for authorization
// at endpoint under scope, coming back to redirectURI with state. The address
// ends with the #wechat_redirect fragment WeChat's browser authorization
// requires.
func (c *Client) AuthorizeURL(endpoint, scope, redirectURI, state string) string {
	q := url.Values{}
	q.Set("appid", c.appID)
	q.Set("redirect_uri", redirectURI)
	q.Set("response_type", "code")
	q.Set("scope", scope)
	q.Set("state", state)
	return endpoint + "?" + q.Encode() + "#" + authorizeFragment
}

// Exchange trades an authorization code for the identity it names.
//
// The request is the provider's GET with the code, the application identifier
// and the secret in the query string. A body that is not a token response, a
// status other than 200 and a failure to reach the provider are ErrProviderFailed
// (an outage); a non-zero errcode is a *RefusalError (a stated refusal), whose
// class tells a rejected code from rejected credentials. No error carries the
// request URL, and so none carries the secret.
func (c *Client) Exchange(ctx context.Context, code string) (Identity, error) {
	endpoint, err := url.Parse(c.endpoint)
	if err != nil {
		return Identity{}, fmt.Errorf("%w: the token endpoint does not parse", ErrProviderFailed)
	}
	q := endpoint.Query()
	q.Set("appid", c.appID)
	q.Set("secret", c.appSecret)
	q.Set("code", code)
	q.Set("grant_type", "authorization_code")
	endpoint.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return Identity{}, fmt.Errorf("%w: building the token request", ErrProviderFailed)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		// net/http returns a *url.Error whose text is the whole request URL,
		// secret included; it is never handed back.
		return Identity{}, fmt.Errorf("%w: %w", ErrProviderFailed, sanitize(err))
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return Identity{}, fmt.Errorf("%w: reading the token response", ErrProviderFailed)
	}
	if len(body) > maxResponseBytes {
		return Identity{}, fmt.Errorf("%w: the token response is larger than the limit", ErrProviderFailed)
	}
	if resp.StatusCode != http.StatusOK {
		return Identity{}, fmt.Errorf("%w: the provider answered status %d", ErrProviderFailed, resp.StatusCode)
	}
	return readToken(body)
}

// readToken reads a token response. A non-zero errcode is a refusal; a body that
// is not a token response, or one that names no open id, is an outage.
func readToken(body []byte) (Identity, error) {
	var r tokenResponse
	if err := json.Unmarshal(body, &r); err != nil {
		return Identity{}, fmt.Errorf("%w: the token response is not JSON", ErrProviderFailed)
	}
	if r.ErrCode != 0 {
		return Identity{}, &RefusalError{Code: r.ErrCode, class: classify(r.ErrCode)}
	}
	if r.OpenID == "" {
		return Identity{}, fmt.Errorf("%w: the token response names no open id", ErrProviderFailed)
	}
	return Identity{OpenID: r.OpenID, UnionID: r.UnionID}, nil
}

// tokenResponse is the part of the provider's answer this client reads. Fields
// it does not know are ignored: the provider may add to its answer.
type tokenResponse struct {
	AccessToken string `json:"access_token"`
	ExpiresIn   int    `json:"expires_in"`
	OpenID      string `json:"openid"`
	Scope       string `json:"scope"`
	UnionID     string `json:"unionid"`
	ErrCode     int    `json:"errcode"`
	ErrMsg      string `json:"errmsg"`
}

// classify names the kind of refusal a non-zero errcode is.
func classify(code int) error {
	switch code {
	case errCodeInvalidCode, errCodeCodeUsed:
		return ErrCodeRejected
	case errCodeInvalidSecret, errCodeInvalidAppID, errCodeInvalidAppSecret, errCodeMissingAppID, errCodeMissingAppSecret:
		return ErrCredentialRejected
	default:
		return ErrProviderRefused
	}
}

// sanitize returns an error safe to hand back and log: any *url.Error in the
// chain is unwrapped to its cause, so the URL it carries — and the secret in it
// — never leaves. The cause is a connection or context failure, which names the
// host and the reason and no part of the query.
func sanitize(err error) error {
	for {
		var uerr *url.Error
		if !errors.As(err, &uerr) {
			break
		}
		if uerr.Err == nil {
			return errors.New("the outbound request failed")
		}
		err = uerr.Err
	}
	return err
}
