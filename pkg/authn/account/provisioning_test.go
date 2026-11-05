package account

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/authn/provisioning"
	"github.com/vislake/speed/pkg/authn/provisioning/provisioningtest"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
)

// The cases here drive the in-transaction provisioning seam: an account is
// created or deleted together with what a provisioning module writes for it, or
// neither happens.

// createProvisioningProbe adds the table a stand-in seam writes into, so that a
// case can tell a write made on the caller's transaction from one made on a
// connection of its own.
func createProvisioningProbe(t *testing.T, h *harness) {
	t.Helper()
	if err := h.handle.Exec("CREATE TABLE provisioning_probe (account_id TEXT NOT NULL)").Error; err != nil {
		t.Fatalf("creating the probe table: %v", err)
	}
}

// countIn counts a table's rows through the given session, which is the
// transaction the seam was handed.
func countIn(t *testing.T, session *gorm.DB, table, where string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := session.Table(table).Where(where, args...).Count(&n).Error; err != nil {
		t.Fatalf("counting %s inside the seam: %v", table, err)
	}
	return n
}

func requireInTransaction(t *testing.T, tx *gorm.DB) {
	t.Helper()
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		t.Fatalf("the seam was handed a handle outside the account's transaction")
	}
}

// A creation provisions the account inside its own transaction, after the row,
// its identifier and its credential are written. The stand-in's write is
// committed with the account, and the facts it was handed are the account's.
func TestCreateProvisionsTheAccountInsideItsTransaction(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		createProvisioningProbe(t, h)
		seam := provisioningtest.New()
		seam.OnCreate = func(_ context.Context, tx *gorm.DB, facts provisioning.AccountFacts) error {
			requireInTransaction(t, tx)
			if n := countIn(t, tx, tableAccounts, "id = ?", facts.ID); n != 1 {
				t.Errorf("the account row is not in the transaction when the seam runs")
			}
			if n := countIn(t, tx, tableIdentifiers, "account_id = ? AND kind = ?",
				facts.ID, string(authn.IdentifierUsername)); n != 1 {
				t.Errorf("the username identifier is not in the transaction when the seam runs")
			}
			if n := countIn(t, tx, tableCredentials, "account_id = ?", facts.ID); n != 1 {
				t.Errorf("the credential is not in the transaction when the seam runs")
			}
			return tx.Exec("INSERT INTO provisioning_probe (account_id) VALUES (?)", facts.ID).Error
		}
		h.p.provisioning = seam

		id := h.user("alice", "password123")

		calls := seam.Creates()
		if len(calls) != 1 {
			t.Fatalf("the seam was called %d time(s), want once", len(calls))
		}
		want := provisioning.AccountFacts{
			ID: id.String(), Kind: string(authn.KindUser), Status: "active",
			Username: "alice", DisplayName: "alice",
		}
		if calls[0] != want {
			t.Errorf("the seam was handed %+v, want %+v", calls[0], want)
		}
		if n := h.count("provisioning_probe", "account_id = ?", id.String()); n != 1 {
			t.Errorf("the seam's write is %d row(s) after the commit, want it committed with the account", n)
		}
	})
}

// A provisioning refusal fails the account's transaction: the account row, its
// identifier, its credential and the seam's own write are all rolled back.
func TestAProvisioningFailureRollsBackTheAccount(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		createProvisioningProbe(t, h)
		seam := provisioningtest.New()
		seam.OnCreate = func(_ context.Context, tx *gorm.DB, facts provisioning.AccountFacts) error {
			return tx.Exec("INSERT INTO provisioning_probe (account_id) VALUES (?)", facts.ID).Error
		}
		seam.CreateErr = errors.New("provisioning refused")
		h.p.provisioning = seam

		_, err := h.p.Create(t.Context(), authn.CreateAccount{
			Username: "alice", Email: "alice@example.com", EmailVerifiedVia: authn.Verification{Via: authn.VerifiedViaAdmin},
			Via: "admin",
		})
		if err == nil || !strings.Contains(err.Error(), "provisioning refused") {
			t.Fatalf("creating an account the seam refused gave %v, want the seam's failure", err)
		}
		for _, table := range []string{tableAccounts, tableIdentifiers, tableCredentials, tablePending, "provisioning_probe"} {
			if n := h.count(table, ""); n != 0 {
				t.Errorf("%s holds %d row(s) after the refused creation, want the whole transaction rolled back", table, n)
			}
		}
	})
}

// Self-registration takes the same seam inside its one transaction.
func TestRegisterProvisionsTheAccountInsideItsTransaction(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		createProvisioningProbe(t, h)
		seam := provisioningtest.New()
		seam.OnCreate = func(_ context.Context, tx *gorm.DB, facts provisioning.AccountFacts) error {
			requireInTransaction(t, tx)
			if n := countIn(t, tx, tableAccounts, "id = ? AND signup_state = ?", facts.ID, signupPending); n != 1 {
				t.Errorf("the registration's row is not in the transaction when the seam runs")
			}
			return tx.Exec("INSERT INTO provisioning_probe (account_id) VALUES (?)", facts.ID).Error
		}
		h.p.provisioning = seam
		h.openSelfRegistration()

		registered := h.mustRegister("alice", "alice@example.com", "password123")
		calls := seam.Creates()
		if len(calls) != 1 || calls[0].ID != registered.ID.String() ||
			calls[0].Username != "alice" || calls[0].Status != "active" || calls[0].Kind != string(authn.KindUser) {
			t.Fatalf("the seam was handed %+v, want the registered account's facts", calls)
		}
		if n := h.count("provisioning_probe", "account_id = ?", registered.ID.String()); n != 1 {
			t.Errorf("the seam's write is %d row(s) after the commit, want it committed with the registration", n)
		}
	})
}

// A deletion provisions the account's end inside the same transaction, after
// the account's own rows are erased. The facts still name the username and the
// display name the erase has already removed.
func TestDeleteProvisionsTheAccountInsideItsTransaction(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		createProvisioningProbe(t, h)
		seam := provisioningtest.New()
		h.p.provisioning = seam
		id := h.user("alice", "password123")

		seam.OnDelete = func(_ context.Context, tx *gorm.DB, facts provisioning.AccountFacts) error {
			requireInTransaction(t, tx)
			if n := countIn(t, tx, tableAccounts, "id = ? AND status = ?", facts.ID, statusDeleted); n != 1 {
				t.Errorf("the tombstone is not in the transaction when the seam runs")
			}
			if n := countIn(t, tx, tableIdentifiers, "account_id = ?", facts.ID); n != 0 {
				t.Errorf("the account's identifiers were not erased before the seam ran")
			}
			if n := countIn(t, tx, tableCredentials, "account_id = ?", facts.ID); n != 0 {
				t.Errorf("the account's credential was not erased before the seam ran")
			}
			return tx.Exec("INSERT INTO provisioning_probe (account_id) VALUES (?)", facts.ID).Error
		}

		if err := h.p.Delete(t.Context(), id, "admin"); err != nil {
			t.Fatalf("deleting: %v", err)
		}
		calls := seam.Deletions()
		if len(calls) != 1 {
			t.Fatalf("the deletion seam was called %d time(s), want once", len(calls))
		}
		want := provisioning.AccountFacts{
			ID: id.String(), Kind: string(authn.KindUser), Status: "active",
			Username: "alice", DisplayName: "alice",
		}
		if calls[0] != want {
			t.Errorf("the deletion seam was handed %+v, want %+v", calls[0], want)
		}
		if n := h.count("provisioning_probe", "account_id = ?", id.String()); n != 1 {
			t.Errorf("the seam's write is %d row(s) after the commit, want it committed with the tombstone", n)
		}
	})
}

// A deletion the seam refuses leaves the account in service: the tombstone, the
// erase and the seam's own write all roll back.
func TestADeletionProvisioningFailureRollsBackTheTombstone(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		createProvisioningProbe(t, h)
		seam := provisioningtest.New()
		h.p.provisioning = seam
		id := h.user("alice", "password123")

		seam.OnDelete = func(_ context.Context, tx *gorm.DB, facts provisioning.AccountFacts) error {
			return tx.Exec("INSERT INTO provisioning_probe (account_id) VALUES (?)", facts.ID).Error
		}
		seam.DeleteErr = errors.New("provisioning refused")

		err := h.p.Delete(t.Context(), id, "admin")
		if err == nil || !strings.Contains(err.Error(), "provisioning refused") {
			t.Fatalf("deleting an account the seam refused gave %v, want the seam's failure", err)
		}
		if n := h.count(tableAccounts, "id = ? AND status <> ?", id.String(), statusDeleted); n != 1 {
			t.Errorf("the refused deletion left %d live row(s) of the account, want it in service", n)
		}
		if n := h.count(tableIdentifiers, "account_id = ?", id.String()); n == 0 {
			t.Errorf("the refused deletion erased the account's identifiers, want them kept")
		}
		if n := h.count("provisioning_probe", ""); n != 0 {
			t.Errorf("the seam's write is %d row(s) after the rollback, want none", n)
		}
	})
}

// The unverified-account sweep provisions the deletion inside the transaction
// it deletes the account in.
func TestTheUnverifiedSweepProvisionsTheDeletionInsideItsTransaction(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		h.openSelfRegistration()
		idle := h.mustRegister("idle", "idle@example.com", "password123")

		seam := provisioningtest.New()
		seam.OnDelete = func(_ context.Context, tx *gorm.DB, facts provisioning.AccountFacts) error {
			requireInTransaction(t, tx)
			if n := countIn(t, tx, tableAccounts, "id = ? AND status = ?", facts.ID, statusDeleted); n != 1 {
				t.Errorf("the swept account is not a tombstone when the seam runs")
			}
			return nil
		}
		h.p.provisioning = seam

		h.clock.advance(defaults.UnverifiedTTL + time.Hour)
		deleted, err := h.p.sweepUnverified(t.Context())
		if err != nil {
			t.Fatalf("sweeping: %v", err)
		}
		if deleted != 1 {
			t.Errorf("the sweep deleted %d account(s), want the idle registration", deleted)
		}
		calls := seam.Deletions()
		if len(calls) != 1 || calls[0].ID != idle.ID.String() || calls[0].Username != "idle" {
			t.Fatalf("the sweep handed the deletion seam %+v, want the idle registration's facts", calls)
		}
	})
}

// The work a provisioning implementation hands to the after-commit collector
// runs once the account's transaction has committed, so it reads the committed
// account and not an uncommitted one.
func TestTheProvisioningAfterCommitWorkRunsAfterTheCommit(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		seam := provisioningtest.New()
		ran := false
		seam.OnCreate = func(ctx context.Context, _ *gorm.DB, facts provisioning.AccountFacts) error {
			after := provisioning.AfterCommitOf(ctx)
			if after == nil {
				t.Errorf("the seam was handed a context with no after-commit collector")
				return nil
			}
			after.Add(func(context.Context) error {
				ran = true
				if n := h.count(tableAccounts, "id = ?", facts.ID); n != 1 {
					t.Errorf("the after-commit work reads no committed account")
				}
				return nil
			})
			return nil
		}
		h.p.provisioning = seam

		h.user("alice", "")
		if !ran {
			t.Error("the after-commit work did not run once the creation committed")
		}
	})
}

// The provisioning seam is a required dependency: a registry that names the
// account module without a provider does not start.
func TestTheAccountModuleDoesNotStartWithoutAProvisioningSeam(t *testing.T) {
	reader := authntest.ConfigReader(t, Module(), `{}`)
	reg := core.New()
	reg.Register(config.Module())
	reg.Register(standInDBModule(stubDatabase{dialect: db.SQLite}))
	reg.Register(Module())
	reg.Supply(reader, config.ModuleName)
	err := reg.Drive(t.Context(), core.Drive{
		Depth:        core.StageInit,
		Participants: []string{ModuleName, standInDBName},
	})
	if err == nil {
		t.Fatal("a registry without a provisioning provider started the account module")
	}
	if !errors.Is(err, core.ErrMissingProvider) && !strings.Contains(fmt.Sprintf("%v", err), "AccountProvisioning") {
		t.Errorf("the startup failed with %v, want the missing provisioning provider", err)
	}
}

// A status change moves the account's own state and calls the lifecycle hooks;
// it does not run the provisioning seam. What a status change leaves in place is
// the data the account owns for the whole of its life, and re-writing it would
// be a second, unrelated decision.
func TestSetStatusDoesNotCallTheProvisioningSeam(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		seam := provisioningtest.New()
		h.p.provisioning = seam
		id := h.user("alice", "password123")
		if calls := seam.Creates(); len(calls) != 1 {
			t.Fatalf("the creation called the seam %d time(s), want once", len(calls))
		}

		if err := h.p.SetStatus(t.Context(), id, authn.StatusDisabled, authn.ReasonAdmin); err != nil {
			t.Fatalf("disabling: %v", err)
		}
		if err := h.p.SetStatus(t.Context(), id, authn.StatusActive, authn.ReasonAdmin); err != nil {
			t.Fatalf("enabling: %v", err)
		}
		if calls := seam.Creates(); len(calls) != 1 {
			t.Errorf("the status changes called the creation seam; it was called %d time(s) in all", len(calls))
		}
		if calls := seam.Deletions(); len(calls) != 0 {
			t.Errorf("the status changes called the deletion seam %d time(s), want none", len(calls))
		}
	})
}

// A service account has no username and owns no organization. The seam is still
// reached on its creation, and the facts say what the account is: kind service
// and an empty username, so an implementation writes nothing for it.
func TestAServiceAccountReachesTheSeamWithoutAUsername(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		seam := provisioningtest.New()
		h.p.provisioning = seam

		created, err := h.p.Create(t.Context(), authn.CreateAccount{Kind: authn.KindService, Via: "admin"})
		if err != nil {
			t.Fatalf("creating a service account: %v", err)
		}
		calls := seam.Creates()
		if len(calls) != 1 {
			t.Fatalf("the seam was called %d time(s), want once", len(calls))
		}
		want := provisioning.AccountFacts{
			ID: created.ID.String(), Kind: string(authn.KindService), Status: "active",
		}
		if calls[0] != want {
			t.Errorf("the seam was handed %+v, want %+v", calls[0], want)
		}
	})
}
