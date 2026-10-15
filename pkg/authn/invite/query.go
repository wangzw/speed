package invite

import (
	"context"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/authn"
)

// List lists the invitations of a tenant, one page at a time. Pending
// invitations come first, then by creation time descending and identifier
// descending as the tie-break. The cursor is opaque and carries the sort key of
// the page's last row, so a later call resumes where this one stopped. An
// expired pending invitation is listed as it is: it is still a pending row, and
// hiding it would show an invitation that was sent as one that never was.
func (p *product) List(ctx context.Context, tenant string, page authn.Page) ([]Invitation, string, error) {
	if tenant == "" {
		return nil, "", nil
	}
	limit := page.EffectiveLimit()
	tx := p.conn(ctx).Where("tenant_id = ?", tenant)
	if page.After != "" {
		after, err := decodeCursor(page.After)
		if err != nil {
			return nil, "", fmt.Errorf("%w: the cursor is not readable", authn.ErrInvalidArgument)
		}
		tx = tx.Where(
			"status > ? OR (status = ? AND (created_at < ? OR (created_at = ? AND id < ?)))",
			after.status, after.status, after.createdAt, after.createdAt, after.id)
	}
	var rows []invitationRow
	// One more than the page, so that whether another page follows is known
	// without a second query.
	if err := tx.Order("status ASC, created_at DESC, id DESC").Limit(limit + 1).Find(&rows).Error; err != nil {
		return nil, "", failed("listing invitations", err)
	}
	next := ""
	if len(rows) > limit {
		rows = rows[:limit]
		last := rows[len(rows)-1]
		next = encodeCursor(cursor{status: int(last.Status), createdAt: utc(last.CreatedAt), id: last.ID})
	}
	views := make([]Invitation, len(rows))
	for i, r := range rows {
		views[i] = toInvitation(r)
	}
	return views, next, nil
}

// Revoke revokes a pending invitation, voiding its token. Revoking one that was
// already accepted is ErrInvitationAlreadyAccepted, because removing a member is
// not this route's job; revoking one that was already revoked is an idempotent
// success.
func (p *product) Revoke(ctx context.Context, tenant, id string) error {
	if tenant == "" || id == "" {
		return fmt.Errorf("%w", ErrInvitationNotFound)
	}
	now := p.at()
	rows := int64(0)
	err := p.conn(ctx).Transaction(func(tx *gorm.DB) error {
		res := tx.Model(&invitationRow{}).
			Where("tenant_id = ? AND id = ? AND status = ?", tenant, id, statusPending).
			Updates(map[string]any{"status": statusRevoked, "updated_at": now})
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
	// The conditional write met no pending row: tell the caller why, or treat
	// an already revoked one as done.
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
	case statusAccepted:
		return fmt.Errorf("%w", ErrInvitationAlreadyAccepted)
	}
	// A pending row that appeared again between the two statements is a race
	// this call does not loop on; report it as not found to keep the answer
	// closed.
	return fmt.Errorf("%w", ErrInvitationNotFound)
}

// cursor is the sort key a listing resumes after: the status, the creation
// time and the identifier of the page's last row. The status is kept as an int
// so that reading it back needs no narrowing conversion.
type cursor struct {
	status    int
	createdAt time.Time
	id        string
}

// encodeCursor renders a cursor as an opaque URL-safe string: the three parts
// of the sort key, base64url-encoded together.
func encodeCursor(c cursor) string {
	raw := c.createdAt.UTC().Format(time.RFC3339Nano) + "\n" + strconv.Itoa(c.status) + "\n" + c.id
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

// decodeCursor reads a cursor encodeCursor wrote. A value that does not read
// back is refused rather than served as the first page.
func decodeCursor(s string) (cursor, error) {
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return cursor{}, err
	}
	parts := strings.Split(string(raw), "\n")
	if len(parts) != 3 {
		return cursor{}, fmt.Errorf("the cursor does not hold three parts")
	}
	at, err := time.Parse(time.RFC3339Nano, parts[0])
	if err != nil {
		return cursor{}, err
	}
	n, err := strconv.Atoi(parts[1])
	if err != nil {
		return cursor{}, err
	}
	if n < 0 || n > int(statusRevoked) {
		return cursor{}, fmt.Errorf("the cursor carries a status outside the stored set")
	}
	return cursor{createdAt: at, status: n, id: parts[2]}, nil
}
