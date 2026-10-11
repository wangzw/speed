package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	_ "embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	nethttp "net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/kv"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/settings"
)

// sessionsModuleName is the name the sessions module is registered under, and
// the name its backend reports. An identity authenticated by a bearer token is
// the account of the user the session belongs to, so its Key is the user
// identifier.
const (
	sessionsModuleName  = "sessions"
	sessionsBackendName = sessionsModuleName
)

// The two routes, both on the account endpoint. The login is exempt from the
// gateway, because what it takes is the credential it turns into a token; the
// logout is guarded, because what it ends is the session that authenticated
// it.
const (
	loginRoute  = "POST /login"
	logoutRoute = "POST /logout"
)

// bearerScheme is the authentication scheme this backend takes whole, in the
// folded form the gateway hands a backend.
const bearerScheme = "bearer"

// ttlSetting is the setting the session lifetime is read from, under its full
// path: the namespace this module declares, a dot, then the item key. The
// settings surface looks a key up by exactly that path.
const ttlSetting = sessionsModuleName + ".ttl"

// defaultSessionTTL is the lifetime a session gets when no layer holds another
// value.
const defaultSessionTTL = 12 * time.Hour

// sessionKeyPrefix starts every key a session is stored under. The key-value
// capability has no namespaces, so the prefix is what keeps these keys apart
// from every other user of the same store.
const sessionKeyPrefix = "crm:session:"

// tokenBytes is how much randomness one token carries.
const tokenBytes = 32

// The causes this backend refuses a token with. The gateway logs a refusal's
// cause, and its log chain masks a word after a scheme name that its fixed
// prose list does not hold, so neither text puts a word after "bearer" (see
// the failures in users.go).
var (
	errUnknownSession   = errors.New("no live session holds this token")
	errMalformedSession = errors.New("the stored session is not in this host's form")
	errNoSuchUser       = errors.New("the session's user no longer exists")
)

// session is what the key-value store holds under a token's key: the user the
// session belongs to and when the login that issued it happened, which is the
// authentication time the identity carries.
type session struct {
	User   string    `json:"user"`
	Issued time.Time `json:"issued"`
}

// encode is the stored form of a session.
func (s session) encode() ([]byte, error) { return json.Marshal(s) }

// readSession reads a stored session back. A value that does not decode, or that
// names no user or no time, is not a session this host wrote.
func readSession(stored []byte) (session, error) {
	var s session
	if err := json.Unmarshal(stored, &s); err != nil || s.User == "" || s.Issued.IsZero() {
		return session{}, errMalformedSession
	}
	return s, nil
}

//go:embed openapi/sessions.json
var sessionsOpenAPI []byte

// sessionSettings is the carrier of this module's setting items. A field's value
// is the declared default.
type sessionSettings struct {
	TTL time.Duration `config:"ttl"`
}

// sessionsSettingsSchema declares the session lifetime on the settings surface,
// where it can differ per deployment without a restart.
func sessionsSettingsSchema() settings.Schema {
	return settings.Schema{
		Namespace: sessionsModuleName,
		Mounts:    []settings.Mount{{Value: &sessionSettings{TTL: defaultSessionTTL}}},
		Items: map[string]settings.Item{
			"ttl": {Description: "how long a bearer token issued at login stays valid"},
		},
	}
}

// sessionKey is where a token's session is stored: the digest of the token,
// never the token itself, so a reader of the store holds nothing a request
// could present.
func sessionKey(token string) string {
	sum := sha256.Sum256([]byte(token))
	return sessionKeyPrefix + hex.EncodeToString(sum[:])
}

// newToken draws a bearer token.
func newToken() (string, error) {
	raw := make([]byte, tokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// sessionsBackend is the sessions module's product: the credential-verifying
// backend for bearer tokens, and the handlers that issue and end them.
type sessionsBackend struct {
	released
	db        db.Database
	store     kv.KV
	settings  settings.Store
	passwords passwords
}

// Name is what the gateway stamps on every identity this backend authenticates.
func (b *sessionsBackend) Name() string { return sessionsBackendName }

// Accepts takes the bearer scheme whole. The accounts it hands back are the
// identifiers of the users table, which this host keeps itself and not the
// library: the backend declares the prefix those identifiers begin with, so the
// gateway refuses an account outside it, and it claims no impersonation and no
// SSO.
func (b *sessionsBackend) Accepts() authn.Accepts {
	return authn.Accepts{Schemes: []string{bearerScheme}, Accounts: userIDPrefix}
}

// Verify looks a token's session up and answers for the user it belongs to.
// The user is read again on every request, so a user disabled after logging in
// is refused from the next request on rather than when the token expires.
func (b *sessionsBackend) Verify(ctx context.Context, c authn.Credential) authn.Verdict {
	principal, err := b.principalOf(ctx, c.Value)
	switch {
	case err == nil:
		return authn.Authenticated(principal)
	case isRefusal(err):
		return authn.Denied(err)
	default:
		return authn.Unavailable(err)
	}
}

// isRefusal reports whether an error from principalOf says the token does not
// hold, as against the store or the table failing to answer.
func isRefusal(err error) bool {
	return errors.Is(err, errUnknownSession) || errors.Is(err, errMalformedSession) ||
		errors.Is(err, errNoSuchUser) || errors.Is(err, errUserDisabled)
}

// principalOf is what a token's session claims: the principal of the user it
// belongs to, signed in at the time the session records. An error that isRefusal
// holds says the token does not hold; any other says a store could not answer.
func (b *sessionsBackend) principalOf(ctx context.Context, token string) (authn.Principal, error) {
	stored, present, err := b.store.Get(ctx, sessionKey(token))
	if err != nil {
		return authn.Principal{}, err
	}
	if !present {
		return authn.Principal{}, errUnknownSession
	}
	held, err := readSession(stored)
	if err != nil {
		return authn.Principal{}, err
	}
	user, found, err := userByID(ctx, b.db, held.User)
	if err != nil {
		return authn.Principal{}, err
	}
	if !found {
		return authn.Principal{}, errNoSuchUser
	}
	if user.Disabled {
		return authn.Principal{}, errUserDisabled
	}
	return principalFor(user.ID, held.Issued), nil
}

// issued is what a login answers with.
type issued struct {
	Token     string `json:"token"`
	TokenType string `json:"token_type"`
	ExpiresIn int64  `json:"expires_in"`
	UserID    string `json:"user_id"`
}

// login takes the Basic credential of a CRM user and answers with a bearer
// token. The route is exempt, so nothing has checked the credential before this
// handler runs; it is checked here against the users table. The Basic header is
// read by this handler and not by the gateway, which hands Basic to the one
// backend that takes it, the operators' table.
func (b *sessionsBackend) login(c *gin.Context) {
	ctx := c.Request.Context()
	email, password, ok := c.Request.BasicAuth()
	if !ok {
		c.Header("WWW-Authenticate", `Basic realm="`+accountEndpoint+`"`)
		writeError(c, nethttp.StatusUnauthorized, "a login takes the Basic credential of a CRM user")
		return
	}
	user, err := authenticate(ctx, b.db, b.passwords, email, password)
	switch {
	case errors.Is(err, errBadCredentials), errors.Is(err, errUserDisabled):
		c.Header("WWW-Authenticate", `Basic realm="`+accountEndpoint+`"`)
		writeError(c, nethttp.StatusUnauthorized, "the credential does not sign a user in")
		return
	case err != nil:
		log.FromContext(ctx).Error("checking a login", "error", err)
		writeError(c, nethttp.StatusServiceUnavailable, "the user table cannot be read")
		return
	}
	ttl, err := settings.Get[time.Duration](b.settings, settings.Global(), ttlSetting)
	if err != nil {
		log.FromContext(ctx).Error("reading the session lifetime", "error", err)
		writeError(c, nethttp.StatusInternalServerError, "the session lifetime cannot be read")
		return
	}
	// The key-value store keeps a value with a lifetime that is not positive
	// for ever, so a value of that kind would issue tokens that never end.
	// Validating a setting is the consumer's part: the settings surface holds
	// whatever was written to it under the declared type.
	if ttl <= 0 {
		log.FromContext(ctx).Error("the session lifetime is not positive, so no session is issued",
			"setting", ttlSetting, "value", ttl.String())
		writeError(c, nethttp.StatusInternalServerError, "the session lifetime is misconfigured")
		return
	}
	token, err := newToken()
	if err != nil {
		log.FromContext(ctx).Error("drawing a token", "error", err)
		writeError(c, nethttp.StatusInternalServerError, "no token could be drawn")
		return
	}
	record, err := session{User: user.ID, Issued: time.Now().UTC()}.encode()
	if err != nil {
		log.FromContext(ctx).Error("encoding a session", "error", err)
		writeError(c, nethttp.StatusInternalServerError, "no session could be recorded")
		return
	}
	if err := b.store.Set(ctx, sessionKey(token), record, ttl); err != nil {
		log.FromContext(ctx).Error("storing a session", "error", err)
		writeError(c, nethttp.StatusServiceUnavailable, "the session store cannot be written")
		return
	}
	c.JSON(nethttp.StatusCreated, issued{
		Token:     token,
		TokenType: "Bearer",
		ExpiresIn: int64(ttl / time.Second),
		UserID:    user.ID,
	})
}

// logout ends the session whose token authenticated the request. A request
// authenticated some other way carries no session to end, and is told so
// rather than answered as though one had been ended.
func (b *sessionsBackend) logout(c *gin.Context) {
	ctx := c.Request.Context()
	caller, ok := authn.FromContext(ctx)
	if !ok {
		// Unreachable behind the gateway: a guarded route that finds no
		// identity has been reached by a path that skipped authentication.
		log.FromContext(ctx).Error("a guarded route was reached with no identity", "route", logoutRoute)
		writeError(c, nethttp.StatusInternalServerError, "the request carries no identity")
		return
	}
	token, isBearer := bearerToken(c.Request)
	if caller.Backend() != sessionsBackendName || !isBearer {
		writeError(c, nethttp.StatusBadRequest, "a logout ends a bearer session, and this request was not authenticated by one")
		return
	}
	if err := b.store.Delete(ctx, sessionKey(token)); err != nil {
		log.FromContext(ctx).Error("ending a session", "error", err)
		writeError(c, nethttp.StatusServiceUnavailable, "the session store cannot be written")
		return
	}
	c.Status(nethttp.StatusNoContent)
}

// bearerToken reads the token out of a request's Authorization header.
func bearerToken(r *nethttp.Request) (string, bool) {
	scheme, value, found := strings.Cut(r.Header.Get("Authorization"), " ")
	if !found || !strings.EqualFold(scheme, bearerScheme) {
		return "", false
	}
	return strings.TrimSpace(value), true
}

// init registers the sessions module. It runs on serve, where the gateway asks it
// about bearer tokens and the account endpoint carries its two routes.
func init() {
	registerApplication(application{
		module:     sessionsModule(),
		runsOn:     []string{serveCommand},
		models:     []any{User{}},
		journalled: []string{loginRoute, logoutRoute},
	})
}

// sessionsModule delivers the bearer backend and binds the two account routes.
//
// It requires the five capabilities its product holds. The key-value store, the
// settings surface and the users module's derivation, which a login naming no
// user is checked against, are exclusive provisions, so the requirement brings
// none into a command; what it does is fail a command that names this module
// and forgets one, at resolution and naming the capability, rather than at the
// first login.
func sessionsModule() core.Module {
	return core.Module{
		Name: sessionsModuleName,
		Requires: []core.Requirement{
			{Token: (*log.Logger)(nil)},
			{Token: (*db.Database)(nil)},
			{Token: (*kv.KV)(nil)},
			{Token: (*settings.Store)(nil)},
			{Token: (*passwords)(nil)},
		},
		Provides: []core.Provision{
			{Token: (*authn.Verifier)(nil)},
		},
		Resources: []any{
			sessionsSettingsSchema(),
			speedhttp.Spec{Endpoint: accountEndpoint, Document: sessionsOpenAPI},
		},

		// The backend has no input items; its stance is all there is to say.
		Prepare: func(context.Context, *core.Registry) (core.Enablement, error) {
			return core.Enablement{State: core.StateEnabled}, nil
		},

		New: func(_ context.Context, reg *core.Registry) (any, error) {
			logger, err := namedLogger(reg, sessionsModuleName)
			if err != nil {
				return nil, err
			}
			database, err := core.Resolve[db.Database](reg)
			if err != nil {
				return nil, err
			}
			store, err := core.Resolve[kv.KV](reg)
			if err != nil {
				return nil, err
			}
			surface, err := core.Resolve[settings.Store](reg)
			if err != nil {
				return nil, err
			}
			derivation, err := core.Resolve[passwords](reg)
			if err != nil {
				return nil, err
			}
			return &sessionsBackend{
				released:  released{log: logger},
				db:        database,
				store:     store,
				settings:  surface,
				passwords: derivation,
			}, nil
		},

		Init: func(_ context.Context, reg *core.Registry, instance any) error {
			backend, ok := instance.(*sessionsBackend)
			if !ok {
				return errForeignInstance(sessionsModuleName, instance)
			}
			router, err := core.Resolve[speedhttp.Router](reg)
			if err != nil {
				return err
			}
			account, err := router.Endpoint(accountEndpoint)
			if err != nil {
				return err
			}
			gateway, err := core.Resolve[authn.Gate](reg)
			if err != nil {
				return err
			}
			// The login route and its exemption are written together: the
			// exemption table freezes when the gateway starts, so a route
			// registered without its exemption in the same stage stays
			// guarded, and nothing reports the difference.
			mount(account, loginRoute, backend.login)
			method, path := splitRoute(loginRoute)
			gateway.Exempt(accountEndpoint, method, path)
			mount(account, logoutRoute, backend.logout)
			return nil
		},

		Stop:  recordStop,
		Close: recordClose,
	}
}
