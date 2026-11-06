package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http/httptest"
	"reflect"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/authn/internal/mount/mounttest"
	"github.com/vislake/speed/pkg/settings"
)

// The identifiers and tenants a case starts from.
const (
	adminID    = authn.AccountID("acct-admin")
	userID     = authn.AccountID("acct-user")
	tenantA    = "tenant-a"
	tenantB    = "tenant-b"
	sessionA   = authn.SessionID("0190a6f2-3b6c-7c2e-8a4d-5e6f7a8b9c0d")
	idpA       = "0190a6f2-3b6c-7c2e-8a4d-5e6f7a8b9c0e"
	grantA     = "0190a6f2-3b6c-7c2e-8a4d-5e6f7a8b9c0f"
	secretA    = "secret-1"
	factorA    = "factor-1"
	identifier = "identifier-1"
)

// logBuffer collects what the module logs.
type logBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *logBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *logBuffer) logger() *slog.Logger {
	return slog.New(slog.NewTextHandler(b, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

func (b *logBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// recordingAudit keeps the rows it was asked to write and can be told to fail.
type recordingAudit struct {
	mu   sync.Mutex
	rows []audit.Entry
	err  error
}

func (a *recordingAudit) Record(_ context.Context, e audit.Entry) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.rows = append(a.rows, e)
	return a.err
}

func (a *recordingAudit) entries() []audit.Entry {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]audit.Entry(nil), a.rows...)
}

func (a *recordingAudit) fails(err error) { a.mu.Lock(); a.err = err; a.mu.Unlock() }

// fakeDecider answers what a case scripts and counts its calls.
type fakeDecider struct {
	mu    sync.Mutex
	allow bool
	err   error
	calls []authn.Decision
}

func (d *fakeDecider) Decide(_ context.Context, dec authn.Decision) (bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.calls = append(d.calls, dec)
	return d.allow, d.err
}

func (d *fakeDecider) count() int { d.mu.Lock(); defer d.mu.Unlock(); return len(d.calls) }

func (d *fakeDecider) last() (authn.Decision, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.calls) == 0 {
		return authn.Decision{}, false
	}
	return d.calls[len(d.calls)-1], true
}

// fakeAccounts is the account read surface.
type fakeAccounts struct {
	authn.Accounts
	mu              sync.Mutex
	accounts        map[authn.AccountID]authn.Account
	order           []authn.AccountID
	secrets         map[authn.AccountID][]authn.ServiceSecretInfo
	identifiers     map[authn.AccountID][]authn.Identifier
	listErr         error
	secretErr       error
	identifierErr   error
	listCalls       int
	secretCalls     int
	identifierCalls int
	// identifierBatches counts the batch identifier reads, so a case can hold
	// that one page costs one read rather than one per row.
	identifierBatches int
	// identifierReads counts the identifier reads per account, so a case can
	// hold that a tombstone's identifiers are never read.
	identifierReads map[authn.AccountID]int
	// vanished holds accounts the page read still returns but whose identifier
	// read reports gone, modelling a delete between the two reads.
	vanished map[authn.AccountID]bool
	// lastQuery is the query the most recent List was handed, so a case can hold
	// what the handler parsed out of the request.
	lastQuery authn.AccountQuery
}

func newAccounts() *fakeAccounts {
	return &fakeAccounts{
		accounts:        map[authn.AccountID]authn.Account{},
		secrets:         map[authn.AccountID][]authn.ServiceSecretInfo{},
		identifiers:     map[authn.AccountID][]authn.Identifier{},
		identifierReads: map[authn.AccountID]int{},
		vanished:        map[authn.AccountID]bool{},
	}
}

// putIdentifiers binds identifiers to an account, in the order given, so a case
// can hold the order the view sorts into against an order that differs.
func (a *fakeAccounts) putIdentifiers(id authn.AccountID, identifiers ...authn.Identifier) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.identifiers[id] = identifiers
}

func (a *fakeAccounts) Identifiers(_ context.Context, id authn.AccountID) ([]authn.Identifier, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.identifierCalls++
	a.identifierReads[id]++
	if a.identifierErr != nil {
		return nil, a.identifierErr
	}
	// The store reports a missing account and a tombstone as
	// authn.ErrAccountNotFound; the fake holds the same contract so a case can
	// exercise a read the real capability would refuse.
	acct, ok := a.accounts[id]
	if !ok || acct.Status == authn.StatusDeleted || a.vanished[id] {
		return nil, authn.ErrAccountNotFound
	}
	return append([]authn.Identifier(nil), a.identifiers[id]...), nil
}

// IdentifiersMany answers the batch read, omitting an unknown account, a
// tombstone and one that vanished, the same absence the single read reports as
// authn.ErrAccountNotFound.
func (a *fakeAccounts) IdentifiersMany(_ context.Context, ids []authn.AccountID) (map[authn.AccountID][]authn.Identifier, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.identifierBatches++
	if a.identifierErr != nil {
		return nil, a.identifierErr
	}
	out := make(map[authn.AccountID][]authn.Identifier, len(ids))
	for _, id := range ids {
		a.identifierReads[id]++
		acct, ok := a.accounts[id]
		if !ok || acct.Status == authn.StatusDeleted || a.vanished[id] {
			continue
		}
		out[id] = append([]authn.Identifier{}, a.identifiers[id]...)
	}
	return out, nil
}

// failIdentifiers makes every identifier read fail, so a case can hold what a
// page does when the store does not answer.
func (a *fakeAccounts) failIdentifiers(err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.identifierErr = err
}

// identifierBatchCalls reports how many times the batch identifier read ran.
func (a *fakeAccounts) identifierBatchCalls() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.identifierBatches
}

// identifierSingleCalls reports how many times the per-account identifier read
// ran.
func (a *fakeAccounts) identifierSingleCalls() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.identifierCalls
}

// vanish makes the identifier read of one account report it gone while the
// page read still returns it, so a case can hold a delete between the two
// reads.
func (a *fakeAccounts) vanish(id authn.AccountID) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.vanished[id] = true
}

// identifiersRead reports how many times the identifier read ran for one
// account.
func (a *fakeAccounts) identifiersRead(id authn.AccountID) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.identifierReads[id]
}

// lastList is the query the most recent List was handed, taken under the lock so
// a case reads it without racing the handler.
func (a *fakeAccounts) lastList() authn.AccountQuery {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.lastQuery
}

func (a *fakeAccounts) put(id authn.AccountID, acct authn.Account) {
	a.mu.Lock()
	defer a.mu.Unlock()
	acct.ID = id
	a.accounts[id] = acct
	a.order = append(a.order, id)
}

func (a *fakeAccounts) Get(_ context.Context, id authn.AccountID) (authn.Account, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	acct, ok := a.accounts[id]
	if !ok {
		return authn.Account{}, authn.ErrAccountNotFound
	}
	return acct, nil
}

func (a *fakeAccounts) List(_ context.Context, q authn.AccountQuery) ([]authn.Account, string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.listCalls++
	a.lastQuery = q
	if a.listErr != nil {
		return nil, "", a.listErr
	}
	out := make([]authn.Account, 0, len(a.order))
	for _, id := range a.order {
		out = append(out, a.accounts[id])
	}
	return out, "", nil
}

func (a *fakeAccounts) ListServiceSecrets(_ context.Context, id authn.AccountID) ([]authn.ServiceSecretInfo, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.secretCalls++
	if a.secretErr != nil {
		return nil, a.secretErr
	}
	return append([]authn.ServiceSecretInfo(nil), a.secrets[id]...), nil
}

// fakeAccountAdmin is the account write surface, counting each call.
type fakeAccountAdmin struct {
	authn.AccountAdmin
	mu         sync.Mutex
	calls      map[string]int
	err        error
	created    authn.CreateAccount
	status     authn.AccountStatus
	secret     string
	secretID   string
	addedKind  authn.IdentifierKind
	addedValue string
	addedVia   authn.Verification
	// loginWay is what HasLoginWayOtherThanPasskey answers. It defaults to true,
	// the ordinary account, so only the cases about the lock-out guard set it
	// false.
	loginWay bool
}

func newAccountAdmin() *fakeAccountAdmin {
	return &fakeAccountAdmin{calls: map[string]int{}, loginWay: true}
}

func (a *fakeAccountAdmin) note(op string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.calls[op]++
	return a.err
}

func (a *fakeAccountAdmin) count(op string) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.calls[op]
}

func (a *fakeAccountAdmin) Create(_ context.Context, in authn.CreateAccount) (authn.Account, error) {
	if err := a.note("create"); err != nil {
		return authn.Account{}, err
	}
	a.mu.Lock()
	a.created = in
	a.mu.Unlock()
	return authn.Account{ID: userID, Kind: in.Kind, Status: authn.StatusActive}, nil
}

func (a *fakeAccountAdmin) SetStatus(_ context.Context, _ authn.AccountID, s authn.AccountStatus, _ string) error {
	a.mu.Lock()
	a.status = s
	a.mu.Unlock()
	return a.note("set-status")
}

func (a *fakeAccountAdmin) Delete(_ context.Context, _ authn.AccountID, _ string) error {
	return a.note("delete")
}

func (a *fakeAccountAdmin) SetDisplayName(_ context.Context, _ authn.AccountID, _ string) error {
	return a.note("set-display-name")
}

func (a *fakeAccountAdmin) SetAttributes(_ context.Context, _ authn.AccountID, _ map[string]string) error {
	return a.note("set-attributes")
}

func (a *fakeAccountAdmin) SetProtected(_ context.Context, _ authn.AccountID, _ bool) error {
	return a.note("set-protected")
}

func (a *fakeAccountAdmin) SetMFARequired(_ context.Context, _ authn.AccountID, _ bool) error {
	return a.note("set-mfa-required")
}

func (a *fakeAccountAdmin) AddIdentifier(_ context.Context, _ authn.AccountID, kind authn.IdentifierKind, value string, via authn.Verification) error {
	a.mu.Lock()
	a.addedKind, a.addedValue, a.addedVia = kind, value, via
	a.mu.Unlock()
	return a.note("add-identifier")
}

func (a *fakeAccountAdmin) RemoveIdentifier(_ context.Context, _ authn.AccountID, _ string) error {
	return a.note("remove-identifier")
}

func (a *fakeAccountAdmin) HasLoginWayOtherThanPasskey(_ context.Context, _ authn.AccountID) (bool, error) {
	a.mu.Lock()
	way := a.loginWay
	a.mu.Unlock()
	return way, a.note("has-login-way")
}

func (a *fakeAccountAdmin) CreateServiceSecret(_ context.Context, _ authn.AccountID, _ string, _ *time.Time) (string, string, error) {
	if err := a.note("create-secret"); err != nil {
		return "", "", err
	}
	return a.secretID, a.secret, nil
}

func (a *fakeAccountAdmin) RevokeServiceSecret(_ context.Context, _ authn.AccountID, _ string) error {
	return a.note("revoke-secret")
}

// fakePasswords is the password capability.
type fakePasswords struct {
	authn.Passwords
	mu    sync.Mutex
	calls int
	opts  authn.SetPasswordOptions
	err   error
}

func (p *fakePasswords) Set(_ context.Context, _ authn.AccountID, _ string, o authn.SetPasswordOptions) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	p.opts = o
	return p.err
}

// fakeMemberships is the membership store.
type fakeMemberships struct {
	authn.Memberships
	mu        sync.Mutex
	member    map[string]authn.Membership
	puts      []authn.Membership
	rmd       int
	listCalls int
	lastQuery authn.MemberQuery
	queries   []authn.MemberQuery
	err       error
}

func newMemberships() *fakeMemberships {
	return &fakeMemberships{member: map[string]authn.Membership{}}
}

func memKey(tenant string, id authn.AccountID) string { return tenant + "/" + string(id) }

func (f *fakeMemberships) put(m authn.Membership) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.member[memKey(m.Tenant, m.Account)] = m
}

func (f *fakeMemberships) Membership(_ context.Context, tenant string, id authn.AccountID) (authn.Membership, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, ok := f.member[memKey(tenant, id)]
	return m, ok, nil
}

func (f *fakeMemberships) Members(_ context.Context, tenant string, query authn.MemberQuery) ([]authn.Membership, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listCalls++
	f.lastQuery = query
	f.queries = append(f.queries, query)
	var out []authn.Membership
	for _, m := range f.member {
		if m.Tenant == tenant && (query.After == "" || m.Account.String() > query.After) {
			out = append(out, m)
		}
	}
	// The real read orders by account identifier and applies the page bounds,
	// so a case about cursors observes the same page a request would.
	slices.SortFunc(out, func(a, b authn.Membership) int {
		switch {
		case a.Account < b.Account:
			return -1
		case a.Account > b.Account:
			return 1
		default:
			return 0
		}
	})
	if limit := query.EffectiveLimit(); len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (f *fakeMemberships) Put(_ context.Context, m authn.Membership) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.member[memKey(m.Tenant, m.Account)] = m
	f.puts = append(f.puts, m)
	return nil
}

func (f *fakeMemberships) Remove(_ context.Context, id authn.AccountID, tenant string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	delete(f.member, memKey(tenant, id))
	f.rmd++
	return nil
}

// fakeSessions is the session store.
type fakeSessions struct {
	authn.Sessions
	mu          sync.Mutex
	infos       map[authn.SessionID]authn.SessionInfo
	revoked     int
	revokedAcct int
	listCalls   int
	err         error
	// revokeErr fails the revocation alone, so a case can drive an action that
	// fails after the set it is about to end has been read.
	revokeErr error
}

func newSessions() *fakeSessions {
	return &fakeSessions{infos: map[authn.SessionID]authn.SessionInfo{}}
}

func (s *fakeSessions) put(info authn.SessionInfo) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.infos[info.ID] = info
}

func (s *fakeSessions) Get(_ context.Context, id authn.SessionID) (authn.SessionInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return authn.SessionInfo{}, s.err
	}
	info, ok := s.infos[id]
	if !ok {
		return authn.SessionInfo{}, authn.ErrSessionNotFound
	}
	return info, nil
}

func (s *fakeSessions) List(_ context.Context, account authn.AccountID) ([]authn.SessionInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.listCalls++
	var out []authn.SessionInfo
	for _, info := range s.infos {
		if info.Account == account {
			out = append(out, info)
		}
	}
	return out, nil
}

func (s *fakeSessions) Revoke(_ context.Context, _ authn.SessionID, _ string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return s.err
	}
	s.revoked++
	return nil
}

// revocable is the account's browser and token identifiers, the set the real
// RevocableSessions hands out and RevokeAccount ends. Service sessions are not
// in it, and neither read nor write looks at a row's deadline: the statement
// selects on revoked_at alone.
func (s *fakeSessions) revocable(account authn.AccountID) []authn.SessionID {
	var out []authn.SessionID
	for _, info := range s.infos {
		if info.Account == account && info.Kind != authn.SessionService {
			out = append(out, info.ID)
		}
	}
	slices.Sort(out)
	return out
}

func (s *fakeSessions) RevocableSessions(_ context.Context, account authn.AccountID) ([]authn.SessionID, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return nil, s.err
	}
	return s.revocable(account), nil
}

func (s *fakeSessions) RevokeAccount(_ context.Context, account authn.AccountID, _ authn.SessionID, _ string) ([]authn.SessionID, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return nil, s.err
	}
	if s.revokeErr != nil {
		return nil, s.revokeErr
	}
	s.revokedAcct++
	return s.revocable(account), nil
}

// fakeMFA is the multi-factor composition.
type fakeMFA struct {
	authn.MFA
	mu          sync.Mutex
	factors     []authn.FactorRecord
	seasoned    bool
	resets      int
	factorCalls int
	err         error
}

func (f *fakeMFA) Factors(_ context.Context, _ authn.AccountID) ([]authn.FactorRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.factorCalls++
	return append([]authn.FactorRecord(nil), f.factors...), nil
}

func (f *fakeMFA) Reset(_ context.Context, _ authn.AccountID, _ string) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return 0, f.err
	}
	f.resets++
	return len(f.factors), nil
}

func (f *fakeMFA) Seasoned(_ context.Context, _ authn.AccountID) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.seasoned, nil
}

// fakeTokens stands in for the token capability, which only gates the secret
// group.
type fakeTokens struct{ authn.Tokens }

// fakeProviders is the SSO provider register.
type fakeProviders struct {
	authn.SSOProviders
	mu          sync.Mutex
	byID        map[string]authn.SSOProvider
	order       []string
	puts        []authn.SSOProvider
	deletes     int
	domains     []string
	reverify    int
	listCalls   int
	domainReads int
	putErr      error
	getErr      error
	claimErr    error
	domainErr   error
	claimTok    string
	onPutCall   func()
	claims      map[string][]authn.SSOProviderDomain
}

func newProviders() *fakeProviders {
	return &fakeProviders{
		byID:     map[string]authn.SSOProvider{},
		claims:   map[string][]authn.SSOProviderDomain{},
		claimTok: "verify-token",
	}
}

// claimDomain records one email domain a provider has claimed, verified or not,
// so the views' domain read has something to report.
func (f *fakeProviders) claimDomain(id string, domain authn.SSOProviderDomain) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.claims[id] = append(f.claims[id], domain)
}

// Domains answers the single-provider domain read.
func (f *fakeProviders) Domains(_ context.Context, id string) ([]authn.SSOProviderDomain, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.domainErr != nil {
		return nil, f.domainErr
	}
	f.domainReads++
	return append([]authn.SSOProviderDomain{}, f.claims[id]...), nil
}

// DomainsMany answers the batch domain read the list builds from.
func (f *fakeProviders) DomainsMany(_ context.Context, ids []string) (map[string][]authn.SSOProviderDomain, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.domainErr != nil {
		return nil, f.domainErr
	}
	f.domainReads++
	out := make(map[string][]authn.SSOProviderDomain, len(ids))
	for _, id := range ids {
		out[id] = append([]authn.SSOProviderDomain{}, f.claims[id]...)
	}
	return out, nil
}

func (f *fakeProviders) put(p authn.SSOProvider) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.byID[p.ID] = p
	f.order = append(f.order, p.ID)
}

func (f *fakeProviders) Get(_ context.Context, id string) (authn.SSOProvider, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.getErr != nil {
		return authn.SSOProvider{}, f.getErr
	}
	p, ok := f.byID[id]
	if !ok {
		return authn.SSOProvider{}, authn.ErrProviderNotFound
	}
	return p, nil
}

func (f *fakeProviders) Tenant(_ context.Context, id string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.byID[id]
	if !ok {
		return "", authn.ErrProviderNotFound
	}
	return p.Tenant, nil
}

func (f *fakeProviders) List(_ context.Context, tenant string) ([]authn.SSOProvider, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listCalls++
	var out []authn.SSOProvider
	for _, id := range f.order {
		if f.byID[id].Tenant == tenant {
			out = append(out, f.byID[id])
		}
	}
	return out, nil
}

func (f *fakeProviders) Put(_ context.Context, p authn.SSOProvider, _ *string) (authn.SSOProvider, error) {
	onCall := f.onPutCall
	if onCall != nil {
		onCall()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.putErr != nil {
		return authn.SSOProvider{}, f.putErr
	}
	if p.ID == "" {
		p.ID = idpA
	}
	p.Version++
	f.byID[p.ID] = p
	f.order = append(f.order, p.ID)
	f.puts = append(f.puts, p)
	return p, nil
}

func (f *fakeProviders) Delete(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.byID, id)
	f.deletes++
	return nil
}

func (f *fakeProviders) ClaimDomain(_ context.Context, _, domain string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.claimErr != nil {
		return "", f.claimErr
	}
	f.domains = append(f.domains, "claim:"+domain)
	return f.claimTok, nil
}

func (f *fakeProviders) VerifyDomain(_ context.Context, _, domain string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.domains = append(f.domains, "verify:"+domain)
	return nil
}

func (f *fakeProviders) RemoveDomain(_ context.Context, _, domain string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.domains = append(f.domains, "remove:"+domain)
	return nil
}

func (f *fakeProviders) ReverifyDomains(_ context.Context, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reverify++
	return nil
}

// fakeRefresher refreshes one protocol's metadata.
type fakeRefresher struct {
	protocol string
	calls    int
}

func (r *fakeRefresher) Protocol() string { return r.protocol }

func (r *fakeRefresher) Refresh(_ context.Context, id string) (authn.SSOProvider, error) {
	r.calls++
	return authn.SSOProvider{ID: id, Protocol: r.protocol, Tenant: tenantA, Version: 2}, nil
}

// fakeRecords is the impersonation capability read through the extended shape.
type fakeRecords struct {
	authn.Impersonation
	mu        sync.Mutex
	grants    map[string]authn.ImpersonationGrant
	order     []string
	revokes   int
	listCalls int
	getErr    error
}

func newRecords() *fakeRecords { return &fakeRecords{grants: map[string]authn.ImpersonationGrant{}} }

func (f *fakeRecords) put(g authn.ImpersonationGrant) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.grants[g.ID] = g
	f.order = append(f.order, g.ID)
}

func (f *fakeRecords) Get(_ context.Context, id string) (authn.ImpersonationGrant, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.getErr != nil {
		return authn.ImpersonationGrant{}, f.getErr
	}
	g, ok := f.grants[id]
	if !ok {
		return authn.ImpersonationGrant{}, authn.ErrNotImpersonating
	}
	return g, nil
}

func (f *fakeRecords) List(_ context.Context, _ authn.GrantQuery) ([]authn.ImpersonationGrant, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listCalls++
	out := make([]authn.ImpersonationGrant, 0, len(f.order))
	for _, id := range f.order {
		out = append(out, f.grants[id])
	}
	return out, "", nil
}

func (f *fakeRecords) Revoke(_ context.Context, _ string, _ authn.Identity, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.revokes++
	return nil
}

// fakeStore is the settings store.
type fakeStore struct {
	settings.Store
	mu     sync.Mutex
	values map[string]any
}

func newStore() *fakeStore { return &fakeStore{values: map[string]any{}} }

func (s *fakeStore) Get(_ settings.Layer, key string, target any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if v, ok := s.values[key]; ok {
		reflect.ValueOf(target).Elem().Set(reflect.ValueOf(v))
	}
	return nil
}

// fakeMemberRoles is the policy side's bulk role read.
type fakeMemberRoles struct {
	mu    sync.Mutex
	roles map[authn.AccountID][]string
	err   error
	calls int
}

func (f *fakeMemberRoles) MemberRoles(_ context.Context, _ string) (map[authn.AccountID][]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	return f.roles, f.err
}

func (f *fakeMemberRoles) seen() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// rig is the module with every dependency a case drives.
type rig struct {
	t            *testing.T
	p            *product
	mounter      *mounttest.Mounter
	accounts     *fakeAccounts
	verification *fakeSignupVerification
	accountAdm   *fakeAccountAdmin
	passwords    *fakePasswords
	members      *fakeMemberships
	roles        *fakeMemberRoles
	sessions     *fakeSessions
	mfa          *fakeMFA
	providers    *fakeProviders
	refresher    *fakeRefresher
	records      *fakeRecords
	audit        *recordingAudit
	decider      *fakeDecider
	store        *fakeStore
	logs         *logBuffer
}

// leaveOut names a capability a rig builds without.
type leaveOut string

const (
	noSessions        leaveOut = "sessions"
	noMFA             leaveOut = "mfa"
	noTokens          leaveOut = "tokens"
	noProviders       leaveOut = "providers"
	noProviderDomains leaveOut = "provider-domains"
	noRefresher       leaveOut = "refresher"
	noRecords         leaveOut = "records"
	noAudit           leaveOut = "audit"
	noDecider         leaveOut = "decider"
	noMemberRoles     leaveOut = "member-roles"
)

// newRig builds the module over a mounttest Mounter with every capability
// present. Each group the capabilities above leave out is not mounted.
func newRig(t *testing.T, omit ...leaveOut) *rig {
	t.Helper()
	left := map[leaveOut]bool{}
	for _, o := range omit {
		left[o] = true
	}
	r := &rig{
		t:            t,
		mounter:      mounttest.New(t),
		accounts:     newAccounts(),
		verification: &fakeSignupVerification{},
		accountAdm:   newAccountAdmin(),
		passwords:    &fakePasswords{},
		members:      newMemberships(),
		roles:        &fakeMemberRoles{},
		sessions:     newSessions(),
		mfa:          &fakeMFA{seasoned: true},
		providers:    newProviders(),
		refresher:    &fakeRefresher{protocol: authn.SSOProtocolSAML},
		records:      newRecords(),
		audit:        &recordingAudit{},
		decider:      &fakeDecider{allow: true},
		store:        newStore(),
		logs:         &logBuffer{},
	}
	r.accounts.put(adminID, authn.Account{Kind: authn.KindUser, Status: authn.StatusActive})
	r.accounts.put(userID, authn.Account{Kind: authn.KindUser, Status: authn.StatusActive})
	r.members.put(authn.Membership{Account: userID, Tenant: tenantA, Active: true, Source: authn.ReasonAdmin})

	logger := r.logs.logger()
	p := &product{
		mounter:       r.mounter,
		accounts:      r.accounts,
		accountAdm:    r.accountAdm,
		verification:  r.verification,
		passwords:     r.passwords,
		memberships:   r.members,
		audit:         r.audit,
		tenantAliases: identityAliases{},
		logger:        logger,
		settings:      newReader(r.store, logger),
		// The rig stands in for a deployment that offers the SSO management
		// surface: its subject is the groups and their capabilities, not the
		// configuration gate, which the module-level cases drive.
		ssoGroupEnabled: true,
	}
	if !left[noSessions] {
		p.sessions = r.sessions
	}
	if !left[noMFA] {
		p.mfa = r.mfa
	}
	if !left[noTokens] {
		p.tokens = &fakeTokens{}
	}
	if !left[noProviders] {
		p.ssoProviders = r.providers
	}
	if !left[noProviderDomains] {
		p.domainClaims = r.providers
	}
	if !left[noRefresher] {
		p.refreshers = []authn.SSOMetadataRefresher{r.refresher}
	}
	if !left[noRecords] {
		p.records = r.records
	}
	if !left[noDecider] {
		p.decider = r.decider
	}
	if !left[noMemberRoles] {
		p.roleReader = r.roles
	}
	r.p = p
	p.mountRoutes()
	return r
}

// fresh builds options for an administrator whose authentication is a fresh,
// local multi-factor one with a one-time code beside it.
func fresh(opts ...authntest.Option) []authntest.Option {
	return append([]authntest.Option{
		authntest.WithAccount(adminID),
		authntest.WithMethods(authn.MethodPassword, authn.MethodTOTP),
		authntest.WithAssurance(authn.AssuranceMulti),
		authntest.WithAuthTime(time.Now()),
	}, opts...)
}

// impersonating builds options for an administrator acting as the user inside
// tenant A, as a verifying backend would stamp it.
func impersonating() []authntest.Option {
	return []authntest.Option{
		authntest.WithAccount(userID),
		authntest.WithActor(adminID),
		authntest.WithSession(sessionA),
		authntest.WithScope(tenantA),
		authntest.WithAuthTime(time.Now()),
	}
}

// do sends a request through the Mounter with the identity the options build
// and a JSON body when one is given.
func (r *rig) do(method, path string, body any, opts ...authntest.Option) *httptest.ResponseRecorder {
	r.t.Helper()
	r.mounter.As(opts...)
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			r.t.Fatalf("encoding the body: %v", err)
		}
		reader = bytes.NewReader(encoded)
	}
	req := httptest.NewRequest(method, path, reader)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return r.mounter.Do(req)
}

// invoke calls a handler directly, with the identity the options build and the
// path parameters given, bypassing the gateway. It is how a case drives the
// handler's own impersonation check, which the Mounter's gateway would answer
// first. A nil body sends an empty JSON object, because the entry point binds
// the operation's declared body before the handler runs; an operation that
// declares none ignores it.
func (r *rig) invoke(handler gin.HandlerFunc, method, target string, params gin.Params, body any, opts ...authntest.Option) *httptest.ResponseRecorder {
	r.t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	if body == nil {
		body = map[string]any{}
	}
	encoded, _ := json.Marshal(body)
	reader := bytes.NewReader(encoded)
	req := httptest.NewRequest(method, target, reader)
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(authntest.Context(r.t, req.Context(), opts...))
	c.Request = req
	c.Params = params
	handler(c)
	return w
}

// fakeSignupVerification reports the self-registration state a case scripts and
// counts how often it was read. states overrides state for the accounts it
// names, so a case can tell an unproved operator from a proved target.
type fakeSignupVerification struct {
	mu     sync.Mutex
	state  authn.SignupStatus
	states map[authn.AccountID]authn.SignupStatus
	asked  []authn.AccountID
	err    error
	reads  int
}

// SignupStateOf answers the state scripted for the account, falling back to
// state, or the scripted failure.
func (v *fakeSignupVerification) SignupStateOf(_ context.Context, id authn.AccountID) (authn.SignupStatus, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.reads++
	v.asked = append(v.asked, id)
	if status, ok := v.states[id]; ok {
		return status, v.err
	}
	return v.state, v.err
}

// PendingAccountByContact is not part of the management surface.
func (v *fakeSignupVerification) PendingAccountByContact(context.Context, string, string) (string, bool, error) {
	return "", false, errors.New("admin: the pending-contact read is not part of this surface")
}

// readCount is how many state reads the fake answered.
func (v *fakeSignupVerification) readCount() int {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.reads
}

// identityAliases stands in for the roster's id-to-alias seam over an assembly
// whose fixture identifiers double as their aliases.
type identityAliases struct{}

func (identityAliases) TenantAliasOf(_ context.Context, id string) (string, error) {
	return id, nil
}
