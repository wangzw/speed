package sso

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/clock"
	"github.com/vislake/speed/pkg/authn/internal/model"
	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/event"
)

// sweepBatch is how many rows one statement of the sweep removes.
const sweepBatch = 1000

// duplicated reports whether a failure is a uniqueness violation, which GORM
// classifies the same on both engines.
func duplicated(err error) bool { return errors.Is(err, gorm.ErrDuplicatedKey) }

// probePlaintext is the value the startup round trip writes into the encrypted
// column and reads back. It is no deployment's secret: it exists only so that a
// write and a read that cannot be sealed or opened fail the startup rather than
// the first OIDC provider created.
const probePlaintext = "speed/authn/sso/encrypted-column-probe"

// idpRow is one row of authn_idps as the model path sees it. ClientSecret carries
// the serializer, so a write seals it and a read that selects it opens it. Every
// read this module hands out selects the other columns and leaves this nil; only
// Secret selects it.
type idpRow struct {
	ID                string    `gorm:"column:id;primaryKey"`
	TenantID          string    `gorm:"column:tenant_id"`
	Protocol          string    `gorm:"column:protocol"`
	Name              string    `gorm:"column:name"`
	Status            int       `gorm:"column:status"`
	Provisioning      string    `gorm:"column:provisioning"`
	UsernameStrategy  string    `gorm:"column:username_strategy"`
	UsernameClaim     string    `gorm:"column:username_claim"`
	TrustIDPMFA       bool      `gorm:"column:trust_idp_mfa"`
	MergeExisting     bool      `gorm:"column:merge_existing"`
	AllowIDPInitiated bool      `gorm:"column:allow_idp_initiated"`
	AllowSHA1         bool      `gorm:"column:allow_sha1"`
	Config            string    `gorm:"column:config"`
	ClientSecret      *string   `gorm:"column:client_secret;serializer:encrypted"`
	Version           int       `gorm:"column:version"`
	CreatedAt         time.Time `gorm:"column:created_at"`
	UpdatedAt         time.Time `gorm:"column:updated_at"`
}

// TableName is the table this model maps to, which is the one the migration
// creates.
func (idpRow) TableName() string { return idpTable }

// providerColumns is every column of authn_idps except the client secret, which
// no read handed out selects.
const providerColumns = "id, tenant_id, protocol, name, status, provisioning, username_strategy, " +
	"username_claim, trust_idp_mfa, merge_existing, allow_idp_initiated, allow_sha1, config, version, " +
	"created_at, updated_at"

// domainRow is one row of authn_idp_domains.
type domainRow struct {
	Domain      string     `gorm:"column:domain;primaryKey"`
	IDPID       string     `gorm:"column:idp_id;primaryKey"`
	TenantID    string     `gorm:"column:tenant_id"`
	VerifyToken string     `gorm:"column:verify_token"`
	VerifiedAt  *time.Time `gorm:"column:verified_at"`
	CreatedAt   time.Time  `gorm:"column:created_at"`
}

func (domainRow) TableName() string { return domainTable }

// register is the statements on this module's three tables and nothing else. It
// decides nothing; the SSOProviders methods decide.
type register struct {
	handle  *gorm.DB
	dialect db.Dialect
}

func (g *register) session(ctx context.Context) *gorm.DB { return g.handle.WithContext(ctx) }

// toProvider maps a row to the value handed out, without the secret.
func toProvider(r idpRow) authn.SSOProvider {
	cfg := r.Config
	return authn.SSOProvider{
		ID:                r.ID,
		Tenant:            r.TenantID,
		Protocol:          r.Protocol,
		Name:              r.Name,
		Enabled:           r.Status != 0,
		Provisioning:      r.Provisioning,
		UsernameStrategy:  r.UsernameStrategy,
		UsernameClaim:     r.UsernameClaim,
		TrustIDPMFA:       r.TrustIDPMFA,
		AllowIDPInitiated: r.AllowIDPInitiated,
		AllowSHA1:         r.AllowSHA1,
		MergeExisting:     r.MergeExisting,
		Config:            []byte(cfg),
		Version:           r.Version,
	}
}

// readIDP reads one provider row without the secret.
func (g *register) readIDP(ctx context.Context, id string) (idpRow, bool, error) {
	var row idpRow
	err := g.session(ctx).Select(providerColumns).Where("id = ?", id).Take(&row).Error
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		return idpRow{}, false, nil
	case err != nil:
		return idpRow{}, false, fmt.Errorf("reading an identity provider: %w", err)
	}
	row.CreatedAt = clock.UTC(row.CreatedAt)
	row.UpdatedAt = clock.UTC(row.UpdatedAt)
	return row, true, nil
}

// readIDPWithSecret reads one provider row through the model, opening the client
// secret. It is what an update reads before it writes, so that a nil secret
// leaves the stored one untouched.
func (g *register) readIDPWithSecret(ctx context.Context, id string) (idpRow, bool, error) {
	var row idpRow
	err := g.session(ctx).Where("id = ?", id).Limit(1).Take(&row).Error
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		return idpRow{}, false, nil
	case err != nil:
		return idpRow{}, false, fmt.Errorf("reading an identity provider with its secret: %w", err)
	}
	row.CreatedAt = clock.UTC(row.CreatedAt)
	row.UpdatedAt = clock.UTC(row.UpdatedAt)
	return row, true, nil
}

// readSecret reads the client secret of a provider, opened by the serializer.
// found is false for a row that is not there; a row with no secret reads as the
// empty string.
func (g *register) readSecret(ctx context.Context, id string) (string, bool, error) {
	var row idpRow
	err := g.session(ctx).Select("id, client_secret").Where("id = ?", id).Take(&row).Error
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		return "", false, nil
	case err != nil:
		return "", false, fmt.Errorf("reading an identity provider secret: %w", err)
	}
	if row.ClientSecret == nil {
		return "", true, nil
	}
	return *row.ClientSecret, true, nil
}

// listIDPs reads every provider row of a tenant, the disabled ones included.
func (g *register) listIDPs(ctx context.Context, tenant string) ([]idpRow, error) {
	var rows []idpRow
	err := g.session(ctx).Select(providerColumns).
		Where("tenant_id = ?", tenant).Order("id ASC").Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("listing the identity providers of a tenant: %w", err)
	}
	for i := range rows {
		rows[i].CreatedAt = clock.UTC(rows[i].CreatedAt)
		rows[i].UpdatedAt = clock.UTC(rows[i].UpdatedAt)
	}
	return rows, nil
}

// discover reads the enabled providers of a verified domain.
func (g *register) discover(ctx context.Context, domain string) ([]idpRow, error) {
	var rows []idpRow
	err := g.session(ctx).Table(idpTable+" AS i").
		Select("i.id, i.tenant_id, i.protocol, i.name, i.status, i.provisioning, i.username_strategy, "+
			"i.username_claim, i.trust_idp_mfa, i.merge_existing, i.allow_idp_initiated, i.allow_sha1, "+
			"i.config, i.version, i.created_at, i.updated_at").
		Joins("JOIN "+domainTable+" AS d ON d.idp_id = i.id").
		Where("d.domain = ? AND d.verified_at IS NOT NULL AND i.status = 1", domain).
		Order("i.id ASC").Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("discovering the identity providers of a domain: %w", err)
	}
	for i := range rows {
		rows[i].CreatedAt = clock.UTC(rows[i].CreatedAt)
		rows[i].UpdatedAt = clock.UTC(rows[i].UpdatedAt)
	}
	return rows, nil
}

// insertIDP writes a new provider row through the model, so that the serializer
// seals the secret.
func (g *register) insertIDP(ctx context.Context, row idpRow) error {
	if err := g.session(ctx).Create(&row).Error; err != nil {
		return fmt.Errorf("writing an identity provider: %w", err)
	}
	return nil
}

// saveIDP writes an existing provider row through the model, sealing the secret
// and preserving the creation time. It is what an update uses.
func (g *register) saveIDP(ctx context.Context, row idpRow) error {
	if err := g.session(ctx).Save(&row).Error; err != nil {
		return fmt.Errorf("updating an identity provider: %w", err)
	}
	return nil
}

// deleteIDP deletes a provider row and its domain claims in one transaction.
func (g *register) deleteIDP(ctx context.Context, id string) error {
	return g.session(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("DELETE FROM "+domainTable+" WHERE idp_id = ?", id).Error; err != nil {
			return fmt.Errorf("deleting the domains of an identity provider: %w", err)
		}
		if err := tx.Exec("DELETE FROM "+idpTable+" WHERE id = ?", id).Error; err != nil {
			return fmt.Errorf("deleting an identity provider: %w", err)
		}
		return nil
	})
}

// domainVerifiedElsewhere reports whether the domain is verified for a provider
// other than idpID.
func (g *register) domainVerifiedElsewhere(ctx context.Context, domain, idpID string) (bool, error) {
	var n int64
	err := g.session(ctx).Table(domainTable).
		Where("domain = ? AND verified_at IS NOT NULL AND idp_id <> ?", domain, idpID).Count(&n).Error
	if err != nil {
		return false, fmt.Errorf("checking a domain claim: %w", err)
	}
	return n > 0, nil
}

// claimDomain writes or refreshes an unverified claim of a domain for a provider
// and returns the token to publish. The verification state is left as it is, so
// a re-claim of a verified domain does not withdraw it.
func (g *register) claimDomain(ctx context.Context, tenant, idpID, domain, token string, now time.Time) error {
	err := g.session(ctx).Exec("INSERT INTO "+domainTable+
		" (domain, idp_id, tenant_id, verify_token, verified_at, created_at) VALUES (?, ?, ?, ?, NULL, ?) "+
		"ON CONFLICT (domain, idp_id) DO UPDATE SET verify_token = excluded.verify_token, created_at = excluded.created_at",
		domain, idpID, tenant, token, clock.UTC(now)).Error
	if err != nil {
		return fmt.Errorf("claiming a domain: %w", err)
	}
	return nil
}

// claim reads one domain claim of a provider.
func (g *register) claim(ctx context.Context, idpID, domain string) (domainRow, bool, error) {
	var rows []domainRow
	err := g.session(ctx).Table(domainTable).
		Where("idp_id = ? AND domain = ?", idpID, domain).Limit(1).Scan(&rows).Error
	if err != nil {
		return domainRow{}, false, fmt.Errorf("reading a domain claim: %w", err)
	}
	if len(rows) == 0 {
		return domainRow{}, false, nil
	}
	return rows[0], true, nil
}

// markVerified sets the verification time of a claim. A domain another provider
// has verified makes the unique index refuse the write, which the caller reads as
// ErrDomainClaimed.
func (g *register) markVerified(ctx context.Context, idpID, domain string, now time.Time) error {
	res := g.session(ctx).Exec("UPDATE "+domainTable+" SET verified_at = ? WHERE idp_id = ? AND domain = ?",
		clock.UTC(now), idpID, domain)
	if res.Error != nil {
		return fmt.Errorf("marking a domain verified: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return errNoDomainClaim
	}
	return nil
}

// removeDomain deletes a claim, verified or not. A claim that is not there is
// not an error.
func (g *register) removeDomain(ctx context.Context, idpID, domain string) error {
	if err := g.session(ctx).Exec("DELETE FROM "+domainTable+" WHERE idp_id = ? AND domain = ?",
		idpID, domain).Error; err != nil {
		return fmt.Errorf("removing a domain: %w", err)
	}
	return nil
}

// domains reads every claim of a provider, verified or not, in ascending
// domain order.
func (g *register) domains(ctx context.Context, idpID string) ([]domainRow, error) {
	var rows []domainRow
	err := g.session(ctx).Table(domainTable).
		Where("idp_id = ?", idpID).Order("domain ASC").Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("reading the domains of an identity provider: %w", err)
	}
	return rows, nil
}

// domainsMany reads every claim of several providers in one statement. A
// provider with no claim contributes no row.
func (g *register) domainsMany(ctx context.Context, ids []string) ([]domainRow, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var rows []domainRow
	err := g.session(ctx).Table(domainTable).
		Where("idp_id IN ?", ids).Order("idp_id ASC, domain ASC").Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("reading the domains of several identity providers: %w", err)
	}
	return rows, nil
}

// verifiedDomains reads the verified claims of a provider.
func (g *register) verifiedDomains(ctx context.Context, idpID string) ([]domainRow, error) {
	var rows []domainRow
	err := g.session(ctx).Table(domainTable).
		Where("idp_id = ? AND verified_at IS NOT NULL", idpID).Order("domain ASC").Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("reading the verified domains of an identity provider: %w", err)
	}
	return rows, nil
}

// withdrawVerification clears the verification time of a claim.
func (g *register) withdrawVerification(ctx context.Context, idpID, domain string) error {
	if err := g.session(ctx).Exec("UPDATE "+domainTable+" SET verified_at = NULL WHERE idp_id = ? AND domain = ?",
		idpID, domain).Error; err != nil {
		return fmt.Errorf("withdrawing a domain verification: %w", err)
	}
	return nil
}

// removeStaleClaims removes one batch of unverified claims created before the
// cutoff and returns how many it removed.
func (g *register) removeStaleClaims(ctx context.Context, cutoff time.Time) (int64, error) {
	res := g.session(ctx).Exec("DELETE FROM "+domainTable+
		" WHERE (domain, idp_id) IN (SELECT domain, idp_id FROM "+domainTable+
		" WHERE verified_at IS NULL AND created_at < ? LIMIT ?)",
		clock.UTC(cutoff), sweepBatch)
	if res.Error != nil {
		return 0, fmt.Errorf("removing the unverified domain claims: %w", res.Error)
	}
	return res.RowsAffected, nil
}

// rememberAssertion writes an assertion's identifier on the replay line. A
// second write of the same (idp_id, assertion_id) loses to the primary key, which
// the caller reads as authn.ErrAssertionReplay. not_on_or_after is the earlier of
// the assertion's and its bearer confirmation's condition, and seen_at is now;
// both are written UTC so the sweep compares two UTC values as text.
func (g *register) rememberAssertion(ctx context.Context, idpID, assertionID string, notOnOrAfter, seenAt time.Time) error {
	err := g.session(ctx).Exec("INSERT INTO "+assertionTable+
		" (idp_id, assertion_id, not_on_or_after, seen_at) VALUES (?, ?, ?, ?)",
		idpID, assertionID, clock.UTC(notOnOrAfter), clock.UTC(seenAt)).Error
	if err != nil {
		if duplicated(err) {
			return authn.ErrAssertionReplay
		}
		return fmt.Errorf("recording a consumed assertion: %w", err)
	}
	return nil
}

// removeExpiredAssertions removes one batch of assertion identifiers past their
// condition and returns how many it removed.
func (g *register) removeExpiredAssertions(ctx context.Context, cutoff time.Time) (int64, error) {
	res := g.session(ctx).Exec("DELETE FROM "+assertionTable+
		" WHERE (idp_id, assertion_id) IN (SELECT idp_id, assertion_id FROM "+assertionTable+
		" WHERE not_on_or_after < ? LIMIT ?)",
		clock.UTC(cutoff), sweepBatch)
	if res.Error != nil {
		return 0, fmt.Errorf("removing the expired assertion identifiers: %w", res.Error)
	}
	return res.RowsAffected, nil
}

// probe reports whether the three tables can be read. A table that is not there
// is ErrNotMigrated, and any other failure is returned as it is.
func (g *register) probe(ctx context.Context) error {
	for _, table := range []string{idpTable, domainTable, assertionTable} {
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

// roundTrip writes a probe row and reads it back through the model, in a
// transaction that is rolled back, so that a handle whose encrypted column cannot
// be sealed or opened is found at startup. Every failure is the same one: the
// column could not be written and read.
func (g *register) roundTrip(ctx context.Context) error {
	tx := g.session(ctx).Begin()
	if tx.Error != nil {
		return fmt.Errorf("opening a transaction for the encrypted-column check: %w", tx.Error)
	}
	defer func() { _ = tx.Rollback() }()

	now := clock.UTC(time.Now())
	secret := probePlaintext
	probe := idpRow{
		ID:               "probe:" + event.NewID(),
		TenantID:         "probe",
		Protocol:         model.SSOProtocolOIDC,
		Name:             "probe",
		Provisioning:     model.ProvisioningJIT,
		UsernameStrategy: model.UsernameStrategyUserConfirm,
		Config:           "{}",
		ClientSecret:     &secret,
		Version:          1,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	if err := tx.Create(&probe).Error; err != nil {
		return fmt.Errorf("writing the encrypted-column check row: %w", err)
	}
	var back idpRow
	if err := tx.Where("id = ?", probe.ID).Take(&back).Error; err != nil {
		return fmt.Errorf("reading the encrypted-column check row back: %w", err)
	}
	if back.ClientSecret == nil || *back.ClientSecret != probePlaintext {
		return errors.New("the encrypted column did not read back what was written into it")
	}
	return nil
}
