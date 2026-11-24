package webauthn

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"strings"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/authn"
)

const (
	userTable          = "authn_webauthn_users"
	authenticatorTable = "authn_webauthn_credentials"
)

const handleBytes = 32

const authenticatorColumns = "factor_id, rp_id, credential_id, public_key, aaguid, attestation_type, " +
	"transports, sign_count, clone_warning, user_verified, backup_eligible, backup_state, passkey"

type credentialRow struct {
	FactorID        string
	RPID            string
	CredentialID    string
	PublicKey       string
	AAGUID          string
	AttestationType string
	Transports      string
	SignCount       int64
	CloneWarning    bool
	UserVerified    bool
	BackupEligible  bool
	BackupState     bool
	Passkey         bool
}

func (r credentialRow) transportList() []string {
	if r.Transports == "" {
		return nil
	}
	return strings.Split(r.Transports, ",")
}

func joinTransports(names []string) string { return strings.Join(names, ",") }

type register struct {
	handle *gorm.DB
}

func (g *register) session(ctx context.Context) *gorm.DB { return g.handle.WithContext(ctx) }

func (g *register) handleOf(ctx context.Context, account authn.AccountID) (handle string, found bool, err error) {
	var rows []struct{ Handle string }
	if err := g.session(ctx).Table(userTable).Select("handle").
		Where("account_id = ?", string(account)).Limit(1).Scan(&rows).Error; err != nil {
		return "", false, fmt.Errorf("reading the user handle: %w", err)
	}
	if len(rows) == 0 {
		return "", false, nil
	}
	return rows[0].Handle, true, nil
}

func (g *register) ensureHandle(ctx context.Context, account authn.AccountID) (string, error) {
	if handle, found, err := g.handleOf(ctx, account); err != nil {
		return "", err
	} else if found {
		return handle, nil
	}
	handle, err := newHandle()
	if err != nil {
		return "", err
	}
	res := g.session(ctx).Exec("INSERT INTO "+userTable+" (account_id, handle) VALUES (?, ?)", string(account), handle)
	if res.Error == nil && res.RowsAffected == 1 {
		return handle, nil
	}
	// A lost insert race reads the row back; any other failure is returned.
	if got, found, rerr := g.handleOf(ctx, account); rerr == nil && found {
		return got, nil
	}
	if res.Error != nil {
		return "", fmt.Errorf("writing the user handle: %w", res.Error)
	}
	return "", fmt.Errorf("writing the user handle: the row was neither inserted nor there")
}

func (g *register) accountForHandle(ctx context.Context, handle string) (authn.AccountID, bool, error) {
	var rows []struct{ AccountID string }
	if err := g.session(ctx).Table(userTable).Select("account_id").
		Where("handle = ?", handle).Limit(1).Scan(&rows).Error; err != nil {
		return "", false, fmt.Errorf("reading the account of a user handle: %w", err)
	}
	if len(rows) == 0 {
		return "", false, nil
	}
	return authn.AccountID(rows[0].AccountID), true, nil
}

func (g *register) credentialsFor(ctx context.Context, factorIDs []string, rpID string) ([]credentialRow, error) {
	if len(factorIDs) == 0 {
		return nil, nil
	}
	var rows []credentialRow
	err := g.session(ctx).Table(authenticatorTable).Select(authenticatorColumns).
		Where("factor_id IN ? AND rp_id = ?", factorIDs, rpID).Order("factor_id").Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("reading the credentials of an account: %w", err)
	}
	return rows, nil
}

func (g *register) getCredential(ctx context.Context, factorID string) (credentialRow, bool, error) {
	var rows []credentialRow
	err := g.session(ctx).Table(authenticatorTable).Select(authenticatorColumns).
		Where("factor_id = ?", factorID).Limit(1).Scan(&rows).Error
	if err != nil {
		return credentialRow{}, false, fmt.Errorf("reading a credential: %w", err)
	}
	if len(rows) == 0 {
		return credentialRow{}, false, nil
	}
	return rows[0], true, nil
}

func (g *register) credentialByID(ctx context.Context, rpID string, credentialID []byte) (credentialRow, bool, error) {
	var rows []credentialRow
	err := g.session(ctx).Table(authenticatorTable).Select(authenticatorColumns).
		Where("rp_id = ? AND credential_id = ?", rpID, base64.RawURLEncoding.EncodeToString(credentialID)).
		Limit(1).Scan(&rows).Error
	if err != nil {
		return credentialRow{}, false, fmt.Errorf("reading a credential by its identifier: %w", err)
	}
	if len(rows) == 0 {
		return credentialRow{}, false, nil
	}
	return rows[0], true, nil
}

func (g *register) insertCredential(ctx context.Context, r credentialRow) error {
	err := g.session(ctx).Exec("INSERT INTO "+authenticatorTable+
		" (factor_id, rp_id, credential_id, public_key, aaguid, attestation_type, transports, "+
		"sign_count, clone_warning, user_verified, backup_eligible, backup_state, passkey) "+
		"VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		r.FactorID, r.RPID, r.CredentialID, r.PublicKey, r.AAGUID, r.AttestationType, r.Transports,
		r.SignCount, r.CloneWarning, r.UserVerified, r.BackupEligible, r.BackupState, r.Passkey).Error
	if err != nil {
		return fmt.Errorf("writing a credential: %w", err)
	}
	return nil
}

// writeBack advances only when the stored counter is not above the presented one, so a concurrent assertion makes this a lost write-back, not an error. clone_warning is set, not assigned, so a concurrent flagClone is not cleared.
func (g *register) writeBack(ctx context.Context, factorID string, presented credentialRow) (bool, error) {
	res := g.session(ctx).Exec("UPDATE "+authenticatorTable+
		" SET sign_count = ?, clone_warning = (clone_warning OR ?), user_verified = ?, backup_state = ? "+
		"WHERE factor_id = ? AND sign_count <= ?",
		presented.SignCount, presented.CloneWarning, presented.UserVerified, presented.BackupState,
		factorID, presented.SignCount)
	if res.Error != nil {
		return false, fmt.Errorf("writing back an assertion: %w", res.Error)
	}
	return res.RowsAffected == 1, nil
}

// flagClone records a clone warning the conditional write-back does not cover, because the presented counter went backwards.
func (g *register) flagClone(ctx context.Context, factorID string) error {
	if err := g.session(ctx).Exec("UPDATE "+authenticatorTable+" SET clone_warning = ? WHERE factor_id = ?",
		true, factorID).Error; err != nil {
		return fmt.Errorf("flagging a clone warning: %w", err)
	}
	return nil
}

func (g *register) deleteCredential(ctx context.Context, factorID string) (bool, error) {
	res := g.session(ctx).Exec("DELETE FROM "+authenticatorTable+" WHERE factor_id = ?", factorID)
	if res.Error != nil {
		return false, fmt.Errorf("deleting a credential: %w", res.Error)
	}
	return res.RowsAffected == 1, nil
}

func (g *register) anyCredential(ctx context.Context, factorIDs []string) (bool, error) {
	if len(factorIDs) == 0 {
		return false, nil
	}
	var n int64
	err := g.session(ctx).Table(authenticatorTable).Where("factor_id IN ?", factorIDs).Limit(1).Count(&n).Error
	if err != nil {
		return false, fmt.Errorf("counting the credentials of an account: %w", err)
	}
	return n > 0, nil
}

func (g *register) dropHandle(ctx context.Context, account authn.AccountID) error {
	if err := g.session(ctx).Exec("DELETE FROM "+userTable+" WHERE account_id = ?", string(account)).Error; err != nil {
		return fmt.Errorf("deleting the user handle: %w", err)
	}
	return nil
}

func (g *register) probe(ctx context.Context) error {
	for _, table := range []string{userTable, authenticatorTable} {
		err := g.session(ctx).Exec("SELECT 1 FROM " + table + " WHERE 1 = 0").Error
		if err == nil {
			continue
		}
		if !g.session(ctx).Migrator().HasTable(table) {
			return fmt.Errorf("%w: %s is not there: %w", authn.ErrNotMigrated, table, err)
		}
		return fmt.Errorf("reading %s: %w", table, err)
	}
	return nil
}

func newHandle() (string, error) {
	b := make([]byte, handleBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("drawing a user handle: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
