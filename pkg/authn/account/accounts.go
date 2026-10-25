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

// checkID refuses an identifier that cannot name an account.
func checkID(id authn.AccountID) error {
	if _, err := authn.ParseAccountID(id.String()); err != nil {
		return err
	}
	return nil
}

// loadAccount reads one account row. An account that does not exist, and a
// tombstone, are authn.ErrAccountNotFound.
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

// passwordSet reports whether the account has a password credential.
func passwordSet(tx *gorm.DB, id string) (bool, error) {
	var n int64
	err := tx.Raw("SELECT COUNT(*) FROM "+tableCredentials+" WHERE account_id = ?", id).Scan(&n).Error
	return n > 0, err
}

// decodeAttributes reads the attribute column. A set that is empty is nil.
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

// encodeAttributes writes an attribute set as the column holds it. The keys come
// out in order, and a character such as < is not escaped.
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

// toAccount is the account a row stands for.
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

// Get reads one account. A deleted account is authn.ErrAccountNotFound, as is
// one that never existed, and so is a subject of the static credential table,
// which no row stands for.
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

// Lookup finds the account an identifier belongs to.
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

// ByExternal finds the account an external identity is bound to. The key is the
// provider and the subject, never an email address.
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

// identifierOf is the identifier one row stands for. The single read and the
// batch read both build their answers through it, so the two cannot disagree
// about what an identifier carries.
func identifierOf(r identifierRow) authn.Identifier {
	return authn.Identifier{
		ID:          r.ID,
		Kind:        authn.IdentifierKind(r.Kind),
		Value:       r.Value,
		VerifiedAt:  utc(r.VerifiedAt),
		VerifiedVia: r.VerifiedVia,
	}
}

// Identifiers lists the identifiers bound to an account, the username first and
// then the rest in the order they were bound.
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

// IdentifiersMany lists the identifiers of several accounts in one read. The
// answer has an entry for each id that names a live account, holding what
// Identifiers returns for it, in the same order and with the same fields. An
// unknown account and a tombstone have no entry, the same absence the single
// read reports as authn.ErrAccountNotFound: a page that listed one keeps
// answering instead of failing as a whole. An empty input is an empty map and no
// statement.
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
	// The live accounts among the requested ids. A tombstone is left out here
	// before its rows are read, the same refusal loadAccount makes for it.
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
			// A tombstone or an account that is gone. Its stale row is not the
			// answer, exactly as the single read does not read a tombstone's rows.
			continue
		}
		out[id] = append(out[id], identifierOf(r))
	}
	return out, nil
}

// Externals lists the external identities bound to an account.
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

// ListServiceSecrets lists the secrets of a service account: identifier, label,
// expiry and revocation, never a secret.
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

// List returns one page of accounts and the cursor of the next, which is empty
// when there is none. The accounts come in the order of their identifiers, which
// are UUIDv7 text and so close to the order of creation, and a page is read by
// the key and not by an offset, so that a page costs the same however deep it
// is. A deleted account is left out unless the query asks for the status. A
// listing by an identifier prefix reads a range of the identifier index, which
// every engine does the same way, and never matches by pattern. A listing by a
// display-name prefix compares the display name as written, by its bytes, the
// same range on every collation the database was created with — no index covers
// the column, so it scans the table rather than reading an index range.
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
		// The display name is stored as written and is compared the same way, so
		// the prefix is a byte prefix of the stored name and its case matters. The
		// comparison is pinned to the byte collation on PostgreSQL: left to the
		// database's own collation it would fold case and accents and read rows
		// that are not a byte prefix of the name. No index covers the column, so
		// this scans the table. A prefix that is not text the column holds — not
		// valid UTF-8 — begins no name, so the read is empty rather than a byte
		// sequence the database would reject; the management route refuses it as a
		// shape error before it reaches here.
		if !utf8.ValidString(q.DisplayNamePrefix) {
			return nil, "", nil
		}
		if bound, bounded := displayNameBound(q.DisplayNamePrefix); bounded {
			tx = tx.Where(p.displayNameRange(), q.DisplayNamePrefix, bound)
		} else {
			// The prefix is every character the highest rune, so there is no
			// string above it that begins with it: compare on the lower bound
			// alone.
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

// displayNameRange is the predicate that reads a range of display names by their
// bytes, so that the range a prefix listing reads is the same range whatever
// collation the column carries now or the database was created with. PostgreSQL
// orders text under the database's own collation by default, which folds case
// and accents, so the comparison is pinned to the C (byte) collation there — the
// same collation the identifier column is declared with. SQLite compares text
// byte by byte already and has no C collation, so its predicate is written
// plain. It carries two placeholders, the prefix and its upper bound.
func (p *product) displayNameRange() string {
	if p.dialect == db.Postgres {
		return `display_name COLLATE "C" >= ? AND display_name COLLATE "C" < ?`
	}
	return "display_name >= ? AND display_name < ?"
}

// displayNameLowerRange is the predicate that reads from a display-name prefix
// upward, for a prefix no string exceeds. A prefix made only of the highest rune
// has no upper bound, and the names it begins are exactly those at or above it,
// so the range is closed on the high end by nothing. It pins the byte collation
// on PostgreSQL as displayNameRange does and carries one placeholder, the
// prefix.
func (p *product) displayNameLowerRange() string {
	if p.dialect == db.Postgres {
		return `display_name COLLATE "C" >= ?`
	}
	return "display_name >= ?"
}

// displayNameBound returns the least string above every stored display name that
// begins with prefix, and whether such a bound exists at all -- the domain is the
// names the column holds, not every possible string. A prefix that is not valid UTF-8
// begins no name the column holds and so selects nothing: the bound it returns
// is the empty string, the second result true, and that empty bound closes the
// range to nothing. The prefix is read as written, so it may end in a
// multi-byte character; the raise is over the last character and not its last
// byte, and it steps to the character above the last one — over the surrogate
// code points, which no name holds — so the bound is itself valid UTF-8 and the
// bytewise range it closes reads exactly the names that begin with the prefix. A
// prefix whose last character is the highest rune is carried to the character
// before it. A prefix that carries past every character — each one the highest
// rune — has no string above it that begins with it, and the second result is
// false. Every bound returned is valid UTF-8.
func displayNameBound(prefix string) (string, bool) {
	if !utf8.ValidString(prefix) {
		// The prefix is not text the column holds, so it begins no stored name:
		// the empty bound closes the range to nothing. Reading the whole prefix
		// here answers the same wherever in it the bytes that begin no rune fall,
		// so every bound returned is valid UTF-8.
		return "", true
	}
	for prefix != "" {
		last, size := utf8.DecodeLastRuneInString(prefix)
		if last == utf8.MaxRune {
			// The last character is the highest rune and cannot be raised: carry
			// to the character before it.
			prefix = prefix[:len(prefix)-size]
			continue
		}
		next := last + 1
		if next >= 0xD800 && next <= 0xDFFF {
			// The characters between U+D7FF and U+E000 are the surrogate code
			// points, which no UTF-8 string holds: the successor of the last
			// character is the one above the gap.
			next = 0xE000
		}
		var raised [utf8.UTFMax]byte
		n := utf8.EncodeRune(raised[:], next)
		return prefix[:len(prefix)-size] + string(raised[:n]), true
	}
	return "", false
}

// markPasswords sets PasswordSet on a page of accounts with one query for the
// page.
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
