package account

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/db"
)

func checkID(id authn.AccountID) error {
	if _, err := authn.ParseAccountID(id.String()); err != nil {
		return err
	}
	return nil
}

func loadAccount(tx *gorm.DB, id authn.AccountID) (accountRow, error) {
	if err := checkID(id); err != nil {
		return accountRow{}, err
	}
	var row accountRow
	err := tx.Where("id = ?", id.String()).Take(&row).Error
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		return accountRow{}, fmt.Errorf("%w", authn.ErrAccountNotFound)
	case err != nil:
		return accountRow{}, err
	case row.Status == statusDeleted:
		return accountRow{}, fmt.Errorf("%w", authn.ErrAccountNotFound)
	}
	return row, nil
}

func passwordSet(tx *gorm.DB, id string) (bool, error) {
	var n int64
	err := tx.Raw("SELECT COUNT(*) FROM "+tableCredentials+" WHERE account_id = ?", id).Scan(&n).Error
	return n > 0, err
}

func decodeAttributes(text string) (map[string]string, error) {
	if text == "" || text == "{}" {
		return nil, nil
	}
	var attrs map[string]string
	if err := json.Unmarshal([]byte(text), &attrs); err != nil {
		return nil, fmt.Errorf("the attribute column does not hold a set of strings: %w", err)
	}
	if len(attrs) == 0 {
		return nil, nil
	}
	return attrs, nil
}

func encodeAttributes(attrs map[string]string) (string, error) {
	if len(attrs) == 0 {
		return "{}", nil
	}
	var b strings.Builder
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(attrs); err != nil {
		return "", err
	}
	return strings.TrimSuffix(b.String(), "\n"), nil
}

func toAccount(row accountRow, hasPassword bool) (authn.Account, error) {
	attrs, err := decodeAttributes(row.Attributes)
	if err != nil {
		return authn.Account{}, err
	}
	return authn.Account{
		ID:          authn.AccountID(row.ID),
		Kind:        authn.AccountKind(row.Kind),
		Status:      statusOf(row.Status),
		DisplayName: row.DisplayName,
		MFARequired: row.MFARequired,
		Protected:   row.Protected,
		PasswordSet: hasPassword,
		Attributes:  attrs,
		ValidAfter:  utc(row.ValidAfter),
		CreatedAt:   utc(row.CreatedAt),
	}, nil
}

func (p *product) Get(ctx context.Context, id authn.AccountID) (authn.Account, error) {
	conn := p.conn(ctx)
	row, err := loadAccount(conn, id)
	if err != nil {
		return authn.Account{}, failed("reading an account", err)
	}
	has, err := passwordSet(conn, row.ID)
	if err != nil {
		return authn.Account{}, failed("reading an account's credential", err)
	}
	account, err := toAccount(row, has)
	if err != nil {
		return authn.Account{}, failed("reading an account", err)
	}
	return account, nil
}

func (p *product) Lookup(ctx context.Context, kind authn.IdentifierKind, value string) (authn.AccountID, error) {
	normalized, err := NormalizeIdentifier(kind, value)
	if err != nil {
		return "", err
	}
	var ids []string
	err = p.conn(ctx).Raw("SELECT account_id FROM "+tableIdentifiers+" WHERE kind = ? AND value = ?",
		string(kind), normalized).Scan(&ids).Error
	if err != nil {
		return "", failed("looking an identifier up", err)
	}
	if len(ids) == 0 {
		return "", fmt.Errorf("%w", authn.ErrAccountNotFound)
	}
	return authn.AccountID(ids[0]), nil
}

func (p *product) ByExternal(ctx context.Context, provider, subject string) (authn.AccountID, error) {
	var ids []string
	err := p.conn(ctx).Raw("SELECT account_id FROM "+tableExternals+" WHERE provider = ? AND subject = ?",
		provider, subject).Scan(&ids).Error
	if err != nil {
		return "", failed("looking an external identity up", err)
	}
	if len(ids) == 0 {
		return "", fmt.Errorf("%w", authn.ErrAccountNotFound)
	}
	return authn.AccountID(ids[0]), nil
}

func identifierOf(r identifierRow) authn.Identifier {
	return authn.Identifier{
		ID:             r.ID,
		Kind:           authn.IdentifierKind(r.Kind),
		Value:          r.Value,
		VerifiedAt:     utc(r.VerifiedAt),
		VerifiedVia:    r.VerifiedVia,
		SignIn:         r.SignIn,
		Contact:        r.Contact,
		DefaultContact: r.DefaultContact,
	}
}

func (p *product) Identifiers(ctx context.Context, id authn.AccountID) ([]authn.Identifier, error) {
	conn := p.conn(ctx)
	if _, err := loadAccount(conn, id); err != nil {
		return nil, failed("reading an account", err)
	}
	var rows []identifierRow
	err := conn.Where("account_id = ?", id.String()).Order("created_at, id").Find(&rows).Error
	if err != nil {
		return nil, failed("listing identifiers", err)
	}
	out := make([]authn.Identifier, 0, len(rows))
	for _, r := range rows {
		out = append(out, identifierOf(r))
	}
	return out, nil
}

func (p *product) IdentifiersMany(ctx context.Context, ids []authn.AccountID) (map[authn.AccountID][]authn.Identifier, error) {
	out := make(map[authn.AccountID][]authn.Identifier, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	wanted := make([]string, 0, len(ids))
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		if err := checkID(id); err != nil {
			return nil, err
		}
		if !seen[id.String()] {
			seen[id.String()] = true
			wanted = append(wanted, id.String())
		}
	}
	conn := p.conn(ctx)
	var live []string
	err := conn.Model(&accountRow{}).Where("id IN ?", wanted).Where("status <> ?", statusDeleted).
		Pluck("id", &live).Error
	if err != nil {
		return nil, failed("reading accounts", err)
	}
	for _, id := range live {
		out[authn.AccountID(id)] = []authn.Identifier{}
	}
	var rows []identifierRow
	if err = conn.Where("account_id IN ?", wanted).Order("account_id, created_at, id").Find(&rows).Error; err != nil {
		return nil, failed("listing identifiers", err)
	}
	for _, r := range rows {
		id := authn.AccountID(r.AccountID)
		if _, ok := out[id]; !ok {
			continue
		}
		out[id] = append(out[id], identifierOf(r))
	}
	return out, nil
}

func (p *product) Externals(ctx context.Context, id authn.AccountID) ([]authn.ExternalBinding, error) {
	conn := p.conn(ctx)
	if _, err := loadAccount(conn, id); err != nil {
		return nil, failed("reading an account", err)
	}
	var rows []externalRow
	if err := conn.Where("account_id = ?", id.String()).Order("created_at, id").Find(&rows).Error; err != nil {
		return nil, failed("listing external identities", err)
	}
	out := make([]authn.ExternalBinding, 0, len(rows))
	for _, r := range rows {
		out = append(out, authn.ExternalBinding{
			ID:            r.ID,
			Provider:      r.Provider,
			Subject:       r.Subject,
			Email:         r.Email,
			EmailVerified: r.EmailVerified,
			CreatedAt:     utc(r.CreatedAt),
			LastLoginAt:   timeOf(r.LastLoginAt),
		})
	}
	return out, nil
}

func (p *product) ListServiceSecrets(ctx context.Context, id authn.AccountID) ([]authn.ServiceSecretInfo, error) {
	conn := p.conn(ctx)
	if _, err := loadAccount(conn, id); err != nil {
		return nil, failed("reading an account", err)
	}
	var rows []secretRow
	if err := conn.Where("account_id = ?", id.String()).Order("created_at, id").Find(&rows).Error; err != nil {
		return nil, failed("listing service secrets", err)
	}
	out := make([]authn.ServiceSecretInfo, 0, len(rows))
	for _, r := range rows {
		out = append(out, authn.ServiceSecretInfo{
			ID:         r.ID,
			Label:      r.Label,
			CreatedAt:  utc(r.CreatedAt),
			ExpiresAt:  timeOf(r.ExpiresAt),
			RevokedAt:  timeOf(r.RevokedAt),
			LastUsedAt: timeOf(r.LastUsedAt),
		})
	}
	return out, nil
}

func (p *product) List(ctx context.Context, q authn.AccountQuery) ([]authn.Account, string, error) {
	limit := q.EffectiveLimit()
	tx := p.conn(ctx).Model(&accountRow{})

	switch {
	case q.Status != nil && !q.Status.Valid():
		return nil, "", fmt.Errorf("%w: the status filter is not one of the three", authn.ErrInvalidArgument)
	case q.Status != nil:
		tx = tx.Where("status = ?", int16(*q.Status))
	default:
		tx = tx.Where("status <> ?", statusDeleted)
	}
	if q.Kind != "" {
		if !q.Kind.Valid() {
			return nil, "", fmt.Errorf("%w: the kind filter is not user or service", authn.ErrInvalidArgument)
		}
		tx = tx.Where("kind = ?", string(q.Kind))
	}
	if (q.IdentifierKind == "") != (q.IdentifierPrefix == "") {
		return nil, "", fmt.Errorf("%w: an identifier filter needs both the kind and the prefix", authn.ErrInvalidArgument)
	}
	if q.IdentifierPrefix != "" {
		prefix, err := normalizePrefix(q.IdentifierKind, q.IdentifierPrefix)
		if err != nil {
			return nil, "", err
		}
		tx = tx.Where("id IN (SELECT account_id FROM "+tableIdentifiers+" WHERE kind = ? AND value >= ? AND value < ?)",
			string(q.IdentifierKind), prefix, prefixBound(prefix))
	}
	if q.DisplayNamePrefix != "" {
		if !utf8.ValidString(q.DisplayNamePrefix) {
			return nil, "", nil
		}
		if bound, bounded := displayNameBound(q.DisplayNamePrefix); bounded {
			tx = tx.Where(p.displayNameRange(), q.DisplayNamePrefix, bound)
		} else {
			tx = tx.Where(p.displayNameLowerRange(), q.DisplayNamePrefix)
		}
	}
	if q.After != "" {
		tx = tx.Where("id > ?", q.After)
	}

	var rows []accountRow
	if err := tx.Order("id").Limit(limit + 1).Find(&rows).Error; err != nil {
		return nil, "", failed("listing accounts", err)
	}
	next := ""
	if len(rows) > limit {
		rows = rows[:limit]
		next = rows[limit-1].ID
	}
	out := make([]authn.Account, 0, len(rows))
	for _, r := range rows {
		a, err := toAccount(r, false)
		if err != nil {
			return nil, "", failed("listing accounts", err)
		}
		out = append(out, a)
	}
	if err := p.markPasswords(ctx, out); err != nil {
		return nil, "", err
	}
	return out, next, nil
}

func (p *product) displayNameRange() string {
	if p.dialect == db.Postgres {
		return `display_name COLLATE "C" >= ? AND display_name COLLATE "C" < ?`
	}
	return "display_name >= ? AND display_name < ?"
}

func (p *product) displayNameLowerRange() string {
	if p.dialect == db.Postgres {
		return `display_name COLLATE "C" >= ?`
	}
	return "display_name >= ?"
}

// displayNameBound returns the least string above every stored display name
// beginning with prefix, and whether such a bound exists. It skips the surrogate
// block when raising, so every returned bound is valid UTF-8.
func displayNameBound(prefix string) (string, bool) {
	if !utf8.ValidString(prefix) {
		return "", true
	}
	for prefix != "" {
		last, size := utf8.DecodeLastRuneInString(prefix)
		if last == utf8.MaxRune {
			prefix = prefix[:len(prefix)-size]
			continue
		}
		next := last + 1
		if next >= 0xD800 && next <= 0xDFFF {
			next = 0xE000
		}
		var raised [utf8.UTFMax]byte
		n := utf8.EncodeRune(raised[:], next)
		return prefix[:len(prefix)-size] + string(raised[:n]), true
	}
	return "", false
}

func (p *product) markPasswords(ctx context.Context, accounts []authn.Account) error {
	if len(accounts) == 0 {
		return nil
	}
	ids := make([]string, len(accounts))
	for i, a := range accounts {
		ids[i] = a.ID.String()
	}
	var have []string
	err := p.conn(ctx).Raw("SELECT account_id FROM "+tableCredentials+" WHERE account_id IN ?", ids).Scan(&have).Error
	if err != nil {
		return failed("reading the credentials of a page", err)
	}
	set := make(map[string]struct{}, len(have))
	for _, id := range have {
		set[id] = struct{}{}
	}
	for i := range accounts {
		_, accounts[i].PasswordSet = set[accounts[i].ID.String()]
	}
	return nil
}
