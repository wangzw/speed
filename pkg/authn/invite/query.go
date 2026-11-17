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

// List lists the invitations of a tenant, one page at a time. Unexpired pending
// invitations come first, then expired ones, then accepted, then revoked; each
// group is ordered by creation time descending with the identifier descending as
// the tie-break. The cursor is opaque and carries the group and the sort key of
// the page's last row, so a later call resumes where this one stopped. An
// expired pending invitation is listed as it is: it is still a stored pending
// row, and hiding it would show an invitation that was sent as one that never
// was.
//
// The management route narrows the page with list and a status filter; this
// method is the unfiltered listing.
func (p *product) List(ctx context.Context, tenant string, page authn.Page) ([]Invitation, string, error) {
	return p.list(ctx, tenant, page, listAll, p.at())
}

// listFilter narrows a listing to one view of an invitation. It is not a stored
// state: pending and expired are both InvitationPending rows, split by whether
// the row was superseded or the deadline has passed, so expiry needs no sweeper
// and no write.
type listFilter uint8

// The views a listing can be narrowed to. The zero value lists every state.
const (
	listAll listFilter = iota
	listPending
	listExpired
	listAccepted
	listRevoked
)

// parseListFilter reads the status query's value. The empty value lists every
// invitation; a value outside the closed set is ErrInvalidArgument, which the
// route answers 400 rather than serving the unfiltered page.
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

// The listing's groups, in the order a page lists them: pending invitations
// whose deadline has not passed, expired ones, then the two terminal states.
// The numbers are the order, not the stored status; pending and expired share
// the stored pending state and differ only in the deadline.
const (
	listGroupPending = iota
	listGroupExpired
	listGroupAccepted
	listGroupRevoked
)

// listGroupSQL is the group of a row as SQL, in the listing's order. The
// deadline is read for the stored pending state, so the group follows the same
// clock judgement the two pending filters use. A pending row a later invitation
// replaced belongs to the expired group whatever its deadline says: its live
// slot is gone. The placeholders are filled by listGroupArgs.
func listGroupSQL() string {
	return "CASE WHEN status = " + strconv.Itoa(int(statusPending)) +
		" AND superseded_at IS NULL AND expires_at > ? THEN " + strconv.Itoa(listGroupPending) +
		" WHEN status = " + strconv.Itoa(int(statusPending)) + " THEN " + strconv.Itoa(listGroupExpired) +
		" WHEN status = " + strconv.Itoa(int(statusAccepted)) + " THEN " + strconv.Itoa(listGroupAccepted) +
		" ELSE " + strconv.Itoa(listGroupRevoked) + " END"
}

// listGroupArgs are the values listGroupSQL's placeholders bind: the current
// instant is the deadline comparison.
func listGroupArgs(now time.Time) []any { return []any{now} }

// listGroupOf reports the group of one row read at now, by the same rule the
// query applies. It builds the cursor of the page's last row.
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

// list lists the invitations of a tenant, one page at a time, narrowed by
// filter. The order, the cursor and the page size are the same whichever filter
// is applied: one more row than the page is read, so whether another page
// follows is known without a second query. now judges both the groups and the
// deadline boundary, so a caller building the views passes the same instant and
// a row never reads in two groups within one answer.
//
// An expired pending invitation is redeemable up to but not at its deadline,
// which is the boundary the two pending filters share: pending reads
// expires_at > now, expired reads expires_at <= now. A pending row a later
// invitation superseded is excluded from the pending filter and listed by the
// expired one whatever its deadline says, because the later invitation took the
// live slot.
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
		// The page resumes after the last row's group and sort key. The group
		// is the deadline judgement at this page's clock, so a row that crossed
		// its deadline between two pages can move to another group.
		args := append(listGroupArgs(now), after.group)
		args = append(args, listGroupArgs(now)...)
		args = append(args, after.group, after.createdAt, after.createdAt, after.id)
		tx = tx.Where(
			"("+group+" > ?) OR ("+group+" = ? AND (created_at < ? OR (created_at = ? AND id < ?)))",
			args...)
	}
	var rows []invitationRow
	// One more than the page, so that whether another page follows is known
	// without a second query.
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

// Revoke revokes a pending invitation that has not expired and that no later
// invitation superseded, voiding its token and recording when it was revoked.
// An expired, superseded or accepted invitation is refused, because removing a
// membership is not this route's job and an expired link no longer grants
// anything; a superseded row reads expired even before its deadline. Revoking
// one that was already revoked is an idempotent success and leaves its
// revocation time alone.
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
	// The conditional write met no live pending row: tell the caller why, or
	// treat an already revoked one as done.
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
	// A pending row that appeared again between the two statements is a race
	// this call does not loop on; report it as not found to keep the answer
	// closed.
	return fmt.Errorf("%w", ErrInvitationNotFound)
}

// cursor is the sort key a listing resumes after: the group, the creation time
// and the identifier of the page's last row. The group is kept as an int so that
// reading it back needs no narrowing conversion.
type cursor struct {
	group     int
	createdAt time.Time
	id        string
}

// cursorVersion tags the cursor format. It is part of the encoded value, so a
// cursor minted by an earlier build, or one whose format this build does not
// know, is refused rather than read as this build's sort key.
const cursorVersion = "v1"

// encodeCursor renders a cursor as an opaque URL-safe string: the format
// version and the three parts of the sort key, base64url-encoded together.
func encodeCursor(c cursor) string {
	raw := cursorVersion + "\n" + c.createdAt.UTC().Format(time.RFC3339Nano) + "\n" + strconv.Itoa(c.group) + "\n" + c.id
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

// decodeCursor reads a cursor encodeCursor wrote. A value that does not read
// back, including one carrying another version or the three-part layout of an
// earlier build, is refused rather than served as the first page.
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
