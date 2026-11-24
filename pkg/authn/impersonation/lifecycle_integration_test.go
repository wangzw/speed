//go:build integration

package impersonation

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
)

// TestTheListAndRevokeLifecycleOnPostgres checks that a page settles a lapsed record and the following revoke is a no-op.
func TestTheListAndRevokeLifecycleOnPostgres(t *testing.T) {
	h := openDialect(t, "postgres")
	r := newRig(t, h)
	ctx := context.Background()

	g, err := r.p.Start(ctx, adminIdentity(t), request())
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	r.audit.rows = nil
	r.clock.advance(31 * time.Minute)

	page, _, err := r.p.List(ctx, authn.GrantQuery{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(page) != 1 || page[0].EndReason != authn.EndExpired || page[0].EndedAt.IsZero() {
		t.Fatalf("the page is %+v, want the record ended as expired", page)
	}

	if err := r.p.Revoke(ctx, g.ID, adminIdentity(t), "abuse reported"); err != nil {
		t.Fatalf("Revoke of a lapsed record = %v, want nil", err)
	}
	_, endedBy, reason := r.stored(g.ID)
	if reason == nil || *reason != authn.EndExpired {
		t.Errorf("the end reason is %q, want expired left alone", reasonText(reason))
	}
	if endedBy != nil {
		t.Errorf("the revoke recorded an initiator %q, want none", *endedBy)
	}
	if rows := r.audit.entries(); len(rows) != 0 {
		t.Errorf("the revoke of a lapsed record wrote audit rows: %+v", rows)
	}
	if got := len(r.bus.ofType(authn.EventImpersonationEnded)); got != 0 {
		t.Errorf("%d ended events were published for an expiry, want none", got)
	}
	if got := r.counter(outcomeEnded); got != 1 {
		t.Errorf("the ended counter reads %d, want 1", got)
	}
}

// TestASettleWriteBackFailureOnPostgres checks that a failed write-back surfaces as an error rather than an unended record.
func TestASettleWriteBackFailureOnPostgres(t *testing.T) {
	h := openDialect(t, "postgres")
	r := newRig(t, h)
	ctx := context.Background()

	g, err := r.p.Start(ctx, adminIdentity(t), request())
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	function := `CREATE FUNCTION block_impersonation_end() RETURNS trigger AS $$
BEGIN
	RAISE EXCEPTION 'write blocked';
END;
$$ LANGUAGE plpgsql`
	if err := h.Exec(function).Error; err != nil {
		t.Fatalf("creating the blocking function: %v", err)
	}
	trigger := `CREATE TRIGGER block_impersonation_end BEFORE UPDATE ON authn_impersonations
	FOR EACH ROW EXECUTE FUNCTION block_impersonation_end()`
	if err := h.Exec(trigger).Error; err != nil {
		t.Fatalf("creating the blocking trigger: %v", err)
	}
	r.clock.advance(31 * time.Minute)

	if _, active, err := r.p.Active(ctx, g.ID); err == nil || active {
		t.Errorf("Active = (active %v, err %v), want the write-back failure", active, err)
	}
	if _, _, err := r.p.List(ctx, authn.GrantQuery{}); !errors.Is(err, authn.ErrUnavailable) {
		t.Errorf("List = %v, want ErrUnavailable", err)
	}
	r.audit.rows = nil
	if err := r.p.Revoke(ctx, g.ID, adminIdentity(t), "abuse reported"); !errors.Is(err, authn.ErrUnavailable) {
		t.Errorf("Revoke = %v, want ErrUnavailable", err)
	}
	if rows := r.audit.entries(); len(rows) != 0 {
		t.Errorf("the failed write-back wrote audit rows: %+v", rows)
	}
	if endedAt, _, _ := r.stored(g.ID); endedAt != nil {
		t.Errorf("the blocked write ended the record: %v", *endedAt)
	}
	if got := len(r.bus.ofType(authn.EventImpersonationEnded)); got != 0 {
		t.Errorf("%d ended events were published, want none", got)
	}
}
