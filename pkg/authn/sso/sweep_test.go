package sso

import (
	"context"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn/internal/clock"
)

// insertClaim writes a domain claim row directly, the way another replica would.
func (r *rig) insertClaim(domain, idpID, tenant, token string, verified bool, createdAt time.Time) {
	r.t.Helper()
	var verifiedAt any
	if verified {
		verifiedAt = clock.UTC(createdAt)
	}
	err := r.h.Exec("INSERT INTO "+domainTable+
		" (domain, idp_id, tenant_id, verify_token, verified_at, created_at) VALUES (?, ?, ?, ?, ?, ?)",
		domain, idpID, tenant, token, verifiedAt, clock.UTC(createdAt)).Error
	if err != nil {
		r.t.Fatalf("inserting a claim: %v", err)
	}
}

// insertAssertion writes an assertion identifier row directly.
func (r *rig) insertAssertion(idpID, assertionID string, notOnOrAfter, seenAt time.Time) {
	r.t.Helper()
	err := r.h.Exec("INSERT INTO "+assertionTable+
		" (idp_id, assertion_id, not_on_or_after, seen_at) VALUES (?, ?, ?, ?)",
		idpID, assertionID, clock.UTC(notOnOrAfter), clock.UTC(seenAt)).Error
	if err != nil {
		r.t.Fatalf("inserting an assertion: %v", err)
	}
}

// TestSweepRemovesStaleUnverifiedClaims holds the claim cutoff: only an
// unverified claim past the retention window goes.
func TestSweepRemovesStaleUnverifiedClaims(t *testing.T) {
	r := newRigSQLite(t)

	r.insertClaim("old.example.com", "idp-1", "tenant-a", "t", false, base.Add(-8*24*time.Hour))
	r.insertClaim("fresh.example.com", "idp-1", "tenant-a", "t", false, base.Add(-24*time.Hour))
	r.insertClaim("verified.example.com", "idp-1", "tenant-a", "t", true, base.Add(-8*24*time.Hour))

	if err := r.p.Sweep(context.Background()); err != nil {
		t.Fatalf("Sweep: %v", err)
	}

	var domains []string
	if err := r.h.Table(domainTable).Order("domain ASC").Pluck("domain", &domains).Error; err != nil {
		t.Fatalf("reading the claims: %v", err)
	}
	want := []string{"fresh.example.com", "verified.example.com"}
	if len(domains) != len(want) || domains[0] != want[0] || domains[1] != want[1] {
		t.Errorf("after the sweep the claims are %v, want %v", domains, want)
	}
}

// TestSweepRemovesExpiredAssertions holds the assertion cutoff: a row past its
// condition goes, one before it stays.
func TestSweepRemovesExpiredAssertions(t *testing.T) {
	r := newRigSQLite(t)

	r.insertAssertion("idp-1", "expired", base.Add(-time.Minute), base.Add(-10*time.Minute))
	r.insertAssertion("idp-1", "live", base.Add(time.Hour), base)

	if err := r.p.Sweep(context.Background()); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if r.count(assertionTable) != 1 {
		t.Errorf("after the sweep the assertion rows = %d, want 1", r.count(assertionTable))
	}
	var ids []string
	if err := r.h.Table(assertionTable).Pluck("assertion_id", &ids).Error; err != nil {
		t.Fatalf("reading the assertions: %v", err)
	}
	if len(ids) != 1 || ids[0] != "live" {
		t.Errorf("the surviving assertion is %v, want the live one", ids)
	}
}

// TestStopSweepEndsTheGoroutineWithinItsBound holds the lifecycle: the sweep
// goroutine starts, Stop ends it within the bound, and a Stop with no goroutine
// is a no-op.
func TestStopSweepEndsTheGoroutineWithinItsBound(t *testing.T) {
	r := newRigSQLite(t)
	if err := r.p.stopSweep(); err != nil {
		t.Fatalf("stopSweep with no goroutine: %v", err)
	}
	r.p.startSweep(context.Background())
	if r.p.sweeper == nil {
		t.Fatal("startSweep recorded no sweeper")
	}
	if err := r.p.stopSweep(); err != nil {
		t.Fatalf("stopSweep: %v", err)
	}
}
