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

const sweepBatch = 1000

func duplicated(err error) bool { return errors.Is(err, gorm.ErrDuplicatedKey) }

const probePlaintext = "speed/authn/sso/encrypted-column-probe"

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

func (idpRow) TableName() string { return idpTable }

const providerColumns = "id, tenant_id, protocol, name, status, provisioning, username_strategy, " +
	"username_claim, trust_idp_mfa, merge_existing, allow_idp_initiated, allow_sha1, config, version, " +
	"created_at, updated_at"

type domainRow struct {
	Domain      string     `gorm:"column:domain;primaryKey"`
	IDPID       string     `gorm:"column:idp_id;primaryKey"`
	TenantID    string     `gorm:"column:tenant_id"`
	VerifyToken string     `gorm:"column:verify_token"`
	VerifiedAt  *time.Time `gorm:"column:verified_at"`
	CreatedAt   time.Time  `gorm:"column:created_at"`
}

func (domainRow) TableName() string { return domainTable }

type register struct {
	handle  *gorm.DB
	dialect db.Dialect
}

func (g *register) session(ctx context.Context) *gorm.DB { return g.handle.WithContext(ctx) }

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

func (g *register) insertIDP(ctx context.Context, row idpRow) error {
	if err := g.session(ctx).Create(&row).Error; err != nil {
		return fmt.Errorf("writing an identity provider: %w", err)
	}
	return nil
}

func (g *register) saveIDP(ctx context.Context, row idpRow) error {
	if err := g.session(ctx).Save(&row).Error; err != nil {
		return fmt.Errorf("updating an identity provider: %w", err)
	}
	return nil
}

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

func (g *register) domainVerifiedElsewhere(ctx context.Context, domain, idpID string) (bool, error) {
	var n int64
	err := g.session(ctx).Table(domainTable).
		Where("domain = ? AND verified_at IS NOT NULL AND idp_id <> ?", domain, idpID).Count(&n).Error
	if err != nil {
		return false, fmt.Errorf("checking a domain claim: %w", err)
	}
	return n > 0, nil
}

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

func (g *register) removeDomain(ctx context.Context, idpID, domain string) error {
	if err := g.session(ctx).Exec("DELETE FROM "+domainTable+" WHERE idp_id = ? AND domain = ?",
		idpID, domain).Error; err != nil {
		return fmt.Errorf("removing a domain: %w", err)
	}
	return nil
}

func (g *register) domains(ctx context.Context, idpID string) ([]domainRow, error) {
	var rows []domainRow
	err := g.session(ctx).Table(domainTable).
		Where("idp_id = ?", idpID).Order("domain ASC").Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("reading the domains of an identity provider: %w", err)
	}
	return rows, nil
}

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

func (g *register) verifiedDomains(ctx context.Context, idpID string) ([]domainRow, error) {
	var rows []domainRow
	err := g.session(ctx).Table(domainTable).
		Where("idp_id = ? AND verified_at IS NOT NULL", idpID).Order("domain ASC").Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("reading the verified domains of an identity provider: %w", err)
	}
	return rows, nil
}

func (g *register) withdrawVerification(ctx context.Context, idpID, domain string) error {
	if err := g.session(ctx).Exec("UPDATE "+domainTable+" SET verified_at = NULL WHERE idp_id = ? AND domain = ?",
		idpID, domain).Error; err != nil {
		return fmt.Errorf("withdrawing a domain verification: %w", err)
	}
	return nil
}

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
