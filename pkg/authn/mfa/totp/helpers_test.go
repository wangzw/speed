package totp

import (
	"context"
	"io/fs"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pquerna/otp"
	pquernaotp "github.com/pquerna/otp/totp"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/mount"
	"github.com/vislake/speed/pkg/authn/internal/mount/mounttest"
	"github.com/vislake/speed/pkg/authn/internal/step"
	"github.com/vislake/speed/pkg/authn/mfa"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/internal/dbtest"
)

const rootKey = "totp-test-root-key-0123456789-abcdefghijklmnop"

var base = time.Date(2026, time.March, 14, 12, 0, 0, 0, time.UTC)

const acct = authn.AccountID("acct-1")

const username = "alice"

type fakeAccounts struct {
	authn.Accounts
	identifiers map[authn.AccountID][]authn.Identifier
	err         error
}

// Identifiers answers the identifiers of the account, or the configured failure.
func (a fakeAccounts) Identifiers(_ context.Context, id authn.AccountID) ([]authn.Identifier, error) {
	if a.err != nil {
		return nil, a.err
	}
	return a.identifiers[id], nil
}

func named() fakeAccounts {
	return fakeAccounts{identifiers: map[authn.AccountID][]authn.Identifier{
		acct: {{Kind: authn.IdentifierUsername, Value: username}},
	}}
}

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newClock(at time.Time) *fakeClock { return &fakeClock{now: at} }

func (c *fakeClock) read() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

const factorsTable = `CREATE TABLE IF NOT EXISTS authn_factors (
    id           TEXT      NOT NULL PRIMARY KEY,
    account_id   TEXT      NOT NULL,
    kind         TEXT      NOT NULL CHECK (kind IN ('totp', 'sms', 'webauthn')),
    label        TEXT      NOT NULL DEFAULT '' CHECK (length(label) <= 64),
    status       SMALLINT  NOT NULL DEFAULT 0 CHECK (status IN (0, 1, 2)),
    created_at   TIMESTAMP NOT NULL,
    activated_at TIMESTAMP NULL,
    last_used_at TIMESTAMP NULL
)`

func openHandle(t *testing.T, key string) db.Database {
	t.Helper()
	items := dbtest.Items(nil)
	if key != "" {
		items = dbtest.Items{"encryption-key": key}
	}
	return dbtest.SQLite(t, filepath.Join(t.TempDir(), "totp.db"), items)
}

func openSerialHandle(t *testing.T, key string) db.Database {
	t.Helper()
	items := dbtest.Items{"max-open-conns": 1, "max-idle-conns": 1}
	if key != "" {
		items["encryption-key"] = key
	}
	return dbtest.SQLite(t, filepath.Join(t.TempDir(), "totp.db"), items)
}

func buildTable(t *testing.T, h *gorm.DB, tree fs.FS, dialect string) {
	t.Helper()
	entries, err := fs.ReadDir(tree, dialect)
	if err != nil {
		t.Fatalf("listing the %s migrations: %v", dialect, err)
	}
	for _, entry := range entries {
		sql, err := fs.ReadFile(tree, dialect+"/"+entry.Name())
		if err != nil {
			t.Fatalf("reading %s: %v", entry.Name(), err)
		}
		for _, statement := range strings.Split(string(sql), ";") {
			if strings.TrimSpace(statement) == "" {
				continue
			}
			if err := h.Exec(statement).Error; err != nil {
				t.Fatalf("applying %s: %v", entry.Name(), err)
			}
		}
	}
}

func createRegister(t *testing.T, h *gorm.DB) {
	t.Helper()
	if err := h.Exec(factorsTable).Error; err != nil {
		t.Fatalf("building the factor register table: %v", err)
	}
}

type rig struct {
	t      *testing.T
	factor *factor
	h      *gorm.DB
}

func newRig(t *testing.T) *rig { return newRigOn(t, openHandle(t, rootKey)) }

func newSerialRig(t *testing.T) *rig { return newRigOn(t, openSerialHandle(t, rootKey)) }

func newRigOn(t *testing.T, database db.Database) *rig {
	t.Helper()
	h := database.DB().Session(&gorm.Session{Logger: logger.Discard})
	buildTable(t, h, migrationsFS, "sqlite")
	clk := newClock(base)
	return &rig{
		t: t,
		h: h,
		factor: &factor{
			register: &register{handle: h},
			accounts: named(),
			issuer:   "speed",
			clock:    clk.read,
		},
	}
}

func (r *rig) seed(factorID, secret string) {
	r.t.Helper()
	if err := r.factor.register.insert(context.Background(), seed{
		FactorID:    factorID,
		Secret:      secret,
		Algorithm:   algorithmSHA1,
		Digits:      digitsOfSix,
		Period:      periodSeconds,
		LastCounter: 0,
	}); err != nil {
		r.t.Fatalf("seeding a factor: %v", err)
	}
}

func active(factorID string) authn.FactorRecord {
	return authn.FactorRecord{ID: factorID, Account: acct, Kind: authn.FactorTOTP, Status: authn.FactorActive}
}

func generate(t *testing.T) string {
	t.Helper()
	key, err := pquernaotp.Generate(pquernaotp.GenerateOpts{
		Issuer: "speed", AccountName: "acct-1", Period: periodSeconds, SecretSize: secretBytes,
		Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1,
	})
	if err != nil {
		t.Fatalf("generating a seed: %v", err)
	}
	return key.Secret()
}

func codeAt(t *testing.T, secret string, at time.Time) string {
	t.Helper()
	return codeWith(t, secret, at, pquernaotp.ValidateOpts{
		Period: periodSeconds, Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1,
	})
}

func codeWith(t *testing.T, secret string, at time.Time, opts pquernaotp.ValidateOpts) string {
	t.Helper()
	code, err := pquernaotp.GenerateCodeCustom(secret, at.UTC(), opts)
	if err != nil {
		t.Fatalf("generating a code: %v", err)
	}
	return code
}

func input(code string) authn.StepInput {
	return authn.StepInput{Action: authn.InputSubmit, Fields: map[string]string{codeField: code}}
}

func flow() authn.FlowState {
	return authn.FlowState{ID: "flow-1", Purpose: authn.PurposeEnroll, Account: acct}
}

const (
	dbModule      = "stand-in.db"
	mounterModule = "stand-in.mounter"
	accountModule = "stand-in.account"
)

func standIn(name string, instance any, exclusive bool, tokens ...core.Token) core.Module {
	provides := make([]core.Provision, len(tokens))
	for i, tk := range tokens {
		provides[i] = core.Provision{Token: tk, Exclusive: exclusive}
	}
	return core.Module{
		Name:     name,
		Provides: provides,
		New:      func(context.Context, *core.Registry) (any, error) { return instance, nil },
	}
}

type stubReader struct {
	issuer    string
	publicURL string
}

func (r stubReader) Decode(_ string, target any) error {
	switch v := target.(type) {
	case *moduleConfig:
		v.Issuer = r.issuer
	case *gateMount:
		v.PublicURL = r.publicURL
	}
	return nil
}

type stubThrottle struct{}

func (stubThrottle) Check(context.Context, authn.ThrottleScope, string) (time.Duration, error) {
	return 0, nil
}

func (stubThrottle) Fail(context.Context, authn.ThrottleScope, string) (bool, error) {
	return false, nil
}
func (stubThrottle) Succeed(context.Context, authn.ThrottleScope, string) error { return nil }

type accountStandIn struct {
	authn.Accounts
	authn.AccountAdmin
	authn.Throttle
}

type assembly struct {
	key      string
	seed     bool
	register bool
	accounts fakeAccounts
	issuer   string
}

func (a assembly) drive(t *testing.T, action func(ctx context.Context, reg *core.Registry, h *gorm.DB) error) error {
	t.Helper()
	database := openHandle(t, a.key)
	h := database.DB().Session(&gorm.Session{Logger: logger.Discard})
	if a.seed {
		buildTable(t, h, migrationsFS, "sqlite")
	}
	if a.register {
		createRegister(t, h)
	}

	accounts := a.accounts
	if accounts.identifiers == nil && accounts.err == nil {
		accounts = named()
	}
	issuer := a.issuer
	if issuer == "" {
		issuer = "speed"
	}

	reg := core.New()
	names := []string{ModuleName, mfa.ModuleName}
	reg.Register(Module())
	reg.Register(mfa.Module())
	reg.Register(standIn(dbModule, database, true, (*db.Database)(nil)))
	reg.Register(standIn(mounterModule, mounttest.New(t), true, (*mount.Mounter)(nil)))
	reg.Register(standIn(accountModule, accountStandIn{Accounts: accounts, Throttle: stubThrottle{}}, true,
		(*authn.Accounts)(nil), (*authn.AccountAdmin)(nil), (*authn.Throttle)(nil)))
	reg.Register(config.Module())
	reg.Supply(stubReader{issuer: issuer}, config.ModuleName)
	names = append(names, dbModule, mounterModule, accountModule, config.ModuleName)

	return reg.Drive(t.Context(), core.Drive{
		Depth:        core.StageStart,
		Participants: names,
		Action: func(ctx context.Context, reg *core.Registry) error {
			return action(ctx, reg, h)
		},
	})
}

func pendingID(t *testing.T, h *gorm.DB) string {
	t.Helper()
	var ids []string
	if err := h.Raw("SELECT id FROM authn_factors WHERE account_id = ? AND kind = 'totp' AND status = 0",
		string(acct)).Scan(&ids).Error; err != nil {
		t.Fatalf("reading the pending factor: %v", err)
	}
	if len(ids) != 1 {
		t.Fatalf("%d pending TOTP factors, want one", len(ids))
	}
	return ids[0]
}

func secretOf(t *testing.T, h *gorm.DB, factorID string) string {
	t.Helper()
	s, found, err := (&register{handle: h}).load(context.Background(), factorID)
	if err != nil || !found {
		t.Fatalf("reading the seed of %s: found=%v err=%v", factorID, found, err)
	}
	return s.Secret
}

func kindOf(t *testing.T, s authn.Step) step.Kind {
	t.Helper()
	return step.KindOf(s)
}
