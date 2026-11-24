package selfcreate

import (
	"context"
	"errors"
	"fmt"
	"net/http"
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
	"github.com/vislake/speed/pkg/kv"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/settings"
	"github.com/vislake/speed/pkg/tenant"
	tenantpersonal "github.com/vislake/speed/pkg/tenant/personal"
)

const unitAccount = authn.AccountID("7c1e0f6a")

var testNow = time.Date(2026, time.November, 5, 10, 15, 0, 0, time.UTC)

type fakeKV struct {
	mu           sync.Mutex
	values       map[string]kvEntry
	lockAttempts chan struct{}
}

type kvEntry struct {
	value   []byte
	expires time.Time
}

func newFakeKV() *fakeKV { return &fakeKV{values: map[string]kvEntry{}} }

func (k *fakeKV) live(key string) (kvEntry, bool) {
	entry, ok := k.values[key]
	if !ok {
		return kvEntry{}, false
	}
	if !entry.expires.IsZero() && !entry.expires.After(time.Now()) {
		delete(k.values, key)
		return kvEntry{}, false
	}
	return entry, true
}

func (k *fakeKV) Get(_ context.Context, key string) ([]byte, bool, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	entry, ok := k.live(key)
	if !ok {
		return nil, false, nil
	}
	return append([]byte(nil), entry.value...), true, nil
}

func (k *fakeKV) Set(_ context.Context, key string, value []byte, ttl time.Duration) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	entry := kvEntry{value: append([]byte(nil), value...)}
	if ttl > 0 {
		entry.expires = time.Now().Add(ttl)
	}
	k.values[key] = entry
	return nil
}

func (k *fakeKV) Delete(_ context.Context, key string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	delete(k.values, key)
	return nil
}

func (k *fakeKV) IncrByFloat(ctx context.Context, key string, delta float64) (float64, error) {
	return k.IncrByFloatWithTTL(ctx, key, delta, 0)
}

func (k *fakeKV) IncrByFloatWithTTL(_ context.Context, key string, delta float64, ttl time.Duration) (float64, error) {
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

func (k *fakeKV) CompareAndSwap(_ context.Context, key string, old, newVal []byte) (bool, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	entry, ok := k.live(key)
	if !ok {
		entry = kvEntry{}
	}
	if string(entry.value) != string(old) {
		return false, nil
	}
	entry.value = append([]byte(nil), newVal...)
	k.values[key] = entry
	return true, nil
}

func (k *fakeKV) SetIfAbsent(_ context.Context, key string, value []byte, ttl time.Duration) (bool, error) {
	k.mu.Lock()
	_, live := k.live(key)
	if !live {
		entry := kvEntry{value: append([]byte(nil), value...)}
		if ttl > 0 {
			entry.expires = time.Now().Add(ttl)
		}
		k.values[key] = entry
	}
	attempts := k.lockAttempts
	k.mu.Unlock()
	if attempts != nil {
		attempts <- struct{}{}
	}
	return !live, nil
}

type fakeSettings struct {
	settings.Store

	values map[string]int
	err    error
}

func (s *fakeSettings) Get(_ settings.Layer, key string, target any) error {
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

type fakeVerification struct {
	authn.SignupVerification

	mu     sync.Mutex
	status authn.SignupStatus
	err    error
	calls  int
}

func (v *fakeVerification) SignupStateOf(_ context.Context, _ authn.AccountID) (authn.SignupStatus, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.calls++
	return v.status, v.err
}

type fakeReservation struct {
	authn.UsernameReservation

	mu       sync.Mutex
	reserved map[string]bool
	err      error
	asked    []string
}

func (r *fakeReservation) ReservedUsername(_ context.Context, username string) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.asked = append(r.asked, username)
	return r.reserved[username], r.err
}

func newFakeReservation() *fakeReservation {
	return &fakeReservation{reserved: map[string]bool{
		"api": true, "admin": true, "system": true, "login": true, "root": true,
	}}
}

type fakeRoster struct {
	tenant.Catalog

	mu   sync.Mutex
	rows map[tenant.ID]tenant.Tenant
}

func newFakeRoster() *fakeRoster {
	return &fakeRoster{rows: map[tenant.ID]tenant.Tenant{}}
}

func (r *fakeRoster) Get(_ context.Context, id tenant.ID) (tenant.Tenant, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	row, found := r.rows[id]
	return row, found, nil
}

func (r *fakeRoster) ByName(_ context.Context, name string) (tenant.Tenant, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, row := range r.rows {
		if row.Name == name {
			return row, true, nil
		}
	}
	return tenant.Tenant{}, false, nil
}

func (r *fakeRoster) List(_ context.Context) ([]tenant.Tenant, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	rows := make([]tenant.Tenant, 0, len(r.rows))
	for _, row := range r.rows {
		rows = append(rows, row)
	}
	return rows, nil
}

func (r *fakeRoster) put(t tenant.Tenant) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.rows[t.ID] = t
}

type fakeMemberships struct {
	authn.Memberships

	rows map[authn.AccountID][]authn.Membership
}

func newFakeMemberships() *fakeMemberships {
	return &fakeMemberships{rows: map[authn.AccountID][]authn.Membership{}}
}

func (m *fakeMemberships) Tenants(_ context.Context, id authn.AccountID) ([]authn.Membership, error) {
	return append([]authn.Membership(nil), m.rows[id]...), nil
}

type provisionCall struct {
	tenant  tenant.Tenant
	creator string
}

type fakeProvisioner struct {
	tenant.Provisioner

	mu    sync.Mutex
	calls []provisionCall
	err   error
	block chan struct{}
}

func (p *fakeProvisioner) Provision(_ context.Context, t tenant.Tenant, creator string) error {
	if p.block != nil {
		<-p.block
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls = append(p.calls, provisionCall{tenant: t, creator: creator})
	return p.err
}

func (p *fakeProvisioner) recorded() []provisionCall {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]provisionCall(nil), p.calls...)
}

type recordedAudit struct {
	audit.Audit

	mu      sync.Mutex
	entries []audit.Entry
	err     error
}

func (a *recordedAudit) Record(_ context.Context, entry audit.Entry) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.err != nil {
		return a.err
	}
	a.entries = append(a.entries, entry)
	return nil
}

func (a *recordedAudit) rows() []audit.Entry {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]audit.Entry(nil), a.entries...)
}

type rig struct {
	product      *product
	roster       *fakeRoster
	memberships  *fakeMemberships
	reservation  *fakeReservation
	kv           *fakeKV
	verification *fakeVerification
	settings     *fakeSettings
	provisioner  *fakeProvisioner
	audit        *recordedAudit
}

func newRig() *rig {
	rig := &rig{
		roster:       newFakeRoster(),
		memberships:  newFakeMemberships(),
		reservation:  newFakeReservation(),
		kv:           newFakeKV(),
		verification: &fakeVerification{status: authn.SignupStatus{State: authn.SignupVerified}},
		settings: &fakeSettings{values: map[string]int{
			maxOrganizationsPath: 5,
			maxPerHourPath:       3,
		}},
		provisioner: &fakeProvisioner{},
		audit:       &recordedAudit{},
	}
	rig.product = &product{
		config:        moduleConfig{Enabled: true},
		roster:        rig.roster,
		memberships:   rig.memberships,
		verification:  rig.verification,
		reservations:  rig.reservation,
		settings:      rig.settings,
		kv:            rig.kv,
		provisioner:   rig.provisioner,
		audit:         rig.audit,
		logger:        log.Default(),
		now:           func() time.Time { return testNow },
		newIdentifier: func() string { return "org-" + uuid.NewString() },
	}
	return rig
}

func (r *rig) ctx(t *testing.T, options ...authntest.Option) context.Context {
	t.Helper()
	if len(options) == 0 {
		options = []authntest.Option{authntest.WithAccount(unitAccount)}
	}
	return authntest.Context(t, context.Background(), options...)
}

func (r *rig) create(t *testing.T, ctx context.Context, name, displayName string) (OrganizationCreatedReply, error) {
	t.Helper()
	return r.product.handleCreate(ctx, createInput{
		Name:        name,
		DisplayName: displayName,
		Description: "a description",
	})
}

func (r *rig) attempts(t *testing.T) int {
	t.Helper()
	count, err := r.product.hourlyCount(context.Background(), unitAccount)
	if err != nil {
		t.Fatalf("reading the hourly count: %v", err)
	}
	return count
}

func faultOf(t *testing.T, err error) speedhttp.Fault {
	t.Helper()
	var fault speedhttp.Fault
	if !errors.As(err, &fault) {
		t.Fatalf("the refusal is %v, want a fault", err)
	}
	return fault
}

// TestCreateRequiresAnIdentity holds the first step: a context with no identity
// is refused 401 and nothing is read or written.
func TestCreateRequiresAnIdentity(t *testing.T) {
	rig := newRig()
	_, err := rig.create(t, context.Background(), "acme-inc", "Acme")
	if fault := faultOf(t, err); fault.Status != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", fault.Status)
	}
	if len(rig.provisioner.recorded()) != 0 || rig.verification.calls != 0 {
		t.Error("an anonymous request reached the verification read or the provisioner")
	}
}

// TestCreateRefusesImpersonation checks that an identity acting for another account
// is refused 403 before the verification read.
func TestCreateRefusesImpersonation(t *testing.T) {
	rig := newRig()
	ctx := rig.ctx(t,
		authntest.WithAccount("target-account"),
		authntest.WithActor("operator-account"),
		authntest.WithSession("session-1"),
		authntest.WithScope("acme"),
	)
	_, err := rig.create(t, ctx, "acme-inc", "Acme")
	if fault := faultOf(t, err); fault.Status != http.StatusForbidden {
		t.Errorf("status = %d, want 403", fault.Status)
	}
	if rig.verification.calls != 0 || len(rig.provisioner.recorded()) != 0 {
		t.Error("an impersonating request reached the verification read or the provisioner")
	}
}

// TestCreateRefusesServiceAccountsAndStaticSubjects holds the third step.
func TestCreateRefusesServiceAccountsAndStaticSubjects(t *testing.T) {
	for name, options := range map[string][]authntest.Option{
		"a service account": {authntest.WithAccount("api-client"), authntest.WithMethods(authn.MethodClientSecret)},
		"a static subject": {
			authntest.WithAccount("static.ops"),
			authntest.WithBackend("static"),
			authntest.WithMethods(authn.MethodStatic),
		},
	} {
		t.Run(name, func(t *testing.T) {
			rig := newRig()
			_, err := rig.create(t, rig.ctx(t, options...), "acme-inc", "Acme")
			if fault := faultOf(t, err); fault.Status != http.StatusForbidden {
				t.Errorf("status = %d, want 403", fault.Status)
			}
			if rig.verification.calls != 0 || len(rig.provisioner.recorded()) != 0 {
				t.Error("the refused caller reached the verification read or the provisioner")
			}
		})
	}
}

// TestCreateRequiresAProvedEmailAddress checks that a pending address is refused as
// contact required, a read failure is a 503 and a missing one is admitted.
func TestCreateRequiresAProvedEmailAddress(t *testing.T) {
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
			rig := newRig()
			rig.verification.status = tc.status
			rig.verification.err = tc.err
			_, err := rig.create(t, rig.ctx(t), "acme-inc", "Acme")
			if tc.want == http.StatusCreated {
				if err != nil {
					t.Fatalf("the creation failed: %v", err)
				}
				return
			}
			fault := faultOf(t, err)
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

// TestCreateChecksTheEmailBeforeTheShape holds the order: a pending address is
// refused even when the body is also invalid.
func TestCreateChecksTheEmailBeforeTheShape(t *testing.T) {
	rig := newRig()
	rig.verification.status = authn.SignupStatus{State: authn.SignupPending}
	_, err := rig.product.handleCreate(rig.ctx(t), createInput{})
	fault := faultOf(t, err)
	if fault.Status != http.StatusForbidden || fault.Type != authn.ContactRequiredProblemType {
		t.Errorf("status/type = %d/%q, want 403/%q", fault.Status, fault.Type, authn.ContactRequiredProblemType)
	}
}

// TestCreateValidatesTheNameAndProfileShape checks the slug, the required
// display name and both fields' bounds.
func TestCreateValidatesTheNameAndProfileShape(t *testing.T) {
	cases := map[string]createInput{
		"a blank display name": {Name: "acme-inc", DisplayName: "   "},
		"a display name too long": {
			Name: "acme-inc", DisplayName: strings.Repeat("a", tenant.DisplayNameMaxRunes+1),
		},
		"a description too long": {
			Name: "acme-inc", DisplayName: "Acme",
			Description: strings.Repeat("a", tenant.DescriptionMaxRunes+1),
		},
		"an empty name":            {DisplayName: "Acme"},
		"a two-character name":     {Name: "ab", DisplayName: "Acme"},
		"an upper-case name":       {Name: "Acme", DisplayName: "Acme"},
		"an underscore":            {Name: "acme_inc", DisplayName: "Acme"},
		"a leading dash":           {Name: "-acme", DisplayName: "Acme"},
		"a built-in reserved name": {Name: "api", DisplayName: "Acme"},
	}
	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			rig := newRig()
			_, err := rig.product.handleCreate(rig.ctx(t), input)
			if fault := faultOf(t, err); fault.Status != http.StatusBadRequest {
				t.Errorf("status = %d, want 400", fault.Status)
			}
			if len(rig.provisioner.recorded()) != 0 {
				t.Error("an invalid body reached the provisioner")
			}
		})
	}
}

// TestCreateAsksTheReservedAnswerAndRefusesAPersonalPrefix checks that a name
// under the personal prefix is refused by the reserved-name answer.
func TestCreateAsksTheReservedAnswerAndRefusesAPersonalPrefix(t *testing.T) {
	rig := newRig()
	rig.reservation.reserved["personal-alice"] = true
	_, err := rig.create(t, rig.ctx(t), "personal-alice", "Acme")
	if fault := faultOf(t, err); fault.Status != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", fault.Status)
	}
	if len(rig.reservation.asked) != 1 || rig.reservation.asked[0] != "personal-alice" {
		t.Errorf("the reserved answer was asked %v, want the name", rig.reservation.asked)
	}
	if len(rig.provisioner.recorded()) != 0 {
		t.Error("a reserved name reached the provisioner")
	}
}

// TestCreateReadsTheReservedAnswerFailure is the seam's outage: the creation is
// not carried out and answers 503.
func TestCreateReadsTheReservedAnswerFailure(t *testing.T) {
	rig := newRig()
	rig.reservation.err = errors.New("the settings store is unavailable")
	_, err := rig.create(t, rig.ctx(t), "acme-inc", "Acme")
	if fault := faultOf(t, err); fault.Status != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", fault.Status)
	}
	if len(rig.provisioner.recorded()) != 0 {
		t.Error("an unreadable reserved set reached the provisioner")
	}
}

// TestCreateAnswersANameTheReservationCannotNormalize checks that
// authn.ErrInvalidIdentifier from the reservation answers 400, not 503.
func TestCreateAnswersANameTheReservationCannotNormalize(t *testing.T) {
	rig := newRig()
	rig.reservation.err = fmt.Errorf("%w: the name is the shape of an account identifier", authn.ErrInvalidIdentifier)
	_, err := rig.create(t, rig.ctx(t), "0f8fad5b-d9cb-469f-a165-70867728950e", "Acme")
	if fault := faultOf(t, err); fault.Status != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", fault.Status)
	}
	if len(rig.reservation.asked) != 1 || rig.reservation.asked[0] != "0f8fad5b-d9cb-469f-a165-70867728950e" {
		t.Errorf("the reserved answer was asked %v, want the name", rig.reservation.asked)
	}
	if len(rig.provisioner.recorded()) != 0 {
		t.Error("an unnameable name reached the provisioner")
	}
}

// TestCreateRefusesAnNameTheRosterHolds checks that a slug held as another tenant's
// name, identifier or username gets the identifier-unavailable refusal.
func TestCreateRefusesAnNameTheRosterHolds(t *testing.T) {
	cases := map[string]tenant.Tenant{
		"a name":        {ID: "tenant-1", Name: "acme-inc", Status: tenant.StatusActive},
		"an identifier": {ID: "acme-inc", Name: "another", Status: tenant.StatusActive},
		"a username":    {ID: tenantpersonal.TenantID(authn.AccountID("alice")), Name: "acme-inc", Status: tenant.StatusActive},
		"a tombstone":   {ID: "tenant-2", Name: "acme-inc", Status: tenant.StatusDeleted},
	}
	for name, row := range cases {
		t.Run(name, func(t *testing.T) {
			rig := newRig()
			rig.roster.put(row)
			_, err := rig.create(t, rig.ctx(t), "acme-inc", "Acme")
			fault := faultOf(t, err)
			if fault.Status != http.StatusConflict || fault.Type != problemPrefix+identifierUnavailable {
				t.Errorf("status/type = %d/%q, want 409/%q", fault.Status, fault.Type,
					problemPrefix+identifierUnavailable)
			}
			if len(rig.provisioner.recorded()) != 0 {
				t.Error("a held name reached the provisioner")
			}
			if got := rig.attempts(t); got != 0 {
				t.Errorf("a held name counted %d attempt(s), want none", got)
			}
		})
	}
}

// TestCreateCountsOnlyActiveBusinessMemberships checks that personal and
// disabled tenants and suspended memberships are not counted toward the cap.
func TestCreateCountsOnlyActiveBusinessMemberships(t *testing.T) {
	rig := newRig()
	rig.settings.values[maxOrganizationsPath] = 4
	addBusiness := func(name string, status tenant.Status) {
		rig.roster.put(tenant.Tenant{ID: tenant.ID(name), Name: name, Status: status})
		rig.memberships.rows[unitAccount] = append(rig.memberships.rows[unitAccount],
			authn.Membership{Account: unitAccount, Tenant: name, Active: true, Source: "admin"})
	}
	for i := range 3 {
		addBusiness(fmt.Sprintf("biz-%d", i), tenant.StatusActive)
	}
	addBusiness("biz-disabled", tenant.StatusDisabled)
	personal := string(tenantpersonal.TenantID(unitAccount))
	rig.roster.put(tenant.Tenant{ID: tenant.ID(personal), Name: "unit-user", Status: tenant.StatusActive})
	rig.memberships.rows[unitAccount] = append(rig.memberships.rows[unitAccount],
		authn.Membership{Account: unitAccount, Tenant: personal, Active: true, Source: "personal"})
	rig.memberships.rows[unitAccount] = append(rig.memberships.rows[unitAccount],
		authn.Membership{Account: unitAccount, Tenant: "biz-suspended", Active: false, Source: "admin"})

	if _, err := rig.create(t, rig.ctx(t), "acme-inc", "Acme"); err != nil {
		t.Fatalf("the creation below the cap failed: %v", err)
	}

	addBusiness("biz-3", tenant.StatusActive)
	_, err := rig.create(t, rig.ctx(t), "acme-two", "Acme Two")
	fault := faultOf(t, err)
	if fault.Status != http.StatusConflict || fault.Type != problemPrefix+organizationLimitCode {
		t.Errorf("status/type = %d/%q, want 409/%q", fault.Status, fault.Type,
			problemPrefix+organizationLimitCode)
	}
	if len(rig.provisioner.recorded()) != 1 {
		t.Errorf("the provisioner was asked %d time(s), want once", len(rig.provisioner.recorded()))
	}
}

// TestCreateLimitsTheHourlyCount checks the hourly bound, its Retry-After and
// the new window an hour later.
func TestCreateLimitsTheHourlyCount(t *testing.T) {
	rig := newRig()
	for i := range 3 {
		if _, err := rig.create(t, rig.ctx(t), fmt.Sprintf("acme-%d", i), fmt.Sprintf("Acme %d", i)); err != nil {
			t.Fatalf("creation %d below the hourly limit failed: %v", i, err)
		}
	}
	_, err := rig.create(t, rig.ctx(t), "acme-four", "Acme Four")
	fault := faultOf(t, err)
	if fault.Status != http.StatusTooManyRequests || fault.Type != problemPrefix+rateLimitedCode {
		t.Fatalf("status/type = %d/%q, want 429/%q", fault.Status, fault.Type, problemPrefix+rateLimitedCode)
	}
	if len(fault.Headers) != 1 || fault.Headers[0].Name != "Retry-After" {
		t.Fatalf("headers = %+v, want one Retry-After", fault.Headers)
	}
	if got := fault.Headers[0].Value; got != "2700" {
		t.Errorf("Retry-After = %q, want %q", got, "2700")
	}
	if len(rig.provisioner.recorded()) != 3 {
		t.Errorf("the provisioner was asked %d time(s), want three", len(rig.provisioner.recorded()))
	}

	rig.product.now = func() time.Time { return testNow.Add(time.Hour) }
	if _, err := rig.create(t, rig.ctx(t), "acme-next-hour", "Acme Next Hour"); err != nil {
		t.Errorf("the next hour's creation failed: %v", err)
	}
}

// TestCreateDoesNotCountRefusalsBeforeTheCount checks that a pre-check refusal
// consumes no place.
func TestCreateDoesNotCountRefusalsBeforeTheCount(t *testing.T) {
	rig := newRig()
	_, err := rig.product.handleCreate(rig.ctx(t), createInput{DisplayName: "Acme"})
	if fault := faultOf(t, err); fault.Status != http.StatusBadRequest {
		t.Fatalf("the shape refusal's status = %d, want 400", fault.Status)
	}
	if got := rig.attempts(t); got != 0 {
		t.Fatalf("a shape refusal counted %d attempt(s), want none", got)
	}

	rig.settings.values[maxOrganizationsPath] = 1
	rig.roster.put(tenant.Tenant{ID: "biz", Name: "biz", Status: tenant.StatusActive})
	rig.memberships.rows[unitAccount] = []authn.Membership{
		{Account: unitAccount, Tenant: "biz", Active: true, Source: "admin"},
	}
	_, err = rig.create(t, rig.ctx(t), "acme-inc", "Acme")
	if fault := faultOf(t, err); fault.Status != http.StatusConflict {
		t.Fatalf("the cap refusal's status = %d, want 409", fault.Status)
	}
	if got := rig.attempts(t); got != 0 {
		t.Errorf("a cap refusal counted %d attempt(s), want none", got)
	}
}

// TestCreateCountsAProvisionerFailure checks that a counted attempt is consumed
// even when the provisioner fails.
func TestCreateCountsAProvisionerFailure(t *testing.T) {
	rig := newRig()
	rig.provisioner.err = errors.New("the roster is unavailable")
	if _, err := rig.create(t, rig.ctx(t), "acme-inc", "Acme"); err == nil {
		t.Fatal("the failed provision returned no error")
	}
	if got := rig.attempts(t); got != 1 {
		t.Fatalf("a failed provision counted %d attempt(s), want one", got)
	}

	rig.provisioner.err = nil
	for i := range 2 {
		if _, err := rig.create(t, rig.ctx(t), fmt.Sprintf("acme-%d", i), fmt.Sprintf("Acme %d", i)); err != nil {
			t.Fatalf("creation %d after the failed provision failed: %v", i, err)
		}
	}
	_, err := rig.create(t, rig.ctx(t), "acme-fourth", "Acme Fourth")
	if fault := faultOf(t, err); fault.Status != http.StatusTooManyRequests {
		t.Errorf("the fourth request's status = %d, want 429", fault.Status)
	}
}

// TestCreateCountsAnAuditFailure checks that the attempt is counted before the
// mandatory audit, so an audit failure consumes it.
func TestCreateCountsAnAuditFailure(t *testing.T) {
	rig := newRig()
	rig.audit.err = errors.New("the audit store is down")
	_, err := rig.create(t, rig.ctx(t), "acme-inc", "Acme")
	if fault := faultOf(t, err); fault.Status != http.StatusServiceUnavailable {
		t.Fatalf("the audit refusal's status = %d, want 503", fault.Status)
	}
	if got := rig.attempts(t); got != 1 {
		t.Fatalf("a failed audit counted %d attempt(s), want one", got)
	}

	rig.audit.err = nil
	for i := range 2 {
		if _, err = rig.create(t, rig.ctx(t), fmt.Sprintf("acme-%d", i), fmt.Sprintf("Acme %d", i)); err != nil {
			t.Fatalf("creation %d after the failed audit failed: %v", i, err)
		}
	}
	_, err = rig.create(t, rig.ctx(t), "acme-fourth", "Acme Fourth")
	if fault := faultOf(t, err); fault.Status != http.StatusTooManyRequests {
		t.Errorf("the fourth request's status = %d, want 429", fault.Status)
	}
}

// TestCreateHoldsThePerAccountLock checks that a live creation lock is a 409 refusal
// and a completed request frees the lock.
func TestCreateHoldsThePerAccountLock(t *testing.T) {
	rig := newRig()
	key := lockKeyOf(unitAccount)
	if err := rig.kv.Set(context.Background(), key, []byte("held"), lockTTL); err != nil {
		t.Fatalf("taking the stand-in lock: %v", err)
	}
	_, err := rig.create(t, rig.ctx(t), "acme-inc", "Acme")
	fault := faultOf(t, err)
	if fault.Status != http.StatusConflict || fault.Type != problemPrefix+creationInProgressCode {
		t.Errorf("status/type = %d/%q, want 409/%q", fault.Status, fault.Type,
			problemPrefix+creationInProgressCode)
	}
	if len(rig.provisioner.recorded()) != 0 {
		t.Error("a locked request reached the provisioner")
	}

	if err := rig.kv.Delete(context.Background(), key); err != nil {
		t.Fatalf("releasing the stand-in lock: %v", err)
	}
	if _, err := rig.create(t, rig.ctx(t), "acme-inc", "Acme"); err != nil {
		t.Fatalf("the creation after the lock was released failed: %v", err)
	}
	if _, held, err := rig.kv.Get(context.Background(), key); err != nil {
		t.Fatalf("reading the lock back: %v", err)
	} else if held {
		t.Error("the completed request left the creation lock behind")
	}
}

// TestCreateHoldsTheLockAgainstConcurrentRequests checks that one account's
// concurrent requests cannot both reach the provisioner.
func TestCreateHoldsTheLockAgainstConcurrentRequests(t *testing.T) {
	const requests = 8
	rig := newRig()
	release := make(chan struct{})
	rig.provisioner.block = release
	rig.kv.lockAttempts = make(chan struct{}, requests)

	var wait sync.WaitGroup
	statuses := make(chan int, requests)
	ctx := rig.ctx(t)
	for i := range requests {
		wait.Add(1)
		go func(i int) {
			defer wait.Done()
			_, err := rig.product.handleCreate(ctx, createInput{
				Name:        fmt.Sprintf("acme-%d", i),
				DisplayName: fmt.Sprintf("Acme %d", i),
			})
			if err == nil {
				statuses <- http.StatusCreated
				return
			}
			var fault speedhttp.Fault
			if errors.As(err, &fault) {
				statuses <- fault.Status
				return
			}
			statuses <- 0
		}(i)
	}
	for range requests {
		<-rig.kv.lockAttempts
	}
	close(release)
	wait.Wait()
	close(statuses)

	created, inProgress := 0, 0
	for status := range statuses {
		switch status {
		case http.StatusCreated:
			created++
		case http.StatusConflict:
			inProgress++
		default:
			t.Errorf("a concurrent request answered %d, want 201 or 409", status)
		}
	}
	if created != 1 {
		t.Errorf("%d requests were created, want one", created)
	}
	if inProgress != requests-1 {
		t.Errorf("%d requests were refused creation-in-progress, want %d", inProgress, requests-1)
	}
	if got := len(rig.provisioner.recorded()); got != 1 {
		t.Errorf("the provisioner was asked %d time(s), want once", got)
	}
	if got := rig.attempts(t); got != 1 {
		t.Errorf("the concurrent requests counted %d attempt(s), want one", got)
	}
}

// TestCreateWritesTheMandatoryAuditRow checks that the mandatory row lands before
// the provisioner and a cross-replica race adds no second row.
func TestCreateWritesTheMandatoryAuditRow(t *testing.T) {
	rig := newRig()
	reply, err := rig.create(t, rig.ctx(t), "acme-inc", "Acme")
	if err != nil {
		t.Fatalf("the creation failed: %v", err)
	}
	rows := rig.audit.rows()
	if len(rows) != 1 {
		t.Fatalf("audit rows = %+v, want exactly one", rows)
	}
	row := rows[0]
	if row.Action != auditAction || row.Outcome != outcomeSuccess {
		t.Errorf("audit row = %+v, want action %q and outcome success", row, auditAction)
	}
	if row.Object != reply.Tenant.Name {
		t.Errorf("audit object = %q, want the new tenant's name %q", row.Object, reply.Tenant.Name)
	}
	if row.Detail[detailAccount] != string(unitAccount) || row.Detail[detailTenant] != reply.Tenant.Name {
		t.Errorf("audit detail = %+v, want the account and the tenant's name", row.Detail)
	}
	if row.Detail[detailActionID] == "" {
		t.Error("the audit detail carries no action_id")
	}

	for name, cause := range map[string]error{
		"identifier taken": fmt.Errorf("%w: identifier taken", tenant.ErrTenantExists),
		"name taken":       fmt.Errorf("%w: name taken", tenant.ErrNameConflict),
	} {
		t.Run(name, func(t *testing.T) {
			rig := newRig()
			rig.provisioner.err = cause
			_, err := rig.create(t, rig.ctx(t), "acme-inc", "Acme")
			fault := faultOf(t, err)
			if fault.Status != http.StatusConflict || fault.Type != problemPrefix+identifierUnavailable {
				t.Errorf("status/type = %d/%q, want 409/%q", fault.Status, fault.Type,
					problemPrefix+identifierUnavailable)
			}
			if rows := rig.audit.rows(); len(rows) != 1 || rows[0].Outcome != outcomeSuccess {
				t.Errorf("audit rows after the race = %+v, want the one mandatory row", rows)
			}
		})
	}
}

// TestCreateClosesAFailedProvisionWithAFailedRow holds the ordinary failure:
// the mandatory row and a linked failed row, and a 503.
func TestCreateClosesAFailedProvisionWithAFailedRow(t *testing.T) {
	rig := newRig()
	rig.provisioner.err = errors.New("the roster is unavailable")
	_, err := rig.create(t, rig.ctx(t), "acme-inc", "Acme")
	if fault := faultOf(t, err); fault.Status != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", fault.Status)
	}
	rows := rig.audit.rows()
	if len(rows) != 2 || rows[0].Outcome != outcomeSuccess || rows[1].Outcome != outcomeFailed {
		t.Fatalf("audit rows = %+v, want the mandatory row and a linked failed row", rows)
	}
	if rows[0].Detail[detailActionID] != rows[1].Detail[detailActionID] {
		t.Error("the two rows carry different action identifiers")
	}
}

// TestCreateGeneratesTheIdentifier checks the generated org-<uuid> identifier,
// the tenant's values and the creator handed to the provisioner.
func TestCreateGeneratesTheIdentifier(t *testing.T) {
	rig := newRig()
	reply, err := rig.create(t, rig.ctx(t), "  acme-inc  ", "  Acme  ")
	if err != nil {
		t.Fatalf("the creation failed: %v", err)
	}
	if reply.Tenant.Name != "acme-inc" {
		t.Errorf("name = %q, want the trimmed name", reply.Tenant.Name)
	}
	provisioned := rig.provisioner.recorded()
	if len(provisioned) != 1 {
		t.Fatalf("the provisioner was asked %d time(s), want once", len(provisioned))
	}
	generated := string(provisioned[0].tenant.ID)
	if !strings.HasPrefix(generated, "org-") {
		t.Errorf("id = %q, want the %q prefix", generated, "org-")
	}
	if _, err := uuid.Parse(strings.TrimPrefix(generated, "org-")); err != nil {
		t.Errorf("the identifier's tail is not a UUID: %v", err)
	}
	if generated == reply.Tenant.Name {
		t.Error("the generated identifier equals the caller's name")
	}
	if reply.Tenant.DisplayName != "Acme" {
		t.Errorf("display_name = %q, want the trimmed value", reply.Tenant.DisplayName)
	}
	if reply.Tenant.Status != tenant.StatusActive.String() {
		t.Errorf("status = %q, want active", reply.Tenant.Status)
	}
	calls := rig.provisioner.recorded()
	if len(calls) != 1 {
		t.Fatalf("the provisioner was asked %d time(s), want once", len(calls))
	}
	got := calls[0].tenant
	if got.Name != reply.Tenant.Name {
		t.Errorf("the provisioned tenant is %q, want the answered name %q", got.Name, reply.Tenant.Name)
	}
	if got.Profile.DisplayName != "Acme" || got.Status != tenant.StatusActive {
		t.Errorf("the provisioned tenant carries %+v, want the profile and the active state", got)
	}
	if got.OwnerAccount != string(unitAccount) {
		t.Errorf("owner = %q, want the creator %q", got.OwnerAccount, unitAccount)
	}
	if calls[0].creator != string(unitAccount) {
		t.Errorf("creator = %q, want the acting account %q", calls[0].creator, unitAccount)
	}
}

// TestCreateRetriesAConflictingIdentifier checks that a collision is regenerated
// up to three retries, then refused with no provision.
func TestCreateRetriesAConflictingIdentifier(t *testing.T) {
	rig := newRig()
	for _, taken := range []string{"org-taken-1", "org-taken-2", "org-taken-3"} {
		rig.roster.put(tenant.Tenant{ID: tenant.ID(taken), Name: taken, Status: tenant.StatusActive})
	}
	sequence := []string{"org-taken-1", "org-taken-2", "org-taken-3", "org-free"}
	next := 0
	rig.product.newIdentifier = func() string {
		out := sequence[next]
		next++
		return out
	}
	reply, err := rig.create(t, rig.ctx(t), "acme-inc", "Acme")
	if err != nil {
		t.Fatalf("the creation after three retries failed: %v", err)
	}
	provisioned := rig.provisioner.recorded()
	if len(provisioned) != 1 || string(provisioned[0].tenant.ID) != "org-free" {
		t.Errorf("the provisioned tenant = %+v, want the free identifier", provisioned)
	}
	if reply.Tenant.Name != "acme-inc" {
		t.Errorf("the answer names %q, want the caller's name", reply.Tenant.Name)
	}

	rig = newRig()
	for i := range identifierRetries + 1 {
		taken := fmt.Sprintf("org-taken-%d", i)
		rig.roster.put(tenant.Tenant{ID: tenant.ID(taken), Name: taken, Status: tenant.StatusActive})
	}
	next = 0
	rig.product.newIdentifier = func() string {
		out := fmt.Sprintf("org-taken-%d", next)
		next++
		return out
	}
	_, err = rig.create(t, rig.ctx(t), "acme-inc", "Acme")
	fault := faultOf(t, err)
	if fault.Status != http.StatusConflict || fault.Type != problemPrefix+identifierUnavailable {
		t.Errorf("status/type = %d/%q, want 409/%q", fault.Status, fault.Type,
			problemPrefix+identifierUnavailable)
	}
	if len(rig.provisioner.recorded()) != 0 {
		t.Error("the exhausted pre-check reached the provisioner")
	}
}

// TestCreateNeverTakesTheNameAsItsIdentifier checks that a generated identifier that
// matches a name is retried like any other taken value.
func TestCreateNeverTakesTheNameAsItsIdentifier(t *testing.T) {
	rig := newRig()
	next := 0
	rig.product.newIdentifier = func() string {
		next++
		if next == 1 {
			return "acme-inc"
		}
		return "org-free"
	}
	reply, err := rig.create(t, rig.ctx(t), "acme-inc", "Acme")
	if err != nil {
		t.Fatalf("the creation failed: %v", err)
	}
	provisioned := rig.provisioner.recorded()
	if len(provisioned) != 1 || string(provisioned[0].tenant.ID) != "org-free" ||
		reply.Tenant.Name != "acme-inc" {
		t.Errorf("the provisioned tenant is %+v, want org-free/acme-inc", provisioned)
	}
}

// TestCreateReadsTheLimitsAtRequestTime checks that the limits come from the runtime
// settings' global layer and a read failure is a 503 that creates nothing.
func TestCreateReadsTheLimitsAtRequestTime(t *testing.T) {
	rig := newRig()
	rig.settings.values[maxOrganizationsPath] = 1
	rig.roster.put(tenant.Tenant{ID: "biz", Name: "biz", Status: tenant.StatusActive})
	rig.memberships.rows[unitAccount] = []authn.Membership{
		{Account: unitAccount, Tenant: "biz", Active: true, Source: "admin"},
	}
	_, err := rig.create(t, rig.ctx(t), "acme-inc", "Acme")
	if fault := faultOf(t, err); fault.Status != http.StatusConflict {
		t.Errorf("status with a one-organization cap = %d, want 409", fault.Status)
	}

	rig = newRig()
	rig.settings.err = errors.New("the settings store is unavailable")
	_, err = rig.create(t, rig.ctx(t), "acme-inc", "Acme")
	if fault := faultOf(t, err); fault.Status != http.StatusServiceUnavailable {
		t.Errorf("status when the settings read fails = %d, want 503", fault.Status)
	}
}

// TestCreateRejectsAnUnusableLimit holds the fail-closed reading of a value the
// declaration's domain does not admit.
func TestCreateRejectsAnUnusableLimit(t *testing.T) {
	rig := newRig()
	rig.settings.values[maxPerHourPath] = 0
	_, err := rig.create(t, rig.ctx(t), "acme-inc", "Acme")
	if fault := faultOf(t, err); fault.Status != http.StatusServiceUnavailable {
		t.Errorf("status with a zero hourly limit = %d, want 503", fault.Status)
	}
}

// TestCreateWritesNoAuditRowForAPreCountRefusal checks that a request refused
// before the attempt is counted writes no audit row.
func TestCreateWritesNoAuditRowForAPreCountRefusal(t *testing.T) {
	rig := newRig()
	_, err := rig.product.handleCreate(rig.ctx(t), createInput{DisplayName: "Acme"})
	if fault := faultOf(t, err); fault.Status != http.StatusBadRequest {
		t.Fatalf("the shape refusal's status = %d, want 400", fault.Status)
	}
	if rows := rig.audit.rows(); len(rows) != 0 {
		t.Errorf("a refused call wrote %+v, want no audit row", rows)
	}
}

var (
	_ kv.KV                     = (*fakeKV)(nil)
	_ settings.Store            = (*fakeSettings)(nil)
	_ authn.SignupVerification  = (*fakeVerification)(nil)
	_ authn.UsernameReservation = (*fakeReservation)(nil)
	_ tenant.Provisioner        = (*fakeProvisioner)(nil)
	_ tenant.Catalog            = (*fakeRoster)(nil)
	_ authn.Memberships         = (*fakeMemberships)(nil)
	_ audit.Audit               = (*recordedAudit)(nil)
)
