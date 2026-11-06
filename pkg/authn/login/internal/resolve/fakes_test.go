package resolve

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/slug"
)

// store is an in-memory account store with the uniqueness semantics the real one
// has: an identifier value is held by one account, an external key is bound to
// one account, and a deleted account holds nothing. The interfaces it does not
// serve are left nil, so a call the resolver should not make panics.
type store struct {
	mu sync.Mutex

	accounts   map[authn.AccountID]*record
	identifier map[string]authn.AccountID // kind + "\x00" + value
	binding    map[string]authn.AccountID // provider + "\x00" + subject
	members    map[string]authn.Membership
	next       int

	// What the resolver did.
	created        []authn.CreateAccount
	deleted        []authn.AccountID
	linked         []authn.ExternalLink
	membershipPuts []authn.Membership
	membershipRead int
	discovered     []string
	other          []string // calls of any other write method

	// Providers, and the verified domains of enabled providers.
	providers map[string]authn.SSOProvider
	domains   map[string][]authn.SSOProvider

	// Hooks and injected failures.
	beforeCreate func(authn.CreateAccount)
	afterCreate  func()
	beforeLink   func(authn.AccountID, authn.ExternalLink)
	failLink     error
	failPut      error
	failCreate   error
	failDiscover error
	failGet      error
	failByExt    error
	failExtern   error
	failTenants  error
}

type record struct {
	authn.Account
	identifiers []authn.Identifier
	bindings    []authn.ExternalBinding
}

func newStore() *store {
	return &store{
		accounts:   map[authn.AccountID]*record{},
		identifier: map[string]authn.AccountID{},
		binding:    map[string]authn.AccountID{},
		members:    map[string]authn.Membership{},
		providers:  map[string]authn.SSOProvider{},
		domains:    map[string][]authn.SSOProvider{},
	}
}

func ikey(kind authn.IdentifierKind, value string) string { return string(kind) + "\x00" + value }
func bkey(provider, subject string) string                { return provider + "\x00" + subject }
func mkey(tenant string, id authn.AccountID) string       { return tenant + "\x00" + string(id) }

func normalize(kind authn.IdentifierKind, value string) (string, error) {
	switch kind {
	case authn.IdentifierEmail:
		v := strings.ToLower(strings.TrimSpace(value))
		if strings.Count(v, "@") != 1 {
			return "", authn.ErrInvalidIdentifier
		}
		return v, nil
	case authn.IdentifierPhone:
		if !strings.HasPrefix(value, "+") {
			return "", authn.ErrInvalidIdentifier
		}
		return value, nil
	}
	return strings.ToLower(value), nil
}

// seed is an account put in the store as a fixture.
type seed struct {
	id          authn.AccountID
	status      authn.AccountStatus
	kind        authn.AccountKind
	password    bool
	mfaRequired bool
	email       string
	emailVia    string
	phone       string
	// phoneNotSignIn seeds a phone the account holds but did not choose as a
	// sign-in way.
	phoneNotSignIn bool
	username       string
	bindings       [][2]string
	tenants        []string
	suspended      []string
}

func (s *store) seed(t *testing.T, sd seed) authn.AccountID {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if sd.kind == "" {
		sd.kind = authn.KindUser
	}
	rec := &record{Account: authn.Account{ID: sd.id, Kind: sd.kind, Status: sd.status, PasswordSet: sd.password, MFARequired: sd.mfaRequired, DisplayName: "seeded"}}
	add := func(kind authn.IdentifierKind, value, via string) {
		if value == "" {
			return
		}
		v, err := normalize(kind, value)
		if err != nil {
			t.Fatalf("seed %s %q: %v", kind, value, err)
		}
		// A seeded account stands for one that predates the two axes: its
		// username and phone are sign-in ways, as the migration fills them,
		// unless the case seeds a phone the holder left out of sign-in.
		signIn := kind == authn.IdentifierUsername ||
			(kind == authn.IdentifierPhone && !sd.phoneNotSignIn)
		rec.identifiers = append(rec.identifiers, authn.Identifier{
			ID: fmt.Sprintf("i%d", len(rec.identifiers)), Kind: kind, Value: v, VerifiedVia: via, SignIn: signIn,
		})
		s.identifier[ikey(kind, v)] = sd.id
	}
	add(authn.IdentifierEmail, sd.email, sd.emailVia)
	add(authn.IdentifierPhone, sd.phone, authn.VerifiedViaSMSCode)
	add(authn.IdentifierUsername, sd.username, authn.VerifiedViaAdmin)
	for _, b := range sd.bindings {
		rec.bindings = append(rec.bindings, authn.ExternalBinding{ID: fmt.Sprintf("b%d", len(rec.bindings)), Provider: b[0], Subject: b[1]})
		s.binding[bkey(b[0], b[1])] = sd.id
	}
	s.accounts[sd.id] = rec
	for _, tn := range sd.tenants {
		s.members[mkey(tn, sd.id)] = authn.Membership{Account: sd.id, Tenant: tn, Active: true, Source: "admin"}
	}
	for _, tn := range sd.suspended {
		s.members[mkey(tn, sd.id)] = authn.Membership{Account: sd.id, Tenant: tn, Source: "admin"}
	}
	return sd.id
}

// live is the accounts that exist and are not deleted.
func (s *store) live() []authn.AccountID {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []authn.AccountID
	for id, r := range s.accounts {
		if r.Status != authn.StatusDeleted {
			out = append(out, id)
		}
	}
	slices.Sort(out)
	return out
}

func (s *store) boundTo(provider, subject string) (authn.AccountID, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id, ok := s.binding[bkey(provider, subject)]
	return id, ok
}

func (s *store) bindingsOf(id authn.AccountID) []authn.ExternalBinding {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.accounts[id].bindings)
}

func (s *store) identifiersOf(id authn.AccountID) []authn.Identifier {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.accounts[id].identifiers)
}

func (s *store) account(id authn.AccountID) authn.Account {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.accounts[id].Clone()
}

func (s *store) membershipOf(tenant string, id authn.AccountID) (authn.Membership, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, ok := s.members[mkey(tenant, id)]
	return m, ok
}

// writes is how many writes of any kind the resolver made.
func (s *store) writes() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.created) + len(s.deleted) + len(s.linked) + len(s.membershipPuts) + len(s.other)
}

// accountsView is the Accounts the resolver reads.
type accountsView struct {
	authn.Accounts
	s *store
}

func (v accountsView) Get(_ context.Context, id authn.AccountID) (authn.Account, error) {
	v.s.mu.Lock()
	defer v.s.mu.Unlock()
	if v.s.failGet != nil {
		return authn.Account{}, v.s.failGet
	}
	r, ok := v.s.accounts[id]
	if !ok || r.Status == authn.StatusDeleted {
		return authn.Account{}, authn.ErrAccountNotFound
	}
	return r.Clone(), nil
}

func (v accountsView) Lookup(_ context.Context, kind authn.IdentifierKind, value string) (authn.AccountID, error) {
	n, err := normalize(kind, value)
	if err != nil {
		return "", err
	}
	v.s.mu.Lock()
	defer v.s.mu.Unlock()
	id, ok := v.s.identifier[ikey(kind, n)]
	if !ok {
		return "", authn.ErrAccountNotFound
	}
	return id, nil
}

func (v accountsView) ByExternal(_ context.Context, provider, subject string) (authn.AccountID, error) {
	v.s.mu.Lock()
	defer v.s.mu.Unlock()
	if v.s.failByExt != nil {
		return "", v.s.failByExt
	}
	id, ok := v.s.binding[bkey(provider, subject)]
	if !ok {
		return "", authn.ErrAccountNotFound
	}
	return id, nil
}

func (v accountsView) Identifiers(_ context.Context, id authn.AccountID) ([]authn.Identifier, error) {
	v.s.mu.Lock()
	defer v.s.mu.Unlock()
	r, ok := v.s.accounts[id]
	if !ok || r.Status == authn.StatusDeleted {
		return nil, authn.ErrAccountNotFound
	}
	return slices.Clone(r.identifiers), nil
}

func (v accountsView) Externals(_ context.Context, id authn.AccountID) ([]authn.ExternalBinding, error) {
	v.s.mu.Lock()
	defer v.s.mu.Unlock()
	if v.s.failExtern != nil {
		return nil, v.s.failExtern
	}
	r, ok := v.s.accounts[id]
	if !ok || r.Status == authn.StatusDeleted {
		return nil, authn.ErrAccountNotFound
	}
	return slices.Clone(r.bindings), nil
}

// adminView is the AccountAdmin the resolver writes through.
type adminView struct {
	authn.AccountAdmin
	s *store
}

func (v adminView) Create(_ context.Context, in authn.CreateAccount) (authn.Account, error) {
	if v.s.beforeCreate != nil {
		v.s.beforeCreate(in)
	}
	acct, err := v.create(in)
	if v.s.afterCreate != nil {
		v.s.afterCreate()
	}
	return acct, err
}

func (v adminView) create(in authn.CreateAccount) (authn.Account, error) {
	s := v.s
	s.mu.Lock()
	defer s.mu.Unlock()
	s.created = append(s.created, in.Clone())
	if s.failCreate != nil {
		return authn.Account{}, s.failCreate
	}
	rec := &record{}
	var keys []string
	add := func(kind authn.IdentifierKind, value string, via authn.Verification, signIn bool) error {
		if value == "" {
			return nil
		}
		if !via.Valid() {
			return fmt.Errorf("%w: unverified %s", authn.ErrInvalidArgument, kind)
		}
		n, err := normalize(kind, value)
		if err != nil {
			return err
		}
		k := ikey(kind, n)
		if _, taken := s.identifier[k]; taken {
			return authn.ErrAccountExists
		}
		keys = append(keys, k)
		rec.identifiers = append(rec.identifiers, authn.Identifier{
			ID: fmt.Sprintf("i%d", len(rec.identifiers)), Kind: kind, Value: n, VerifiedVia: via.Via, SignIn: signIn,
		})
		return nil
	}
	if err := add(authn.IdentifierEmail, in.Email, in.EmailVerifiedVia, false); err != nil {
		return authn.Account{}, err
	}
	// The phone an SMS first login proved is a sign-in way from the start; any
	// other phone row starts on neither axis. The username the create is given is
	// recorded on the CreateAccount itself, not as an identifier: these cases
	// read the creation, and the account module's own username handling is
	// exercised against the real store.
	if err := add(authn.IdentifierPhone, in.Phone, in.PhoneVerifiedVia,
		in.PhoneVerifiedVia.Via == authn.VerifiedViaSMSCode); err != nil {
		return authn.Account{}, err
	}
	s.next++
	id := authn.AccountID(fmt.Sprintf("acct-%d", s.next))
	rec.Account = authn.Account{ID: id, Kind: in.Kind, Status: authn.StatusActive, DisplayName: in.DisplayName, Attributes: in.Attributes, MFARequired: in.MFARequired, PasswordSet: in.Password != "" || in.PasswordHash != ""}
	for _, k := range keys {
		s.identifier[k] = id
	}
	s.accounts[id] = rec
	return rec.Clone(), nil
}

func (v adminView) LinkExternal(_ context.Context, id authn.AccountID, l authn.ExternalLink) error {
	if v.s.beforeLink != nil {
		v.s.beforeLink(id, l)
	}
	s := v.s
	s.mu.Lock()
	defer s.mu.Unlock()
	s.linked = append(s.linked, l)
	if s.failLink != nil {
		return s.failLink
	}
	r, ok := s.accounts[id]
	if !ok || r.Status == authn.StatusDeleted {
		return authn.ErrAccountNotFound
	}
	k := bkey(l.Provider, l.Subject)
	if owner, taken := s.binding[k]; taken {
		if owner != id {
			return authn.ErrBindingConflict
		}
		for i := range r.bindings {
			if r.bindings[i].Provider == l.Provider && r.bindings[i].Subject == l.Subject {
				r.bindings[i].Email, r.bindings[i].EmailVerified = l.Email, l.EmailVerified
			}
		}
		return nil
	}
	s.binding[k] = id
	r.bindings = append(r.bindings, authn.ExternalBinding{ID: fmt.Sprintf("b%d", len(r.bindings)), Provider: l.Provider, Subject: l.Subject, Email: l.Email, EmailVerified: l.EmailVerified})
	return nil
}

func (v adminView) Delete(ctx context.Context, id authn.AccountID, reason string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s := v.s
	s.mu.Lock()
	defer s.mu.Unlock()
	if !authn.ValidPayloadReasonClass(reason) {
		return authn.ErrInvalidArgument
	}
	s.deleted = append(s.deleted, id)
	r, ok := s.accounts[id]
	if !ok {
		return authn.ErrAccountNotFound
	}
	for _, i := range r.identifiers {
		delete(s.identifier, ikey(i.Kind, i.Value))
	}
	for _, b := range r.bindings {
		delete(s.binding, bkey(b.Provider, b.Subject))
	}
	for k, m := range s.members {
		if m.Account == id {
			delete(s.members, k)
		}
	}
	r.identifiers, r.bindings = nil, nil
	r.Status = authn.StatusDeleted
	return nil
}

// membershipsView counts what the resolver does with memberships.
type membershipsView struct {
	authn.Memberships
	s *store
}

func (v membershipsView) Tenants(_ context.Context, id authn.AccountID) ([]authn.Membership, error) {
	v.s.mu.Lock()
	defer v.s.mu.Unlock()
	v.s.membershipRead++
	if v.s.failTenants != nil {
		return nil, v.s.failTenants
	}
	var out []authn.Membership
	for _, m := range v.s.members {
		if m.Account == id {
			out = append(out, m)
		}
	}
	return out, nil
}

func (v membershipsView) Put(_ context.Context, m authn.Membership) error {
	v.s.mu.Lock()
	defer v.s.mu.Unlock()
	v.s.membershipPuts = append(v.s.membershipPuts, m)
	if v.s.failPut != nil {
		return v.s.failPut
	}
	if r, ok := v.s.accounts[m.Account]; !ok || r.Status == authn.StatusDeleted {
		return authn.ErrAccountNotFound
	}
	v.s.members[mkey(m.Tenant, m.Account)] = m
	return nil
}

// ssoView is the register of providers.
type ssoView struct {
	authn.SSOProviders
	s *store
}

func (v ssoView) Get(_ context.Context, id string) (authn.SSOProvider, error) {
	p, ok := v.s.providers[id]
	if !ok {
		return authn.SSOProvider{}, fmt.Errorf("no provider %q", id)
	}
	return p, nil
}

func (v ssoView) Discover(_ context.Context, domain string) ([]authn.SSOProvider, error) {
	v.s.mu.Lock()
	defer v.s.mu.Unlock()
	v.s.discovered = append(v.s.discovered, domain)
	if v.s.failDiscover != nil {
		return nil, v.s.failDiscover
	}
	return slices.Clone(v.s.domains[domain]), nil
}

// provisionerFunc is a Provisioner.
type provisionerFunc func(ctx context.Context, p authn.CreateAccount, via string) (authn.CreateAccount, error)

func (f provisionerFunc) Provision(ctx context.Context, p authn.CreateAccount, via string) (authn.CreateAccount, error) {
	return f(ctx, p, via)
}

// rendezvous lets n callers wait for each other, so that a test can make
// concurrent logins pass a point together.
type rendezvous struct {
	mu   sync.Mutex
	n    int
	left int
	ch   chan struct{}
}

func newRendezvous(n int) *rendezvous { return &rendezvous{n: n, left: n, ch: make(chan struct{})} }

func (r *rendezvous) wait() {
	r.mu.Lock()
	r.left--
	if r.left == 0 {
		close(r.ch)
	}
	r.mu.Unlock()
	<-r.ch
}

// The fixture's tenant and providers.
const (
	tenantAcme = "acme"
	idpAcme    = "okta-1"
	idpOther   = "okta-2"
	tenantElse = "else"
)

// env is a resolver over a store.
type env struct {
	t  *testing.T
	s  *store
	r  *Resolver
	cf Config
}

type option func(*Config, *store)

func withSignup(v Signup) option { return func(c *Config, _ *store) { c.Signup = v } }
func withoutSSO() option         { return func(c *Config, _ *store) { c.SSO = nil } }
func withProvisioner(p provisionerFunc) option {
	return func(c *Config, _ *store) { c.Provisioner = p }
}

// withProvider registers an enterprise provider of tenantAcme with the given
// provisioning and merge switch, and its verified domain acme.com. A JIT
// provider asks the person for a username, which is the strategy the older
// cases exercised; a claim strategy is added by withClaimProvider.
func withProvider(provisioning string, merge bool) option {
	return func(_ *Config, s *store) {
		p := authn.SSOProvider{
			ID: idpAcme, Tenant: tenantAcme, Protocol: authn.SSOProtocolOIDC, Enabled: true,
			Provisioning: provisioning, MergeExisting: merge,
		}
		if provisioning == authn.ProvisioningJIT {
			p.UsernameStrategy = authn.UsernameStrategyUserConfirm
		}
		s.providers[idpAcme] = p
		s.domains["acme.com"] = []authn.SSOProvider{p}
	}
}

// withClaimProvider registers a just-in-time provider of tenantAcme that reads
// its username from the given claim.
func withClaimProvider(claim string) option {
	return func(_ *Config, s *store) {
		p := authn.SSOProvider{
			ID: idpAcme, Tenant: tenantAcme, Protocol: authn.SSOProtocolOIDC, Enabled: true,
			Provisioning: authn.ProvisioningJIT, UsernameStrategy: authn.UsernameStrategyClaim, UsernameClaim: claim,
		}
		s.providers[idpAcme] = p
		s.domains["acme.com"] = []authn.SSOProvider{p}
	}
}

func newEnv(t *testing.T, opts ...option) *env {
	t.Helper()
	s := newStore()
	cfg := Config{
		Accounts:          accountsView{s: s},
		Admin:             adminView{s: s},
		Memberships:       membershipsView{s: s},
		SSO:               ssoView{s: s},
		NormalizeUsername: slug.Normalize,
	}
	for _, o := range opts {
		o(&cfg, s)
	}
	r, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return &env{t: t, s: s, r: r, cf: cfg}
}

// testUsername is the login name the first login of these cases opens an
// account with, standing in for the username the pending-signup step collects.
const testUsername = "ann"

// testPassword is the password the social and phone first logins of these cases
// open their accounts with, standing in for the one their step collects.
const testPassword = "correct horse battery staple"

// passwordOf is the password the engine hands Create for a pending signup: the
// social and phone steps' own, and none for the enterprise path, which takes
// none.
func passwordOf(p Pending) string {
	if p.Source == SourceSocial || p.Source == SourcePhone {
		return testPassword
	}
	return ""
}

// resolve runs a first login the way the engine does: it resolves the proof and,
// when the resolution parks at the pending signup, opens the account with the
// test username.
func (e *env) resolve(p authn.Proof) (Result, error) {
	e.t.Helper()
	return e.resolveAs(testUsername, p)
}

// resolveAs is resolve with a chosen username.
func (e *env) resolveAs(username string, p authn.Proof) (Result, error) {
	e.t.Helper()
	res, err := e.pending(p)
	if err != nil || res.Pending == nil {
		return res, err
	}
	return e.r.Create(context.Background(), *res.Pending, username, passwordOf(*res.Pending), "")
}

// pending resolves a first login without opening the account, which is what the
// engine does when it parks the flow at the pending signup.
func (e *env) pending(p authn.Proof) (Result, error) {
	e.t.Helper()
	return e.r.Resolve(context.Background(), Request{Proof: p, Purpose: authn.PurposeLogin})
}

func social(provider, subject, email string, verified bool) authn.Proof {
	return authn.Proof{External: &authn.External{Provider: provider, Subject: subject, Email: email, EmailVerified: verified, DisplayName: "Ann"}}
}

func enterprise(subject, email string) authn.Proof {
	return authn.Proof{
		Provider: idpAcme,
		External: &authn.External{Provider: "oidc:" + idpAcme, Subject: subject, Email: email, Tenant: tenantAcme, DisplayName: "Ann"},
	}
}

// enterpriseWithUsername is an enterprise proof that asserts a preferred
// username, for the claim strategy.
func enterpriseWithUsername(subject, email, preferredUsername string) authn.Proof {
	p := enterprise(subject, email)
	p.External.PreferredUsername = preferredUsername
	return p
}

func phone(value string) authn.Proof {
	return authn.Proof{Identifier: &authn.Identifier{Kind: authn.IdentifierPhone, Value: value, VerifiedVia: authn.VerifiedViaSMSCode}}
}

// contextWithCancelOnLink is a context that is cancelled when the resolver binds
// an identity, so that what follows runs on a cancelled context.
func contextWithCancelOnLink(e *env) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	e.s.beforeLink = func(authn.AccountID, authn.ExternalLink) { cancel() }
	return ctx, cancel
}
