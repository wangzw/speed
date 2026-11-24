// Package authntest builds the context a gateway would have left on a request,
// for tests of the modules that read an identity.
//
// Context returns a context carrying a real Identity and the access level of the
// route, as the gateway leaves them once a backend's principal has passed its
// checks. Anonymous returns the other shape the gateway leaves: a level and no
// identity, on an exempt route and on an optional one with no credential. A
// test that built an Identity by hand could only make the zero one, which has
// no account and an empty key, and could not drive the step where a module
// reads who is calling.
//
// ConfigReader and ConfigReaderAll build the other thing a backend's tests
// need: the configuration reader the real loader serves from a JSON body, for a
// module whose Prepare or New reads its own configuration.
//
// Each field of the identity is held to the shape and the bound the gateway's
// exit check holds it to, and a pairing the gateway never produces fails the
// test that asked for it: an identity beside AccessOpen or AccessUnknown, and
// AccessRequired with no identity. Three things the exit check decides are not
// checked here. One is the 120-second ceiling on the time of authentication,
// which needs the gateway's clock. Another is the account namespaces other than
// the static one, and the capabilities a backend declares for an actor, an
// impersonation scope and an SSO provider, which need the backends a gateway
// holds. The last is the reserved attribute prefixes, which the exit check does
// not look at either.
//
// Context takes a testing.TB, but that is no barrier: a *testing.T that nobody
// runs is a valid one. What keeps this package out of production code is the
// dependency test in the parent directory, which fails when any package's
// non-test code imports it.
//
// The package lives under pkg/authn so that it may import the internal
// identity package, which is the only place an Identity is made.
package authntest

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/identity"
	"github.com/vislake/speed/pkg/authn/internal/model"
)

// What an identity gets for each field a test leaves unsaid.
const (
	// DefaultAccount is the account of an identity no option named one for.
	DefaultAccount authn.AccountID = "test-account"
	// DefaultBackend is the backend name of an identity no option named one
	// for. It is not "static", so an account carrying the static prefix needs
	// WithBackend("static"), as it needs the static backend at the gateway.
	DefaultBackend = "authntest"
	// DefaultChallenge is the scheme name Identity.Challenge reads when no
	// option named one, the one a re-authentication challenge on a token
	// carrier uses. WithChallenge changes it, to the scheme of another carrier.
	DefaultChallenge = "Bearer"
)

// DefaultAuthTime is the time of the last interactive authentication an
// identity carries when no option named one: a fixed instant in UTC, so that a
// test's expectations about freshness do not move with the clock.
func DefaultAuthTime() time.Time {
	return time.Date(2026, time.October, 1, 8, 0, 0, 0, time.UTC)
}

// Bounds the gateway holds an identity to, read from the model package, which is
// where the numbers of the attribute set are declared once: the totals the exit
// check stamps and the longest key and value an entry may have. authntest is not
// the gateway and imports none of it, so the exit check keeps constants of its
// own, and gate/attribute_bounds_test.go holds them equal to the model's and the
// two writers' sub-bounds under them. The number of authentication methods is
// the gateway's alone.
const (
	maxMethods       = 8
	maxAttributes    = model.IdentityAttributesMax
	maxAttributeSize = model.IdentityAttributeBytesMax
	maxValueLen      = model.AccountAttributeValueMax
)

var (
	backendName  = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]*$`)
	attributeKey = regexp.MustCompile(fmt.Sprintf(`^[a-z0-9][a-z0-9._-]{0,%d}$`, model.AttributeKeyMax-1))
	// uuidText is the canonical spelling alone, lower case, because a provider
	// identifier is compared as a string wherever it is read.
	uuidText = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
)

// spec is what the options write into before Context builds the identity.
type spec struct {
	account    authn.AccountID
	actor      authn.AccountID
	session    authn.SessionID
	methods    []authn.Method
	assurance  authn.Assurance
	idpAssured bool
	authTime   time.Time
	backend    string
	challenge  string
	provider   string
	scope      string
	attributes map[string]string
	access     authn.Access
}

// Option sets one field of the identity Context builds, or the access level.
// An option given twice is last-wins.
type Option func(*spec)

// WithAccount names the account the request is taken to be from. It has to be
// a well-formed identifier, which ParseAccountID decides.
func WithAccount(id authn.AccountID) Option { return func(s *spec) { s.account = id } }

// WithActor makes the identity an impersonation: id is the account whose hands
// are on the request, and the account WithAccount names, or the default, is the
// one impersonated. Key then reads id, and Account reads the account
// impersonated. An impersonation carries a session and a scope, so it needs
// WithSession and WithScope too, and it fails the test without them; so does an
// actor equal to the account.
func WithActor(id authn.AccountID) Option { return func(s *spec) { s.actor = id } }

// WithSession names the login session the request belongs to.
func WithSession(id authn.SessionID) Option { return func(s *spec) { s.session = id } }

// WithMethods sets the authentication methods, replacing the default of the
// password alone. The set is non-empty, every entry is in the closed set, none
// repeats, and there are at most eight.
func WithMethods(m ...authn.Method) Option {
	own := slices.Clone(m)
	return func(s *spec) { s.methods = own }
}

// WithAssurance sets the assurance level. The default is AssuranceSingle, and
// AssuranceNone reads as AssuranceSingle, as it does when the gateway stamps a
// principal that left the level unstated.
func WithAssurance(a authn.Assurance) Option { return func(s *spec) { s.assurance = a } }

// WithIDPAssured says the multi-factor level is an enterprise IdP's
// declaration. True needs a multi-factor assurance and a provider, and fails
// the test without them.
func WithIDPAssured(v bool) Option { return func(s *spec) { s.idpAssured = v } }

// WithAuthTime sets the time of the last interactive authentication. It must
// not be the zero time. The gateway also refuses a time more than 120 seconds
// ahead of its clock, which this package, having no clock, does not check.
func WithAuthTime(t time.Time) Option { return func(s *spec) { s.authTime = t } }

// WithBackend sets the name of the backend that verified the credential,
// [a-z0-9][a-z0-9.-]*. An account carrying the static prefix needs the backend
// named static, and the backend named static can hand back no other account.
func WithBackend(name string) Option { return func(s *spec) { s.backend = name } }

// WithChallenge sets the scheme name Identity.Challenge reads, which the
// gateway stamps from the credential's carrier: "Cookie" for a session cookie,
// the scheme of the Authorization header otherwise, written with its first
// letter in capitals ("Bearer", "Basic"). The name is an RFC 9110 token whose
// only capital is its first character, which is the shape the gateway writes.
func WithChallenge(scheme string) Option { return func(s *spec) { s.challenge = scheme } }

// WithProvider sets the enterprise SSO provider, a UUID text in its canonical
// form: 32 lower-case hexadecimal digits in the 8-4-4-4-12 grouping.
func WithProvider(id string) Option { return func(s *spec) { s.provider = id } }

// WithScope sets the tenant an impersonation is confined to: non-empty, with no
// white space or control character. It needs WithActor, and fails the test
// without it: a scope belongs to an impersonation.
func WithScope(tenant string) Option { return func(s *spec) { s.scope = tenant } }

// WithAttributes sets the read-only attribute set. The map is copied. Keys and
// values are held to the gateway's bounds: at most 32 entries, 4 KiB of keys
// and values together, keys of the form [a-z0-9][a-z0-9._-]{0,63}, values of
// valid UTF-8 without control characters and at most 256 bytes. The reserved
// prefixes idp. and authn. are not checked: the gateway cannot tell who wrote a
// key, so they are kept by whoever writes an attribute.
func WithAttributes(m map[string]string) Option {
	own := maps.Clone(m)
	return func(s *spec) { s.attributes = own }
}

// WithAccess sets the access level recorded for the route. The default is
// AccessRequired, a guarded route with no registration, which is the level a
// request that carries an identity usually has; AccessOptional is the other
// level the gateway pairs with an identity. AccessOpen and AccessUnknown are
// never paired with one, and Context fails the test that asks for either:
// Anonymous makes the contexts those levels belong to.
func WithAccess(a authn.Access) Option { return func(s *spec) { s.access = a } }

// Context returns ctx with an identity and an access level in it, the way the
// gateway leaves them: authn.FromContext reads the identity and
// authn.AccessFromContext reads the level.
//
// The identity is built from the options over these defaults: the account is
// DefaultAccount, the methods are the password alone, the assurance is
// AssuranceSingle, the time of authentication is DefaultAuthTime, the backend
// is DefaultBackend, the challenge is DefaultChallenge, the access level is
// AccessRequired, and there is no actor, session, scope, provider or attribute.
//
// A combination the gateway would not stamp fails the test, through t.Fatalf,
// naming the field: a malformed account, an impersonation without a session or
// a scope or with the actor equal to the account, a scope without an actor, a
// multi-factor declaration of an IdP with no multi-factor assurance or no
// provider, methods out of the set, a zero authentication time, a malformed
// backend name, challenge, scope, provider or attribute, an account or actor
// whose static prefix and backend disagree, and an access level of AccessOpen
// or AccessUnknown, which the gateway leaves no identity beside. The context
// returned on that path is ctx unchanged, which carries no identity.
func Context(t testing.TB, ctx context.Context, opts ...Option) context.Context {
	t.Helper()
	s := spec{
		account:   DefaultAccount,
		methods:   []authn.Method{authn.MethodPassword},
		assurance: authn.AssuranceSingle,
		authTime:  DefaultAuthTime(),
		backend:   DefaultBackend,
		challenge: DefaultChallenge,
		access:    authn.AccessRequired,
	}
	for _, opt := range opts {
		opt(&s)
	}
	if s.assurance == authn.AssuranceNone {
		s.assurance = authn.AssuranceSingle
	}
	if err := s.check(); err != nil {
		t.Fatalf("authntest: %v: no identity the gateway would stamp has this shape", err)
		return ctx
	}

	id := identity.New(model.Principal{
		Account:    s.account,
		Actor:      s.actor,
		Session:    s.session,
		Methods:    s.methods,
		Assurance:  s.assurance,
		IDPAssured: s.idpAssured,
		AuthTime:   s.authTime,
		Scope:      s.scope,
		Provider:   s.provider,
		Attributes: s.attributes,
	}, s.backend, s.challenge)
	return identity.WithAccess(identity.With(ctx, id), s.access)
}

// Anonymous returns ctx with an access level in it and no identity, the way the
// gateway leaves a request that carries none: AccessOpen on an exempt route and
// AccessOptional on an optional route that got no credential. authn.FromContext
// reads false and authn.AccessFromContext reads access, so a module's anonymous
// branch, which has to tell the two apart, is driven by the real thing.
//
// Any other level fails the test through t.Fatalf and returns ctx unchanged:
// AccessRequired never reaches a handler without an identity, because the
// gateway refuses the request first, and AccessUnknown is what a context the
// gateway never decided anything for already reads as, so there is nothing to
// build.
func Anonymous(t testing.TB, ctx context.Context, access authn.Access) context.Context {
	t.Helper()
	if access != authn.AccessOpen && access != authn.AccessOptional {
		t.Fatalf("authntest: the access level %d is not one the gateway leaves with no identity: "+
			"only AccessOpen and AccessOptional are", access)
		return ctx
	}
	return identity.WithAccess(ctx, access)
}

// check applies the gateway's checks before stamping, in the order the design
// lists them, and reports the first that fails.
func (s *spec) check() error {
	if _, err := authn.ParseAccountID(string(s.account)); err != nil {
		return fmt.Errorf("the account is not a well-formed identifier: %w", err)
	}
	if s.actor != "" {
		if _, err := authn.ParseAccountID(string(s.actor)); err != nil {
			return fmt.Errorf("the actor is not a well-formed identifier: %w", err)
		}
		if s.actor == s.account {
			return errors.New("the actor equals the account, which is no impersonation")
		}
		if s.session == "" {
			return errors.New("an actor without WithSession: an impersonation belongs to a login session")
		}
		if s.scope == "" {
			return errors.New("an actor without WithScope: an impersonation is confined to a tenant")
		}
	}
	if s.actor == "" && s.scope != "" {
		return errors.New("WithScope without WithActor: a scope belongs to an impersonation")
	}
	if err := s.checkMethods(); err != nil {
		return err
	}
	if s.assurance > authn.AssuranceMulti {
		return fmt.Errorf("the assurance %d is above AssuranceMulti", s.assurance)
	}
	if s.idpAssured {
		if s.assurance != authn.AssuranceMulti {
			return errors.New("WithIDPAssured(true) without a multi-factor assurance")
		}
		if s.provider == "" {
			return errors.New("WithIDPAssured(true) without WithProvider")
		}
	}
	if s.authTime.IsZero() {
		return errors.New("the time of authentication is the zero time")
	}
	if s.scope != "" && !isTenantShape(s.scope) {
		return errors.New("the scope is not the shape of a tenant identifier")
	}
	if s.provider != "" && !uuidText.MatchString(s.provider) {
		return errors.New("the provider is not UUID text")
	}
	if err := checkAttributes(s.attributes); err != nil {
		return err
	}
	if !backendName.MatchString(s.backend) {
		return errors.New("the backend name is not [a-z0-9][a-z0-9.-]*")
	}
	if !challengeScheme(s.challenge) {
		return errors.New("the challenge is not a scheme name the gateway writes: an RFC 9110 token " +
			"with no capital but its first character")
	}
	if err := s.checkNamespace(); err != nil {
		return err
	}
	switch s.access {
	case authn.AccessOptional, authn.AccessRequired:
	case authn.AccessOpen, authn.AccessUnknown:
		return fmt.Errorf("the access level %d is never paired with an identity: Anonymous builds the "+
			"context of an exempt route and of an optional route with no credential", s.access)
	default:
		return fmt.Errorf("the access level %d is not one of the defined levels", s.access)
	}
	return nil
}

func (s *spec) checkMethods() error {
	if len(s.methods) == 0 {
		return errors.New("no authentication method")
	}
	if len(s.methods) > maxMethods {
		return fmt.Errorf("more than %d authentication methods", maxMethods)
	}
	seen := make(map[authn.Method]bool, len(s.methods))
	for _, m := range s.methods {
		if !m.Valid() {
			return fmt.Errorf("the authentication method %q is not in the closed set", m)
		}
		if seen[m] {
			return fmt.Errorf("the authentication method %q is repeated", m)
		}
		seen[m] = true
	}
	return nil
}

func (s *spec) checkNamespace() error {
	for _, c := range []struct {
		name string
		id   authn.AccountID
	}{{"account", s.account}, {"actor", s.actor}} {
		if c.id == "" {
			continue
		}
		if c.id.IsStatic() != (s.backend == "static") {
			return fmt.Errorf("the %s and the backend disagree about the static namespace: "+
				"the prefix %q belongs to the backend named \"static\" and to no other", c.name, authn.StaticPrefix)
		}
	}
	return nil
}

// challengeScheme reports whether v is a scheme name as the gateway writes one
// into a challenge: an RFC 9110 token that is not in lower case at its first
// character and has no capital after it, the folded name with its first letter
// raised.
func challengeScheme(v string) bool {
	if v == "" {
		return false
	}
	for i := 0; i < len(v); i++ {
		c := v[i]
		switch {
		case c >= 'a' && c <= 'z' && i == 0, c >= 'A' && c <= 'Z' && i > 0:
			return false
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9',
			strings.IndexByte("!#$%&'*+-.^_`|~", c) >= 0:
		default:
			return false
		}
	}
	return true
}

func isTenantShape(v string) bool {
	return strings.IndexFunc(v, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) < 0
}

func checkAttributes(m map[string]string) error {
	if len(m) > maxAttributes {
		return fmt.Errorf("more than %d attributes", maxAttributes)
	}
	size := 0
	for k, v := range m {
		if !attributeKey.MatchString(k) {
			return fmt.Errorf("the attribute key %q is not a key the gateway admits", k)
		}
		if len(v) > maxValueLen || !utf8.ValidString(v) ||
			strings.IndexFunc(v, unicode.IsControl) >= 0 {
			return fmt.Errorf("the value of the attribute %q is not one the gateway admits", k)
		}
		size += len(k) + len(v)
	}
	if size > maxAttributeSize {
		return fmt.Errorf("the attributes hold more than %d bytes of keys and values", maxAttributeSize)
	}
	return nil
}
