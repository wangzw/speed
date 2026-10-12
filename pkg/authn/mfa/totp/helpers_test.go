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

// rootKey is a root key for the field encryption of the test handles. It is
// longer than the floor and is not a secret of any deployment.
const rootKey = "totp-test-root-key-0123456789-abcdefghijklmnop"

// base is the instant the cases that need a fixed time start from.
var base = time.Date(2026, time.March, 14, 12, 0, 0, 0, time.UTC)

// acct is the account the cases register factors for.
const acct = authn.AccountID("acct-1")

// fakeClock is a clock a case moves. It reads in the zone it was built with, so
// a case can hand the factor a time that is not UTC.
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

// factorsTable is the factor register table of authn.mfa, which the register of
// the multi-factor module writes and this factor is keyed into by. It mirrors
// that module's migration; the cases here build it so that a real multi-factor
// module can drive this factor end to end.
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

// openHandle delivers a real database handle with the encryption key set, so the
// seed column can be sealed, and with no table built.
func openHandle(t *testing.T, key string) db.Database {
	t.Helper()
	items := dbtest.Items(nil)
	if key != "" {
		items = dbtest.Items{"encryption-key": key}
	}
	return dbtest.SQLite(t, filepath.Join(t.TempDir(), "totp.db"), items)
}

// openSerialHandle is openHandle with the pool held to one connection, so that
// two statements on one factor serialize on it rather than one failing on the
// engine's write lock. It is what the concurrency case uses, where what is under
// test is that the conditional write and not the pool decides the winner.
func openSerialHandle(t *testing.T, key string) db.Database {
	t.Helper()
	items := dbtest.Items{"max-open-conns": 1, "max-idle-conns": 1}
	if key != "" {
		items["encryption-key"] = key
	}
	return dbtest.SQLite(t, filepath.Join(t.TempDir(), "totp.db"), items)
}

// buildTable runs a migration tree's statements of one dialect against the
// handle, so the table a case needs is the one the module ships.
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

// createRegister builds the factor register table.
func createRegister(t *testing.T, h *gorm.DB) {
	t.Helper()
	if err := h.Exec(factorsTable).Error; err != nil {
		t.Fatalf("building the factor register table: %v", err)
	}
}

// rig is the factor over a real database handle with a fixed clock.
type rig struct {
	t      *testing.T
	factor *factor
	h      *gorm.DB
}

// newRig builds the factor over a handle with the seed table built and the
// encryption key set.
func newRig(t *testing.T) *rig { return newRigOn(t, openHandle(t, rootKey)) }

// newSerialRig is newRig over a handle whose pool is one connection.
func newSerialRig(t *testing.T) *rig { return newRigOn(t, openSerialHandle(t, rootKey)) }

func newRigOn(t *testing.T, database db.Database) *rig {
	t.Helper()
	h := database.DB().Session(&gorm.Session{Logger: logger.Discard})
	buildTable(t, h, migrationsFS, "sqlite")
	clk := newClock(base)
	return &rig{
		t:      t,
		h:      h,
		factor: &factor{register: &register{handle: h}, issuer: "speed", clock: clk.read},
	}
}

// seed writes a seed row for a factor of the account with the secret given.
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

// active is the record of an enrolled factor.
func active(factorID string) authn.FactorRecord {
	return authn.FactorRecord{ID: factorID, Account: acct, Kind: authn.FactorTOTP, Status: authn.FactorActive}
}

// generate is a fresh valid base32 seed.
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

// codeAt is the code a seed produces at the instant given, with the parameters
// the factor generates codes for.
func codeAt(t *testing.T, secret string, at time.Time) string {
	t.Helper()
	return codeWith(t, secret, at, pquernaotp.ValidateOpts{
		Period: periodSeconds, Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1,
	})
}

// codeWith is the code a seed produces with the options given, which the
// negative cases use to build a code the factor must not accept.
func codeWith(t *testing.T, secret string, at time.Time, opts pquernaotp.ValidateOpts) string {
	t.Helper()
	code, err := pquernaotp.GenerateCodeCustom(secret, at.UTC(), opts)
	if err != nil {
		t.Fatalf("generating a code: %v", err)
	}
	return code
}

// input is a submitted answer carrying a code.
func input(code string) authn.StepInput {
	return authn.StepInput{Action: authn.InputSubmit, Fields: map[string]string{codeField: code}}
}

// flow is an enrollment flow state.
func flow() authn.FlowState {
	return authn.FlowState{ID: "flow-1", Purpose: authn.PurposeEnroll, Account: acct}
}

// ---- the registry harness ----

// The stand-in modules an assembly drives: the capabilities another module
// delivers exclusively in a real command, supplied to a module of its own here.
const (
	dbModule      = "stand-in.db"
	mounterModule = "stand-in.mounter"
	accountModule = "stand-in.account"
)

// standIn is a module that declares the tokens given, exclusively when told to,
// and delivers instance from its New.
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

// stubReader is a configuration reader handing this module its two input items
// and nothing else: the issuer of its own namespace and the gateway's public
// address it defaults off. It is supplied to the registry the way a host
// supplies the one the loader built.
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

// stubThrottle is a failure back-off that is never in back-off and counts
// nothing, which is the state a working module is in when a case reaches it.
type stubThrottle struct{}

func (stubThrottle) Check(context.Context, authn.ThrottleScope, string) (time.Duration, error) {
	return 0, nil
}

func (stubThrottle) Fail(context.Context, authn.ThrottleScope, string) (bool, error) {
	return false, nil
}
func (stubThrottle) Succeed(context.Context, authn.ThrottleScope, string) error { return nil }

// accountStandIn carries the three capabilities the account module does. The
// account reads and writes are the embedded interface's; the back-off is the
// stub above, because the multi-factor module reaches it around every
// verification.
type accountStandIn struct {
	authn.Accounts
	authn.AccountAdmin
	authn.Throttle
}

// assembly drives a real registry through this module's descriptor and the real
// multi-factor module, which takes the TOTP factor up and drives it. The
// database, the mounter and the account capabilities are supplied as stand-in
// modules of their own, which is how the required capabilities are satisfied
// without the modules that deliver them.
type assembly struct {
	// key is the field-encryption root key; empty leaves the handle without one,
	// which is the assembly the startup check refuses.
	key string
	// seed builds this module's seed table before the drive.
	seed bool
	// register builds the factor register table before the drive, so that the
	// multi-factor module's own Start finds it.
	register bool
}

// drive advances the assembly to Start, runs action there, and returns what the
// drive returned. The database handle is handed to the action so a case can read
// what the registry wrote.
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

	reg := core.New()
	names := []string{ModuleName, mfa.ModuleName}
	reg.Register(Module())
	reg.Register(mfa.Module())
	reg.Register(standIn(dbModule, database, true, (*db.Database)(nil)))
	reg.Register(standIn(mounterModule, mounttest.New(t), true, (*mount.Mounter)(nil)))
	reg.Register(standIn(accountModule, accountStandIn{Throttle: stubThrottle{}}, true,
		(*authn.Accounts)(nil), (*authn.AccountAdmin)(nil), (*authn.Throttle)(nil)))
	reg.Register(config.Module())
	reg.Supply(stubReader{issuer: "speed"}, config.ModuleName)
	names = append(names, dbModule, mounterModule, accountModule, config.ModuleName)

	return reg.Drive(t.Context(), core.Drive{
		Depth:        core.StageStart,
		Participants: names,
		Action: func(ctx context.Context, reg *core.Registry) error {
			return action(ctx, reg, h)
		},
	})
}

// pendingID is the identifier of the account's pending TOTP factor row.
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

// secretOf reads the seed of a factor through the module's own store.
func secretOf(t *testing.T, h *gorm.DB, factorID string) string {
	t.Helper()
	s, found, err := (&register{handle: h}).load(context.Background(), factorID)
	if err != nil || !found {
		t.Fatalf("reading the seed of %s: found=%v err=%v", factorID, found, err)
	}
	return s.Secret
}

// kindOf is the kind of a step, for a case that reads what the factor answered.
func kindOf(t *testing.T, s authn.Step) step.Kind {
	t.Helper()
	return step.KindOf(s)
}
