package invite

import (
	"context"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/vislake/speed/pkg/authn"
)

// List lists the invitations of a tenant, one page at a time: unexpired pending
// first, then expired, accepted and revoked, each group newest first. The second
// result is the cursor of the next page, empty when there is none.
func (p *product) List(ctx context.Context, tenant string, page authn.Page) ([]Invitation, string, error) {
	return p.list(ctx, tenant, page, listAll, p.at())
}

type listFilter uint8

const (
	listAll listFilter = iota
	listPending
	listExpired
	listAccepted
	listRevoked
)

func parseListFilter(value string) (listFilter, error) {
	switch value {
	case "":
		return listAll, nil
	case "pending":
		return listPending, nil
	case "expired":
		return listExpired, nil
	case "accepted":
		return listAccepted, nil
	case "revoked":
		return listRevoked, nil
	}
	return listAll, fmt.Errorf("%w: the status is not one of pending, expired, accepted or revoked",
		authn.ErrInvalidArgument)
}

const (
	listGroupPending = iota
	listGroupExpired
	listGroupAccepted
	listGroupRevoked
)

// listGroupSQL is a row's group in SQL, in listing order. A pending row a later
// invitation superseded belongs to the expired group whatever its deadline says.
func listGroupSQL() string {
	return "CASE WHEN status = " + strconv.Itoa(int(statusPending)) +
		" AND superseded_at IS NULL AND expires_at > ? THEN " + strconv.Itoa(listGroupPending) +
		" WHEN status = " + strconv.Itoa(int(statusPending)) + " THEN " + strconv.Itoa(listGroupExpired) +
		" WHEN status = " + strconv.Itoa(int(statusAccepted)) + " THEN " + strconv.Itoa(listGroupAccepted) +
		" ELSE " + strconv.Itoa(listGroupRevoked) + " END"
}

func listGroupArgs(now time.Time) []any { return []any{now} }

func listGroupOf(row invitationRow, now time.Time) int {
	switch row.Status {
	case statusAccepted:
		return listGroupAccepted
	case statusRevoked:
		return listGroupRevoked
	}
	if row.SupersededAt == nil && utc(row.ExpiresAt).After(now) {
		return listGroupPending
	}
	return listGroupExpired
}

// list lists one page of a tenant's invitations. The same instant judges the
// groups and the deadline boundary, so a row never reads in two groups within
// one answer.
func (p *product) list(ctx context.Context, tenant string, page authn.Page, filter listFilter, now time.Time) ([]Invitation, string, error) {
	if tenant == "" {
		return nil, "", nil
	}
	limit := page.EffectiveLimit()
	group := listGroupSQL()
	tx := p.conn(ctx).Where("tenant_id = ?", tenant)
	switch filter {
	case listPending:
		tx = tx.Where("status = ? AND superseded_at IS NULL AND expires_at > ?", statusPending, now)
	case listExpired:
		tx = tx.Where("status = ? AND (superseded_at IS NOT NULL OR expires_at <= ?)", statusPending, now)
	case listAccepted:
		tx = tx.Where("status = ?", statusAccepted)
	case listRevoked:
		tx = tx.Where("status = ?", statusRevoked)
	}
	if page.After != "" {
		after, err := decodeCursor(page.After)
		if err != nil {
			return nil, "", fmt.Errorf("%w: the cursor is not readable", authn.ErrInvalidArgument)
		}
		args := append(listGroupArgs(now), after.group)
		args = append(args, listGroupArgs(now)...)
		args = append(args, after.group, after.createdAt, after.createdAt, after.id)
		tx = tx.Where(
			"("+group+" > ?) OR ("+group+" = ? AND (created_at < ? OR (created_at = ? AND id < ?)))",
			args...)
	}
	var rows []invitationRow
	order := clause.OrderBy{Expression: clause.Expr{
		SQL:  group + " ASC, created_at DESC, id DESC",
		Vars: listGroupArgs(now),
	}}
	if err := tx.Order(order).Limit(limit + 1).Find(&rows).Error; err != nil {
		return nil, "", failed("listing invitations", err)
	}
	next := ""
	if len(rows) > limit {
		rows = rows[:limit]
		last := rows[len(rows)-1]
		next = encodeCursor(cursor{
			group:     listGroupOf(last, now),
			createdAt: utc(last.CreatedAt),
			id:        last.ID,
		})
	}
	views := make([]Invitation, len(rows))
	for i, r := range rows {
		views[i] = toInvitation(r)
	}
	return views, next, nil
}

// Revoke revokes a pending, unexpired invitation no later invitation superseded
// and voids its token. An expired, superseded or accepted invitation is refused;
// revoking one that was already revoked is an idempotent success.
func (p *product) Revoke(ctx context.Context, tenant, id string) error {
	if tenant == "" || id == "" {
		return fmt.Errorf("%w", ErrInvitationNotFound)
	}
	now := p.at()
	rows := int64(0)
	err := p.conn(ctx).Transaction(func(tx *gorm.DB) error {
		res := tx.Model(&invitationRow{}).
			Where("tenant_id = ? AND id = ? AND status = ? AND expires_at > ? AND superseded_at IS NULL",
				tenant, id, statusPending, now).
			Updates(map[string]any{"status": statusRevoked, "revoked_at": now, "updated_at": now})
		if res.Error != nil {
			return res.Error
		}
		rows = res.RowsAffected
		return nil
	})
	if err != nil {
		return failed("revoking an invitation", err)
	}
	if rows == 1 {
		return nil
	}
	held, found, err := p.findByID(p.conn(ctx), tenant, id)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("%w", ErrInvitationNotFound)
	}
	switch held.Status {
	case statusRevoked:
		return nil
	}
	if terminal := terminalState(held, now); terminal != nil {
		return terminal
	}
	return fmt.Errorf("%w", ErrInvitationNotFound)
}

type cursor struct {
	group     int
	createdAt time.Time
	id        string
}

const cursorVersion = "v1"

func encodeCursor(c cursor) string {
	raw := cursorVersion + "\n" + c.createdAt.UTC().Format(time.RFC3339Nano) + "\n" + strconv.Itoa(c.group) + "\n" + c.id
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func decodeCursor(s string) (cursor, error) {
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return cursor{}, err
	}
	parts := strings.Split(string(raw), "\n")
	if len(parts) != 4 {
		return cursor{}, fmt.Errorf("the cursor does not hold the version and three sort-key parts")
	}
	if parts[0] != cursorVersion {
		return cursor{}, fmt.Errorf("the cursor carries an unknown version")
	}
	at, err := time.Parse(time.RFC3339Nano, parts[1])
	if err != nil {
		return cursor{}, err
	}
	n, err := strconv.Atoi(parts[2])
	if err != nil {
		return cursor{}, err
	}
	if n < listGroupPending || n > listGroupRevoked {
		return cursor{}, fmt.Errorf("the cursor carries a group outside the listing's set")
	}
	return cursor{createdAt: at, group: n, id: parts[3]}, nil
}
