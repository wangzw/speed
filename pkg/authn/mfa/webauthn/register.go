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

// The two tables this module owns.
const (
	userTable          = "authn_webauthn_users"
	authenticatorTable = "authn_webauthn_credentials"
)

// handleBytes is how many random bytes a user handle is. It is the whole of the
// handle: it carries no name, no identifier and nothing else about the person,
// which is why a client-side discoverable credential may hand it back in the
// clear. Base64url of 32 bytes is 43 characters, inside the 64-byte bound the
// protocol places on a user handle.
const handleBytes = 32

// authenticatorColumns are the columns of the credential table as a read names
// them.
const authenticatorColumns = "factor_id, rp_id, credential_id, public_key, aaguid, attestation_type, " +
	"transports, sign_count, clone_warning, user_verified, backup_eligible, backup_state, passkey"

// credentialRow is one row of the credential table as a read returns it.
type credentialRow struct {
	FactorID        string
	RPID            string
	CredentialID    string // base64url
	PublicKey       string // base64url, a COSE key
	AAGUID          string // base64url
	AttestationType string
	Transports      string // the transport names, comma-separated
	SignCount       int64
	CloneWarning    bool
	UserVerified    bool
	BackupEligible  bool
	BackupState     bool
	Passkey         bool
}

// transportList splits the stored, comma-separated transport names.
func (r credentialRow) transportList() []string {
	if r.Transports == "" {
		return nil
	}
	return strings.Split(r.Transports, ",")
}

// joinTransports stores a list of transport names as one column.
func joinTransports(names []string) string { return strings.Join(names, ",") }

// register is this module's statements on its two tables and nothing else. It
// decides nothing; the factor and the login method do. The factor ids a read
// takes are the account's, read through the multi-factor module's seam and never
// from its table.
type register struct {
	handle *gorm.DB
}

func (g *register) session(ctx context.Context) *gorm.DB { return g.handle.WithContext(ctx) }

// handleOf reads the account's user handle. found is false when the account has
// none yet, which is the case before its first registration.
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

// ensureHandle returns the account's user handle, creating one when the account
// has none. The insert is conditional on the row not being there, so two
// registrations racing produce one handle; a row another writer made between the
// read and the insert is read back.
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
	// Either the insert lost a race — the row is there now — or it failed for
	// another reason. A read that finds a row answers the call; anything else is
	// the failure that came back.
	if got, found, rerr := g.handleOf(ctx, account); rerr == nil && found {
		return got, nil
	}
	if res.Error != nil {
		return "", fmt.Errorf("writing the user handle: %w", res.Error)
	}
	return "", fmt.Errorf("writing the user handle: the row was neither inserted nor there")
}

// accountForHandle reads the account a user handle belongs to. found is false
// for a handle nobody holds, which the caller answers as an unknown credential.
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

// credentialsFor reads the credentials of the listed factor rows that were
// registered under rpID, ordered by factor id. The library compares an assertion
// against these, so one bound to another relying party id is not handed to it.
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

// getCredential reads the credential a factor row keys. found is false when the
// factor has no credential row, which the factor reads as one not enrolled.
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

// credentialByID reads the credential registered under rpID with this credential
// id, which is how a discoverable assertion — one that carries no account — is
// resolved.
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

// insertCredential writes a credential row.
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

// writeBack advances a credential's counter and flags after an assertion that
// held. The statement is conditional on the stored counter being at or below the
// one presented, so it wins only when the presented count has not been seen: a
// concurrent assertion that advanced the counter first makes this one lose, and
// the caller reads the zero affected rows as a replay. A false result is that
// loss and not an error.
func (g *register) writeBack(ctx context.Context, factorID string, presented credentialRow) (bool, error) {
	res := g.session(ctx).Exec("UPDATE "+authenticatorTable+
		" SET sign_count = ?, clone_warning = ?, user_verified = ?, backup_state = ? "+
		"WHERE factor_id = ? AND sign_count <= ?",
		presented.SignCount, presented.CloneWarning, presented.UserVerified, presented.BackupState,
		factorID, presented.SignCount)
	if res.Error != nil {
		return false, fmt.Errorf("writing back an assertion: %w", res.Error)
	}
	return res.RowsAffected == 1, nil
}

// flagClone records that the credential may be cloned. It is written when the
// library reports the presented counter did not advance, which is the case the
// conditional write-back does not cover: the count went backwards, so the row is
// marked rather than advanced.
func (g *register) flagClone(ctx context.Context, factorID string) error {
	if err := g.session(ctx).Exec("UPDATE "+authenticatorTable+" SET clone_warning = ? WHERE factor_id = ?",
		true, factorID).Error; err != nil {
		return fmt.Errorf("flagging a clone warning: %w", err)
	}
	return nil
}

// deleteCredential removes a factor's credential row and says whether there was
// one.
func (g *register) deleteCredential(ctx context.Context, factorID string) (bool, error) {
	res := g.session(ctx).Exec("DELETE FROM "+authenticatorTable+" WHERE factor_id = ?", factorID)
	if res.Error != nil {
		return false, fmt.Errorf("deleting a credential: %w", res.Error)
	}
	return res.RowsAffected == 1, nil
}

// anyCredential reports whether any of the listed factor rows still has a
// credential. The account's handle row is dropped with the last one, because a
// handle only has a purpose while a credential can hand it back.
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

// dropHandle removes the account's handle row, which is idempotent.
func (g *register) dropHandle(ctx context.Context, account authn.AccountID) error {
	if err := g.session(ctx).Exec("DELETE FROM "+userTable+" WHERE account_id = ?", string(account)).Error; err != nil {
		return fmt.Errorf("deleting the user handle: %w", err)
	}
	return nil
}

// probe reports whether this module's tables can be read. An error that is not
// ErrNotMigrated says the table is there and the read failed.
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

// newHandle draws a random user handle in base64url.
func newHandle() (string, error) {
	b := make([]byte, handleBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("drawing a user handle: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
