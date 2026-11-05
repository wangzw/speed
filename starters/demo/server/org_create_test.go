package main

// The self-service organization creation cases: the decision order and the four
// upper bounds at the unit level, over stand-ins for the module's seams, and the
// route's coexistence with the personal-tenant route through the running
// program.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/http/openapigen"
	"github.com/vislake/speed/pkg/kv"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/settings"
	"github.com/vislake/speed/pkg/tenant"
)

// orgCreateFragmentPath is this module's fragment, relative to the package
// directory `go test` runs in. It is the file the module embeds.
const orgCreateFragmentPath = "org-create-openapi.json"

// orgCreateUnitAccount is the account the unit cases act as. It is not a UUID so
// a failure message stays readable.
const orgCreateUnitAccount = authn.AccountID("7c1e0f6a")

// orgCreateTestNow is the instant the unit cases run at: fifteen minutes into
// an hour, so Retry-After has a value the case can name.
var orgCreateTestNow = time.Date(2026, time.November, 5, 10, 15, 0, 0, time.UTC)

// orgCreateKV is the in-memory kv.KV the unit cases use. It honours the expiry
// rules the contract states, because the creation lock and the hourly window
// are both judged through them.
type orgCreateKV struct {
	mu     sync.Mutex
	values map[string]orgCreateKVEntry
}

type orgCreateKVEntry struct {
	value   []byte
	expires time.Time
}

func newOrgCreateKV() *orgCreateKV {
	return &orgCreateKV{values: map[string]orgCreateKVEntry{}}
}

func (k *orgCreateKV) live(key string) (orgCreateKVEntry, bool) {
	entry, ok := k.values[key]
	if !ok {
		return orgCreateKVEntry{}, false
	}
	if !entry.expires.IsZero() && !entry.expires.After(time.Now()) {
		delete(k.values, key)
		return orgCreateKVEntry{}, false
	}
	return entry, true
}

func (k *orgCreateKV) Get(_ context.Context, key string) ([]byte, bool, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	entry, ok := k.live(key)
	if !ok {
		return nil, false, nil
	}
	return append([]byte(nil), entry.value...), true, nil
}

func (k *orgCreateKV) Set(_ context.Context, key string, value []byte, ttl time.Duration) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	entry := orgCreateKVEntry{value: append([]byte(nil), value...)}
	if ttl > 0 {
		entry.expires = time.Now().Add(ttl)
	}
	k.values[key] = entry
	return nil
}

func (k *orgCreateKV) Delete(_ context.Context, key string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	delete(k.values, key)
	return nil
}

func (k *orgCreateKV) IncrByFloat(ctx context.Context, key string, delta float64) (float64, error) {
	return k.IncrByFloatWithTTL(ctx, key, delta, 0)
}

func (k *orgCreateKV) IncrByFloatWithTTL(_ context.Context, key string, delta float64, ttl time.Duration) (float64, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	entry, ok := k.live(key)
	current := 0.0
	if ok {
		parsed, err := strconv.ParseFloat(string(entry.value), 64)
		if err != nil {
			return 0, kv.ErrNotNumeric
		}
		current = parsed
	}
	current += delta
	entry.value = []byte(strconv.FormatFloat(current, 'f', -1, 64))
	if !ok && ttl > 0 {
		entry.expires = time.Now().Add(ttl)
	}
	k.values[key] = entry
	return current, nil
}

func (k *orgCreateKV) CompareAndSwap(_ context.Context, key string, old, newVal []byte) (bool, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	entry, ok := k.live(key)
	if !ok {
		entry = orgCreateKVEntry{}
	}
	if string(entry.value) != string(old) {
		return false, nil
	}
	entry.value = append([]byte(nil), newVal...)
	k.values[key] = entry
	return true, nil
}

func (k *orgCreateKV) SetIfAbsent(_ context.Context, key string, value []byte, ttl time.Duration) (bool, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if _, live := k.live(key); live {
		return false, nil
	}
	entry := orgCreateKVEntry{value: append([]byte(nil), value...)}
	if ttl > 0 {
		entry.expires = time.Now().Add(ttl)
	}
	k.values[key] = entry
	return true, nil
}

// orgCreateSettings is the settings.Store the unit cases read. Only Get is
// reached: the module declares its two items and never writes one.
type orgCreateSettings struct {
	settings.Store

	values map[string]int
	err    error
}

func (s *orgCreateSettings) Get(_ settings.Layer, key string, target any) error {
	if s.err != nil {
		return s.err
	}
	value, ok := s.values[key]
	if !ok {
		return fmt.Errorf("no value for %q", key)
	}
	pointer, ok := target.(*int)
	if !ok {
		return fmt.Errorf("target for %q is %T, want *int", key, target)
	}
	*pointer = value
	return nil
}

// orgCreateVerification is the signup read the unit cases use.
type orgCreateVerification struct {
	authn.SignupVerification

	status authn.SignupStatus
	err    error
	calls  int
}

func (v *orgCreateVerification) SignupStateOf(_ context.Context, _ authn.AccountID) (authn.SignupStatus, error) {
	v.calls++
	return v.status, v.err
}

// orgCreateProvisionCall is one Provision request the stand-in saw.
type orgCreateProvisionCall struct {
	tenant  tenant.Tenant
	creator string
}

// orgCreateProvisioner is the tenant.Provisioner the unit cases use.
type orgCreateProvisioner struct {
	tenant.Provisioner

	mu    sync.Mutex
	calls []orgCreateProvisionCall
	err   error
}

func (p *orgCreateProvisioner) Provision(_ context.Context, t tenant.Tenant, creator string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls = append(p.calls, orgCreateProvisionCall{tenant: t, creator: creator})
	return p.err
}

func (p *orgCreateProvisioner) recorded() []orgCreateProvisionCall {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]orgCreateProvisionCall(nil), p.calls...)
}

// ByAlias is the roster lookup the module's identifier pre-check asks for. The
// shared fakeRoster carries it here because no earlier case reached it.
func (r *fakeRoster) ByAlias(_ context.Context, alias string) (tenant.Tenant, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, row := range r.rows {
		if row.Alias == alias {
			return row, true, nil
		}
	}
	return tenant.Tenant{}, false, nil
}

// orgCreateRig is the product plus the stand-ins a case wants to steer.
type orgCreateRig struct {
	product      *orgCreateProduct
	roster       *fakeRoster
	memberships  *fakeMemberships
	kv           *orgCreateKV
	verification *orgCreateVerification
	settings     *orgCreateSettings
	provisioner  *orgCreateProvisioner
	audit        *recordedAudit
}

func newOrgCreateRig() *orgCreateRig {
	rig := &orgCreateRig{
		roster:       newFakeRoster(),
		memberships:  newFakeMemberships(),
		kv:           newOrgCreateKV(),
		verification: &orgCreateVerification{status: authn.SignupStatus{State: authn.SignupVerified}},
		settings: &orgCreateSettings{values: map[string]int{
			orgCreateMaxOrganizationsPath: 5,
			orgCreateMaxPerHourPath:       3,
		}},
		provisioner: &orgCreateProvisioner{},
		audit:       newRecordedAudit(),
	}
	rig.product = &orgCreateProduct{
		config:       orgCreateConfig{Enabled: true},
		roster:       rig.roster,
		memberships:  rig.memberships,
		verification: rig.verification,
		settings:     rig.settings,
		kv:           rig.kv,
		provisioner:  rig.provisioner,
		audit:        rig.audit,
		logger:       log.Default(),
		now:          func() time.Time { return orgCreateTestNow },
		newIdentifier: func() string {
			return orgCreateIDPrefix + uuid.NewString()
		},
	}
	return rig
}

func (r *orgCreateRig) ctx(t *testing.T, options ...authntest.Option) context.Context {
	t.Helper()
	if len(options) == 0 {
		options = []authntest.Option{authntest.WithAccount(orgCreateUnitAccount)}
	}
	return authntest.Context(t, context.Background(), options...)
}

func (r *orgCreateRig) create(t *testing.T, ctx context.Context, displayName string) (OrganizationCreatedReply, error) {
	t.Helper()
	return r.product.handleCreate(ctx, createOrganizationInput{
		DisplayName: displayName,
		Description: "a description",
	})
}

// attempts reads the number of creations counted in the account's current
// hourly window.
func (r *orgCreateRig) attempts(t *testing.T) int {
	t.Helper()
	count, err := r.product.hourlyCount(context.Background(), orgCreateUnitAccount)
	if err != nil {
		t.Fatalf("reading the hourly count: %v", err)
	}
	return count
}

func orgCreateFault(t *testing.T, err error) speedhttpFault {
	t.Helper()
	var fault speedhttpFault
	if !errors.As(err, &fault) {
		t.Fatalf("the refusal is %v, want a fault", err)
	}
	return fault
}

// speedhttpFault is the value type the module answers with, named here so the
// cases do not repeat the package's long name at every call.
type speedhttpFault = speedhttp.Fault

// TestOrgCreateRequiresAnIdentity holds the first step: a context with no
// identity is refused 401 and nothing is read or written.
func TestOrgCreateRequiresAnIdentity(t *testing.T) {
	rig := newOrgCreateRig()
	_, err := rig.create(t, context.Background(), "Acme")
	if fault := orgCreateFault(t, err); fault.Status != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", fault.Status)
	}
	if len(rig.provisioner.recorded()) != 0 || rig.verification.calls != 0 {
		t.Error("an anonymous request reached the verification read or the provisioner")
	}
}

// TestOrgCreateRefusesImpersonation holds the second step: an identity acting
// for another account is refused 403 before the verification read, even though
// the route is mounted so the gateway lets it through.
func TestOrgCreateRefusesImpersonation(t *testing.T) {
	rig := newOrgCreateRig()
	ctx := rig.ctx(t,
		authntest.WithAccount("target-account"),
		authntest.WithActor("operator-account"),
		authntest.WithSession("session-1"),
		authntest.WithScope("acme"),
	)
	_, err := rig.create(t, ctx, "Acme")
	if fault := orgCreateFault(t, err); fault.Status != http.StatusForbidden {
		t.Errorf("status = %d, want 403", fault.Status)
	}
	if rig.verification.calls != 0 || len(rig.provisioner.recorded()) != 0 {
		t.Error("an impersonating request reached the verification read or the provisioner")
	}
}

// TestOrgCreateRefusesServiceAccountsAndStaticSubjects holds the third step.
func TestOrgCreateRefusesServiceAccountsAndStaticSubjects(t *testing.T) {
	for name, options := range map[string][]authntest.Option{
		"a service account": {authntest.WithAccount("api-client"), authntest.WithMethods(authn.MethodClientSecret)},
		"a static subject": {
			authntest.WithAccount("static.ops"),
			authntest.WithBackend("static"),
			authntest.WithMethods(authn.MethodStatic),
		},
	} {
		t.Run(name, func(t *testing.T) {
			rig := newOrgCreateRig()
			_, err := rig.create(t, rig.ctx(t, options...), "Acme")
			if fault := orgCreateFault(t, err); fault.Status != http.StatusForbidden {
				t.Errorf("status = %d, want 403", fault.Status)
			}
			if rig.verification.calls != 0 || len(rig.provisioner.recorded()) != 0 {
				t.Error("the refused caller reached the verification read or the provisioner")
			}
		})
	}
}

// TestOrgCreateRequiresAProvedEmailAddress holds the fourth step: a pending
// address is the authentication capability's contact-required refusal, a read
// failure is a 503, and an account with no address to prove is admitted.
func TestOrgCreateRequiresAProvedEmailAddress(t *testing.T) {
	cases := map[string]struct {
		status authn.SignupStatus
		err    error
		want   int
		typ    string
	}{
		"pending":  {status: authn.SignupStatus{State: authn.SignupPending}, want: http.StatusForbidden, typ: authn.ContactRequiredProblemType},
		"verified": {status: authn.SignupStatus{State: authn.SignupVerified}, want: http.StatusCreated},
		"none":     {status: authn.SignupStatus{State: authn.SignupNone}, want: http.StatusCreated},
		"read failure": {
			status: authn.SignupStatus{State: authn.SignupNone},
			err:    errors.New("the account store is unavailable"),
			want:   http.StatusServiceUnavailable,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			rig := newOrgCreateRig()
			rig.verification.status = tc.status
			rig.verification.err = tc.err
			_, err := rig.create(t, rig.ctx(t), "Acme")
			if tc.want == http.StatusCreated {
				if err != nil {
					t.Fatalf("the creation failed: %v", err)
				}
				return
			}
			fault := orgCreateFault(t, err)
			if fault.Status != tc.want {
				t.Errorf("status = %d, want %d", fault.Status, tc.want)
			}
			if fault.Type != tc.typ {
				t.Errorf("type = %q, want %q", fault.Type, tc.typ)
			}
			if len(rig.provisioner.recorded()) != 0 {
				t.Error("the refused request reached the provisioner")
			}
		})
	}
}

// TestOrgCreateChecksTheEmailBeforeTheShape holds the order: a pending address
// is refused even when the body is also invalid.
func TestOrgCreateChecksTheEmailBeforeTheShape(t *testing.T) {
	rig := newOrgCreateRig()
	rig.verification.status = authn.SignupStatus{State: authn.SignupPending}
	_, err := rig.product.handleCreate(rig.ctx(t), createOrganizationInput{})
	fault := orgCreateFault(t, err)
	if fault.Status != http.StatusForbidden || fault.Type != authn.ContactRequiredProblemType {
		t.Errorf("status/type = %d/%q, want 403/%q", fault.Status, fault.Type, authn.ContactRequiredProblemType)
	}
}

// TestOrgCreateValidatesTheProfileShape holds the fifth step: the display name
// is required and both fields are held to their bounds.
func TestOrgCreateValidatesTheProfileShape(t *testing.T) {
	cases := map[string]createOrganizationInput{
		"a blank display name": {DisplayName: "   "},
		"a display name too long": {
			DisplayName: strings.Repeat("a", tenant.DisplayNameMaxRunes+1),
		},
		"a description too long": {
			DisplayName: "Acme",
			Description: strings.Repeat("a", tenant.DescriptionMaxRunes+1),
		},
	}
	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			rig := newOrgCreateRig()
			_, err := rig.product.handleCreate(rig.ctx(t), input)
			if fault := orgCreateFault(t, err); fault.Status != http.StatusBadRequest {
				t.Errorf("status = %d, want 400", fault.Status)
			}
			if len(rig.provisioner.recorded()) != 0 {
				t.Error("an invalid body reached the provisioner")
			}
		})
	}
}

// TestOrgCreateCountsOnlyActiveBusinessMemberships holds the sixth step: a
// personal tenant, a disabled tenant and a suspended membership are not
// counted, and the cap refuses the creation once the active business count
// reaches it.
func TestOrgCreateCountsOnlyActiveBusinessMemberships(t *testing.T) {
	rig := newOrgCreateRig()
	rig.settings.values[orgCreateMaxOrganizationsPath] = 4
	addBusiness := func(name string, status tenant.Status) {
		rig.roster.rows[tenant.ID(name)] = tenant.Tenant{ID: tenant.ID(name), Alias: name, Status: status}
		rig.memberships.rows[orgCreateUnitAccount] = append(rig.memberships.rows[orgCreateUnitAccount],
			authn.Membership{Account: orgCreateUnitAccount, Tenant: name, Active: true, Source: "admin"})
	}
	for i := range 3 {
		addBusiness(fmt.Sprintf("biz-%d", i), tenant.StatusActive)
	}
	addBusiness("biz-disabled", tenant.StatusDisabled)
	personal := string(personalTenantID(orgCreateUnitAccount))
	rig.roster.rows[tenant.ID(personal)] = tenant.Tenant{ID: tenant.ID(personal), Alias: personal, Status: tenant.StatusActive}
	rig.memberships.rows[orgCreateUnitAccount] = append(rig.memberships.rows[orgCreateUnitAccount],
		authn.Membership{Account: orgCreateUnitAccount, Tenant: personal, Active: true, Source: "personal"})
	rig.memberships.rows[orgCreateUnitAccount] = append(rig.memberships.rows[orgCreateUnitAccount],
		authn.Membership{Account: orgCreateUnitAccount, Tenant: "biz-suspended", Active: false, Source: "admin"})

	if _, err := rig.create(t, rig.ctx(t), "Acme"); err != nil {
		t.Fatalf("the creation below the cap failed: %v", err)
	}

	// One more active business tenant reaches the cap. The fake provisioner
	// does not write the new membership, so the case adds it itself.
	addBusiness("biz-3", tenant.StatusActive)
	_, err := rig.create(t, rig.ctx(t), "Acme Two")
	fault := orgCreateFault(t, err)
	if fault.Status != http.StatusConflict || fault.Type != orgCreateProblemPrefix+orgOrganizationLimitCode {
		t.Errorf("status/type = %d/%q, want 409/%q", fault.Status, fault.Type,
			orgCreateProblemPrefix+orgOrganizationLimitCode)
	}
	if len(rig.provisioner.recorded()) != 1 {
		t.Errorf("the provisioner was asked %d time(s), want once", len(rig.provisioner.recorded()))
	}
}

// TestOrgCreateLimitsTheHourlyCount holds the seventh step: three creations in
// an hour are admitted, the fourth is the rate refusal with a Retry-After, and
// the next hour starts a new window.
func TestOrgCreateLimitsTheHourlyCount(t *testing.T) {
	rig := newOrgCreateRig()
	for i := range 3 {
		if _, err := rig.create(t, rig.ctx(t), fmt.Sprintf("Acme %d", i)); err != nil {
			t.Fatalf("creation %d below the hourly limit failed: %v", i, err)
		}
	}
	_, err := rig.create(t, rig.ctx(t), "Acme Four")
	fault := orgCreateFault(t, err)
	if fault.Status != http.StatusTooManyRequests || fault.Type != orgCreateProblemPrefix+orgRateLimitedCode {
		t.Fatalf("status/type = %d/%q, want 429/%q", fault.Status, fault.Type,
			orgCreateProblemPrefix+orgRateLimitedCode)
	}
	if len(fault.Headers) != 1 || fault.Headers[0].Name != "Retry-After" {
		t.Fatalf("headers = %+v, want one Retry-After", fault.Headers)
	}
	// The test instant is 10:15, so the window has 2700 seconds left.
	if got := fault.Headers[0].Value; got != "2700" {
		t.Errorf("Retry-After = %q, want %q", got, "2700")
	}
	if len(rig.provisioner.recorded()) != 3 {
		t.Errorf("the provisioner was asked %d time(s), want three", len(rig.provisioner.recorded()))
	}

	rig.product.now = func() time.Time { return orgCreateTestNow.Add(time.Hour) }
	if _, err := rig.create(t, rig.ctx(t), "Acme Next Hour"); err != nil {
		t.Errorf("the next hour's creation failed: %v", err)
	}
}

// TestOrgCreateDoesNotCountRefusalsBeforeTheCount holds the first half of the
// counting rule: a request refused at a pre-check consumes no place. The shape
// refusal and the organization cap stand for every refusal in steps 1-8, all of
// which return before recordAttempt.
func TestOrgCreateDoesNotCountRefusalsBeforeTheCount(t *testing.T) {
	rig := newOrgCreateRig()
	_, err := rig.product.handleCreate(rig.ctx(t), createOrganizationInput{DisplayName: ""})
	if fault := orgCreateFault(t, err); fault.Status != http.StatusBadRequest {
		t.Fatalf("the shape refusal's status = %d, want 400", fault.Status)
	}
	if got := rig.attempts(t); got != 0 {
		t.Fatalf("a shape refusal counted %d attempt(s), want none", got)
	}

	rig.settings.values[orgCreateMaxOrganizationsPath] = 1
	rig.roster.rows["biz"] = tenant.Tenant{ID: "biz", Alias: "biz", Status: tenant.StatusActive}
	rig.memberships.rows[orgCreateUnitAccount] = []authn.Membership{
		{Account: orgCreateUnitAccount, Tenant: "biz", Active: true, Source: "admin"},
	}
	_, err = rig.create(t, rig.ctx(t), "Acme")
	if fault := orgCreateFault(t, err); fault.Status != http.StatusConflict {
		t.Fatalf("the cap refusal's status = %d, want 409", fault.Status)
	}
	if got := rig.attempts(t); got != 0 {
		t.Errorf("a cap refusal counted %d attempt(s), want none", got)
	}
}

// TestOrgCreateCountsAProvisionerFailure holds the second half of the counting
// rule: once the attempt is counted, a provisioner failure consumes it. With a
// budget of three, one failed provision leaves room for two creations.
func TestOrgCreateCountsAProvisionerFailure(t *testing.T) {
	rig := newOrgCreateRig()
	rig.provisioner.err = errors.New("the roster is unavailable")
	if _, err := rig.create(t, rig.ctx(t), "Acme"); err == nil {
		t.Fatal("the failed provision returned no error")
	}
	if got := rig.attempts(t); got != 1 {
		t.Fatalf("a failed provision counted %d attempt(s), want one", got)
	}

	rig.provisioner.err = nil
	for i := range 2 {
		if _, err := rig.create(t, rig.ctx(t), fmt.Sprintf("Acme %d", i)); err != nil {
			t.Fatalf("creation %d after the failed provision failed: %v", i, err)
		}
	}
	_, err := rig.create(t, rig.ctx(t), "Acme Fourth")
	if fault := orgCreateFault(t, err); fault.Status != http.StatusTooManyRequests {
		t.Errorf("the fourth request's status = %d, want 429", fault.Status)
	}
}

// TestOrgCreateCountsAnAuditFailure holds the order the counting rule depends
// on: the attempt is counted before the mandatory audit, so an audit failure
// consumes it. With a budget of three, one failed audit leaves room for two
// creations.
func TestOrgCreateCountsAnAuditFailure(t *testing.T) {
	rig := newOrgCreateRig()
	rig.audit.err = errors.New("the audit store is down")
	_, err := rig.create(t, rig.ctx(t), "Acme")
	if fault := orgCreateFault(t, err); fault.Status != http.StatusServiceUnavailable {
		t.Fatalf("the audit refusal's status = %d, want 503", fault.Status)
	}
	if got := rig.attempts(t); got != 1 {
		t.Fatalf("a failed audit counted %d attempt(s), want one", got)
	}

	rig.audit.err = nil
	for i := range 2 {
		if _, err = rig.create(t, rig.ctx(t), fmt.Sprintf("Acme %d", i)); err != nil {
			t.Fatalf("creation %d after the failed audit failed: %v", i, err)
		}
	}
	_, err = rig.create(t, rig.ctx(t), "Acme Fourth")
	if fault := orgCreateFault(t, err); fault.Status != http.StatusTooManyRequests {
		t.Errorf("the fourth request's status = %d, want 429", fault.Status)
	}
}

// TestOrgCreateHoldsThePerAccountLock holds the creation lock: a live lock is
// the 409 creation-in-progress refusal, and a completed request leaves the lock
// free again.
func TestOrgCreateHoldsThePerAccountLock(t *testing.T) {
	rig := newOrgCreateRig()
	key := orgCreateLockKey(orgCreateUnitAccount)
	if err := rig.kv.Set(context.Background(), key, []byte("held"), orgCreateLockTTL); err != nil {
		t.Fatalf("taking the stand-in lock: %v", err)
	}
	_, err := rig.create(t, rig.ctx(t), "Acme")
	fault := orgCreateFault(t, err)
	if fault.Status != http.StatusConflict || fault.Type != orgCreateProblemPrefix+orgCreationInProgressCode {
		t.Errorf("status/type = %d/%q, want 409/%q", fault.Status, fault.Type,
			orgCreateProblemPrefix+orgCreationInProgressCode)
	}
	if len(rig.provisioner.recorded()) != 0 {
		t.Error("a locked request reached the provisioner")
	}

	if err := rig.kv.Delete(context.Background(), key); err != nil {
		t.Fatalf("releasing the stand-in lock: %v", err)
	}
	if _, err := rig.create(t, rig.ctx(t), "Acme"); err != nil {
		t.Fatalf("the creation after the lock was released failed: %v", err)
	}
	if _, held, err := rig.kv.Get(context.Background(), key); err != nil {
		t.Fatalf("reading the lock back: %v", err)
	} else if held {
		t.Error("the completed request left the creation lock behind")
	}
}

// TestOrgCreateWritesTheMandatoryAuditRow holds the audit step: the mandatory
// row lands before the provisioner, and a cross-replica identifier race is a
// 409 that does not add a second row for the same request.
func TestOrgCreateWritesTheMandatoryAuditRow(t *testing.T) {
	rig := newOrgCreateRig()
	reply, err := rig.create(t, rig.ctx(t), "Acme")
	if err != nil {
		t.Fatalf("the creation failed: %v", err)
	}
	rows := rig.audit.rows()
	if len(rows) != 1 {
		t.Fatalf("audit rows = %+v, want exactly one", rows)
	}
	row := rows[0]
	if row.Action != orgCreateAuditAction || row.Outcome != "success" {
		t.Errorf("audit row = %+v, want action %q and outcome success", row, orgCreateAuditAction)
	}
	if row.Object != reply.Tenant.ID {
		t.Errorf("audit object = %q, want the new tenant %q", row.Object, reply.Tenant.ID)
	}
	if row.Detail["account"] != string(orgCreateUnitAccount) || row.Detail["tenant"] != reply.Tenant.ID {
		t.Errorf("audit detail = %+v, want the account and the tenant", row.Detail)
	}
	if row.Detail["action_id"] == "" {
		t.Error("the audit detail carries no action_id")
	}

	rig.provisioner.err = fmt.Errorf("%w: identifier taken", tenant.ErrTenantExists)
	_, err = rig.create(t, rig.ctx(t), "Acme Two")
	fault := orgCreateFault(t, err)
	if fault.Status != http.StatusConflict || fault.Type != orgCreateProblemPrefix+orgIdentifierUnavailableCode {
		t.Errorf("status/type = %d/%q, want 409/%q", fault.Status, fault.Type,
			orgCreateProblemPrefix+orgIdentifierUnavailableCode)
	}
	if rows := rig.audit.rows(); len(rows) != 2 {
		t.Errorf("audit rows after the race = %d, want the first request's one and the second's one", len(rows))
	}
}

// TestOrgCreateClosedAFailedProvisionWithAFailedRow holds the ordinary failure:
// the mandatory row and a linked failed row, and a 503.
func TestOrgCreateClosedAFailedProvisionWithAFailedRow(t *testing.T) {
	rig := newOrgCreateRig()
	rig.provisioner.err = errors.New("the roster is unavailable")
	_, err := rig.create(t, rig.ctx(t), "Acme")
	if fault := orgCreateFault(t, err); fault.Status != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", fault.Status)
	}
	rows := rig.audit.rows()
	if len(rows) != 2 || rows[0].Outcome != "success" || rows[1].Outcome != "failed" {
		t.Fatalf("audit rows = %+v, want the mandatory row and a linked failed row", rows)
	}
	if rows[0].Detail["action_id"] != rows[1].Detail["action_id"] {
		t.Error("the two rows carry different action identifiers")
	}
}

// TestOrgCreateGeneratesTheIdentifierAndAlias holds the identifier rule: one
// org-<uuid> string, the same value in both fields, with the caller's profile
// and the active state.
func TestOrgCreateGeneratesTheIdentifierAndAlias(t *testing.T) {
	rig := newOrgCreateRig()
	reply, err := rig.create(t, rig.ctx(t), "  Acme  ")
	if err != nil {
		t.Fatalf("the creation failed: %v", err)
	}
	if reply.Tenant.ID != reply.Tenant.Alias {
		t.Errorf("id/alias = %q/%q, want the same value", reply.Tenant.ID, reply.Tenant.Alias)
	}
	if !strings.HasPrefix(reply.Tenant.ID, orgCreateIDPrefix) {
		t.Errorf("id = %q, want the %q prefix", reply.Tenant.ID, orgCreateIDPrefix)
	}
	if _, err := uuid.Parse(strings.TrimPrefix(reply.Tenant.ID, orgCreateIDPrefix)); err != nil {
		t.Errorf("the identifier's tail is not a UUID: %v", err)
	}
	if reply.Tenant.DisplayName != "Acme" {
		t.Errorf("displayName = %q, want the trimmed value", reply.Tenant.DisplayName)
	}
	if reply.Tenant.Status != tenant.StatusActive.String() {
		t.Errorf("status = %q, want active", reply.Tenant.Status)
	}
	calls := rig.provisioner.recorded()
	if len(calls) != 1 {
		t.Fatalf("the provisioner was asked %d time(s), want once", len(calls))
	}
	got := calls[0].tenant
	if string(got.ID) != reply.Tenant.ID || got.Alias != reply.Tenant.Alias {
		t.Errorf("the provisioned tenant is %q/%q, want the answered %q/%q",
			got.ID, got.Alias, reply.Tenant.ID, reply.Tenant.Alias)
	}
	if got.Profile.DisplayName != "Acme" || got.Status != tenant.StatusActive {
		t.Errorf("the provisioned tenant carries %+v, want the profile and the active state", got)
	}
	if calls[0].creator != string(orgCreateUnitAccount) {
		t.Errorf("creator = %q, want the acting account %q", calls[0].creator, orgCreateUnitAccount)
	}
}

// TestOrgCreateRetriesAConflictingIdentifier holds the pre-check: a collision
// is regenerated, three retries are allowed, and four collisions are the 409
// identifier-unavailable refusal with no provision.
func TestOrgCreateRetriesAConflictingIdentifier(t *testing.T) {
	rig := newOrgCreateRig()
	for _, taken := range []string{"org-taken-1", "org-taken-2", "org-taken-3"} {
		rig.roster.rows[tenant.ID(taken)] = tenant.Tenant{ID: tenant.ID(taken), Alias: taken, Status: tenant.StatusActive}
	}
	sequence := []string{"org-taken-1", "org-taken-2", "org-taken-3", "org-free"}
	next := 0
	rig.product.newIdentifier = func() string {
		out := sequence[next]
		next++
		return out
	}
	reply, err := rig.create(t, rig.ctx(t), "Acme")
	if err != nil {
		t.Fatalf("the creation after three retries failed: %v", err)
	}
	if reply.Tenant.ID != "org-free" || reply.Tenant.Alias != "org-free" {
		t.Errorf("the answer names %q/%q, want the free identifier", reply.Tenant.ID, reply.Tenant.Alias)
	}

	rig = newOrgCreateRig()
	for i := range orgCreateIdentifierRetries + 1 {
		taken := fmt.Sprintf("org-taken-%d", i)
		rig.roster.rows[tenant.ID(taken)] = tenant.Tenant{ID: tenant.ID(taken), Alias: taken, Status: tenant.StatusActive}
	}
	next = 0
	rig.product.newIdentifier = func() string {
		out := fmt.Sprintf("org-taken-%d", next)
		next++
		return out
	}
	_, err = rig.create(t, rig.ctx(t), "Acme")
	fault := orgCreateFault(t, err)
	if fault.Status != http.StatusConflict || fault.Type != orgCreateProblemPrefix+orgIdentifierUnavailableCode {
		t.Errorf("status/type = %d/%q, want 409/%q", fault.Status, fault.Type,
			orgCreateProblemPrefix+orgIdentifierUnavailableCode)
	}
	if len(rig.provisioner.recorded()) != 0 {
		t.Error("the exhausted pre-check reached the provisioner")
	}
}

// TestOrgCreateReadsTheLimitsAtRequestTime holds the settings seam: the two
// items come from the global layer of the runtime settings, and a read failure
// is a 503 that creates nothing.
func TestOrgCreateReadsTheLimitsAtRequestTime(t *testing.T) {
	rig := newOrgCreateRig()
	rig.settings.values[orgCreateMaxOrganizationsPath] = 1
	addBusiness := func(name string) {
		rig.roster.rows[tenant.ID(name)] = tenant.Tenant{ID: tenant.ID(name), Alias: name, Status: tenant.StatusActive}
		rig.memberships.rows[orgCreateUnitAccount] = append(rig.memberships.rows[orgCreateUnitAccount],
			authn.Membership{Account: orgCreateUnitAccount, Tenant: name, Active: true, Source: "admin"})
	}
	addBusiness("biz-0")
	_, err := rig.create(t, rig.ctx(t), "Acme")
	if fault := orgCreateFault(t, err); fault.Status != http.StatusConflict {
		t.Errorf("status with a one-organization cap = %d, want 409", fault.Status)
	}

	rig = newOrgCreateRig()
	rig.settings.err = errors.New("the settings store is unavailable")
	_, err = rig.create(t, rig.ctx(t), "Acme")
	if fault := orgCreateFault(t, err); fault.Status != http.StatusServiceUnavailable {
		t.Errorf("status when the settings read fails = %d, want 503", fault.Status)
	}
}

// TestOrgCreateRejectsAnUnusableLimit holds the fail-closed reading of a value
// the declaration's domain does not admit.
func TestOrgCreateRejectsAnUnusableLimit(t *testing.T) {
	rig := newOrgCreateRig()
	rig.settings.values[orgCreateMaxPerHourPath] = 0
	_, err := rig.create(t, rig.ctx(t), "Acme")
	if fault := orgCreateFault(t, err); fault.Status != http.StatusServiceUnavailable {
		t.Errorf("status with a zero hourly limit = %d, want 503", fault.Status)
	}
}

// TestOrgCreateFragmentIsGeneratedFromTheDeclarations holds the committed
// fragment to the operation declarations, the way the personal fragment's own
// case does.
func TestOrgCreateFragmentIsGeneratedFromTheDeclarations(t *testing.T) {
	generated, err := openapigen.Generate(orgCreateFragmentTitle, orgCreateFragmentVersion, orgCreateOperations)
	if err != nil {
		t.Fatalf("generating the fragment: %v", err)
	}
	if os.Getenv("UPDATE_FRAGMENTS") == "1" {
		if writeErr := os.WriteFile(orgCreateFragmentPath, generated, 0o644); writeErr != nil {
			t.Fatalf("writing %s: %v", orgCreateFragmentPath, writeErr)
		}
	}
	committed, err := os.ReadFile(orgCreateFragmentPath)
	if err != nil {
		t.Fatalf("reading %s: %v", orgCreateFragmentPath, err)
	}
	if !bytes.Equal(generated, committed) {
		t.Errorf("the committed fragment is out of step with the declarations; regenerate it with " +
			"UPDATE_FRAGMENTS=1 go -C starters/demo/server test . -run TestOrgCreateFragmentIsGeneratedFromTheDeclarations")
	}
}

// TestTheOrganizationCreateRouteCoexistsWithThePersonalRoute runs the program:
// both routes answer for an authenticated caller, three creations are admitted
// and the fourth is the hourly refusal, and the feature document carries
// org.create.
func TestTheOrganizationCreateRouteCoexistsWithThePersonalRoute(t *testing.T) {
	base, token := orgCreateServe(t, "")
	client := &http.Client{Timeout: 10 * time.Second}
	headers := map[string]string{"Authorization": "Bearer " + token}

	for i := range 3 {
		res := send(t, client, http.MethodPost, base+"/api/v1/orgs",
			map[string]string{"display_name": fmt.Sprintf("Acme %d", i)}, headers)
		if res.status != http.StatusCreated {
			t.Fatalf("creation %d answered %d, want 201:\n%s", i, res.status, res.body)
		}
	}
	limited := send(t, client, http.MethodPost, base+"/api/v1/orgs",
		map[string]string{"display_name": "Acme Four"}, headers)
	if limited.status != http.StatusTooManyRequests {
		t.Fatalf("the fourth creation answered %d, want 429:\n%s", limited.status, limited.body)
	}
	if !strings.Contains(string(limited.body), orgRateLimitedCode) {
		t.Errorf("the rate refusal does not carry %q:\n%s", orgRateLimitedCode, limited.body)
	}

	personal := send(t, client, http.MethodPost, base+"/api/v1/orgs/personal", nil, headers)
	if personal.status != http.StatusOK {
		t.Errorf("POST /api/v1/orgs/personal answered %d, want 200:\n%s", personal.status, personal.body)
	}

	features := authenticatedGet(t, base+"/api/v1/features", token)
	var document struct {
		Features []string `json:"features"`
	}
	if err := json.Unmarshal(features.body, &document); err != nil {
		t.Fatalf("the feature document is not JSON: %v\n%s", err, features.body)
	}
	if !slices.Contains(document.Features, orgCreateFeature) {
		t.Errorf("the feature document does not carry %q: %v", orgCreateFeature, document.Features)
	}
}

// TestTheOrganizationCreateRouteIsAbsentWhenTheSwitchIsOff runs the program
// with org.create.enabled false: the route is a 404 that the personal prefix
// does not take over, the personal route still answers, and the feature is
// absent.
func TestTheOrganizationCreateRouteIsAbsentWhenTheSwitchIsOff(t *testing.T) {
	base, token := orgCreateServe(t, "org:\n  create:\n    enabled: false\n")
	client := &http.Client{Timeout: 10 * time.Second}
	headers := map[string]string{"Authorization": "Bearer " + token}

	absent := send(t, client, http.MethodPost, base+"/api/v1/orgs",
		map[string]string{"display_name": "Acme"}, headers)
	if absent.status != http.StatusNotFound {
		t.Fatalf("POST /api/v1/orgs with the switch off answered %d, want 404:\n%s", absent.status, absent.body)
	}

	personal := send(t, client, http.MethodPost, base+"/api/v1/orgs/personal", nil, headers)
	if personal.status != http.StatusOK {
		t.Errorf("POST /api/v1/orgs/personal answered %d, want 200:\n%s", personal.status, personal.body)
	}

	features := authenticatedGet(t, base+"/api/v1/features", token)
	var document struct {
		Features []string `json:"features"`
	}
	if err := json.Unmarshal(features.body, &document); err != nil {
		t.Fatalf("the feature document is not JSON: %v\n%s", err, features.body)
	}
	for _, name := range document.Features {
		if name == orgCreateFeature {
			t.Errorf("the feature document carries %q while the switch is off", name)
		}
	}
}

// orgCreateServe starts a seeded serve with the caller's extra configuration
// and answers the backend base URL and a token for the account with no
// membership and no email address (the configuration's second seed account).
func orgCreateServe(t *testing.T, extra string) (string, string) {
	t.Helper()
	ports := freePorts(t, listenerCount)
	dsn := filepath.Join(t.TempDir(), "demo.db")
	body := configBody(t, ports, dsn) + extra
	config := writeConfigFile(t, body)
	if res := runCommand(t, config, "sqlite-memory", seedCommand); res.err != nil {
		t.Fatalf("seed failed: %v\n%s", res.err, res.combined())
	}
	serve := start(t, config, "sqlite-memory", serveCommand)
	serve.waitReady(t)
	base := fmt.Sprintf("http://127.0.0.1:%d", ports[backendListener])
	return base, signIn(t, base, hashIdentifier, seedPassword)
}

// Keep the compile-time surface honest about the stand-ins implementing the
// interfaces they claim.
var (
	_ kv.KV                    = (*orgCreateKV)(nil)
	_ settings.Store           = (*orgCreateSettings)(nil)
	_ authn.SignupVerification = (*orgCreateVerification)(nil)
	_ tenant.Provisioner       = (*orgCreateProvisioner)(nil)
	_ audit.Audit              = (*recordedAudit)(nil)
)
