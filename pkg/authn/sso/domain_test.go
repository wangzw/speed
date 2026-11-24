package sso

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/db"
)

func (r *rig) verifyState(idpID, domain string) bool {
	r.t.Helper()
	var rows []domainRow
	if err := r.h.Table(domainTable).Where("idp_id = ? AND domain = ?", idpID, domain).Find(&rows).Error; err != nil {
		r.t.Fatalf("reading a domain claim: %v", err)
	}
	if len(rows) == 0 {
		r.t.Fatalf("the claim of %s by %s is not there", domain, idpID)
	}
	return rows[0].VerifiedAt != nil
}

// TestClaimAndVerifyDomain checks that a claimed domain is verified once the TXT record holds the token.
func TestClaimAndVerifyDomain(t *testing.T) {
	r := newRigSQLite(t)
	idp := r.putOIDC("tenant-a", "idp")

	token, err := r.p.ClaimDomain(context.Background(), idp.ID, "Example.COM")
	if err != nil {
		t.Fatalf("ClaimDomain: %v", err)
	}
	if token == "" {
		t.Fatal("ClaimDomain returned an empty token")
	}
	if r.verifyState(idp.ID, "example.com") {
		t.Fatal("a fresh claim is verified")
	}

	if err := r.p.VerifyDomain(context.Background(), idp.ID, "example.com"); !errors.Is(err, errTXTNotFound) {
		t.Errorf("VerifyDomain without a record returned %v, want errTXTNotFound", err)
	}
	if r.verifyState(idp.ID, "example.com") {
		t.Fatal("a failed verification changed the state")
	}

	r.publishTXT("example.com", token)
	if err := r.p.VerifyDomain(context.Background(), idp.ID, "example.com"); err != nil {
		t.Fatalf("VerifyDomain: %v", err)
	}
	if !r.verifyState(idp.ID, "example.com") {
		t.Fatal("a verified domain is not marked verified")
	}
	if got := r.bus.changes(idp.ID); !hasChange(got, changeDomainVerified) {
		t.Errorf("the changes are %v, want domain-verified", got)
	}

	if err := r.p.RemoveDomain(context.Background(), idp.ID, "example.com"); err != nil {
		t.Fatalf("RemoveDomain: %v", err)
	}
	if r.count(domainTable) != 0 {
		t.Error("a removed claim is still there")
	}
	if got := r.bus.changes(idp.ID); !hasChange(got, changeDomainRemoved) {
		t.Errorf("the changes are %v, want domain-removed", got)
	}
}

// TestDomainsReportsEachClaimAndItsState checks that Domains reports every claim with its state.
func TestDomainsReportsEachClaimAndItsState(t *testing.T) {
	r := newRigSQLite(t)
	idp := r.putOIDC("tenant-a", "idp")
	other := r.putOIDC("tenant-b", "other")

	if _, err := r.p.ClaimDomain(context.Background(), idp.ID, "claimed.example"); err != nil {
		t.Fatalf("ClaimDomain claimed.example: %v", err)
	}
	token, err := r.p.ClaimDomain(context.Background(), idp.ID, "proved.example")
	if err != nil {
		t.Fatalf("ClaimDomain proved.example: %v", err)
	}
	r.publishTXT("proved.example", token)
	if err = r.p.VerifyDomain(context.Background(), idp.ID, "proved.example"); err != nil {
		t.Fatalf("VerifyDomain proved.example: %v", err)
	}
	if _, err = r.p.ClaimDomain(context.Background(), other.ID, "other.example"); err != nil {
		t.Fatalf("ClaimDomain other.example: %v", err)
	}

	want := []authn.SSOProviderDomain{
		{Domain: "claimed.example", Verified: false},
		{Domain: "proved.example", Verified: true},
	}
	got, err := r.p.Domains(context.Background(), idp.ID)
	if err != nil {
		t.Fatalf("Domains: %v", err)
	}
	if !slices.Equal(got, want) {
		t.Errorf("Domains = %+v, want %+v", got, want)
	}

	many, err := r.p.DomainsMany(context.Background(), []string{idp.ID, other.ID, "absent"})
	if err != nil {
		t.Fatalf("DomainsMany: %v", err)
	}
	if !slices.Equal(many[idp.ID], want) {
		t.Errorf("the batch read's claims for %s = %+v, want %+v", idp.ID, many[idp.ID], want)
	}
	if got := many[other.ID]; !slices.Equal(got, []authn.SSOProviderDomain{{Domain: "other.example", Verified: false}}) {
		t.Errorf("the batch read's claims for %s = %+v, want one unverified claim", other.ID, got)
	}
	if got, ok := many["absent"]; !ok || len(got) != 0 {
		t.Errorf("the batch read's claims for an unknown provider = %+v (present %t), want an empty entry", got, ok)
	}
}

// TestClaimRefusedOnceAnotherProviderVerifiedIt checks that a claim is refused with ErrDomainClaimed once another provider verified the domain.
func TestClaimRefusedOnceAnotherProviderVerifiedIt(t *testing.T) {
	r := newRigSQLite(t)
	first := r.putOIDC("tenant-a", "first")
	second := r.putOIDC("tenant-b", "second")

	token, err := r.p.ClaimDomain(context.Background(), first.ID, "example.com")
	if err != nil {
		t.Fatalf("ClaimDomain: %v", err)
	}
	r.publishTXT("example.com", token)
	if err := r.p.VerifyDomain(context.Background(), first.ID, "example.com"); err != nil {
		t.Fatalf("VerifyDomain: %v", err)
	}

	if _, err := r.p.ClaimDomain(context.Background(), second.ID, "example.com"); !errors.Is(err, authn.ErrDomainClaimed) {
		t.Errorf("ClaimDomain of a verified domain returned %v, want ErrDomainClaimed", err)
	}
}

// TestTwoProvidersVerifyingOneDomainHaveOneWinner checks that two providers verifying one domain leave one winner under the unique constraint.
func TestTwoProvidersVerifyingOneDomainHaveOneWinner(t *testing.T) {
	run := func(t *testing.T, database db.Database, _ string) {
		r := newRig(t, database)
		first := r.putOIDC("tenant-a", "first")
		second := r.putOIDC("tenant-b", "second")

		tokenA, err := r.p.ClaimDomain(context.Background(), first.ID, "example.com")
		if err != nil {
			t.Fatalf("ClaimDomain A: %v", err)
		}
		tokenB, err := r.p.ClaimDomain(context.Background(), second.ID, "example.com")
		if err != nil {
			t.Fatalf("ClaimDomain B: %v", err)
		}
		r.txt[txtName("example.com")] = []string{"speed-authn=" + tokenA, "speed-authn=" + tokenB}

		var wg sync.WaitGroup
		results := make([]error, 2)
		ids := []string{first.ID, second.ID}
		for i, id := range ids {
			wg.Add(1)
			go func(i int, id string) {
				defer wg.Done()
				results[i] = r.p.VerifyDomain(context.Background(), id, "example.com")
			}(i, id)
		}
		wg.Wait()

		winners, claimed := 0, 0
		for _, err := range results {
			switch {
			case err == nil:
				winners++
			case errors.Is(err, authn.ErrDomainClaimed):
				claimed++
			default:
				t.Fatalf("a verification returned %v, want nil or ErrDomainClaimed", err)
			}
		}
		if winners != 1 || claimed != 1 {
			t.Fatalf("winners=%d claimed=%d, want exactly one of each", winners, claimed)
		}
		var n int64
		if err := r.h.Table(domainTable).Where("domain = ? AND verified_at IS NOT NULL", "example.com").Count(&n).Error; err != nil {
			t.Fatalf("counting verified rows: %v", err)
		}
		if n != 1 {
			t.Errorf("verified rows for the domain = %d, want 1", n)
		}
	}
	forEachDialect(t, run)
}

// TestReverifyWithdrawsADomainNoLongerProven checks that Reverify withdraws a verification whose record is gone.
func TestReverifyWithdrawsADomainNoLongerProven(t *testing.T) {
	r := newRigSQLite(t)
	idp := r.putOIDC("tenant-a", "idp")

	token, err := r.p.ClaimDomain(context.Background(), idp.ID, "example.com")
	if err != nil {
		t.Fatalf("ClaimDomain: %v", err)
	}
	r.publishTXT("example.com", token)
	if err := r.p.VerifyDomain(context.Background(), idp.ID, "example.com"); err != nil {
		t.Fatalf("VerifyDomain: %v", err)
	}

	delete(r.txt, txtName("example.com"))
	if err := r.p.ReverifyDomains(context.Background(), idp.ID); err != nil {
		t.Fatalf("ReverifyDomains: %v", err)
	}
	if r.verifyState(idp.ID, "example.com") {
		t.Fatal("a domain whose record is gone is still verified")
	}
	if got := r.bus.changes(idp.ID); !hasChange(got, changeDomainUnverified) {
		t.Errorf("the changes are %v, want domain-unverified", got)
	}
}

// TestReverifyKeepsAPageableLookupFailure checks that Reverify keeps the verification when the lookup fails and returns the failure.
func TestReverifyKeepsAPageableLookupFailure(t *testing.T) {
	r := newRigSQLite(t)
	idp := r.putOIDC("tenant-a", "idp")

	token, err := r.p.ClaimDomain(context.Background(), idp.ID, "example.com")
	if err != nil {
		t.Fatalf("ClaimDomain: %v", err)
	}
	r.publishTXT("example.com", token)
	if err := r.p.VerifyDomain(context.Background(), idp.ID, "example.com"); err != nil {
		t.Fatalf("VerifyDomain: %v", err)
	}

	r.failTXT("example.com", errors.New("the resolver is down"))
	if err := r.p.ReverifyDomains(context.Background(), idp.ID); err == nil {
		t.Fatal("ReverifyDomains over a failing lookup returned no error")
	}
	if !r.verifyState(idp.ID, "example.com") {
		t.Fatal("a failing lookup withdrew the verification")
	}
}

// TestNormalizeDomainRefusesWhatIsNotADomain holds the discovery's domain rule.
func TestNormalizeDomainRefusesWhatIsNotADomain(t *testing.T) {
	good := map[string]string{
		"Example.com":   "example.com",
		" example.com ": "example.com",
		"a.b.c":         "a.b.c",
	}
	for in, want := range good {
		got, err := normalizeDomain(in)
		if err != nil || got != want {
			t.Errorf("normalizeDomain(%q) = %q, %v, want %q", in, got, err, want)
		}
	}
	bad := []string{"", "exa mple.com", "例子.测试", "-bad.com", "bad-.com", "a..b", "a@b.com"}
	for _, in := range bad {
		if _, err := normalizeDomain(in); !errors.Is(err, errInvalidDomain) {
			t.Errorf("normalizeDomain(%q) returned %v, want errInvalidDomain", in, err)
		}
	}
}

func hasChange(changes []string, want string) bool {
	for _, c := range changes {
		if c == want {
			return true
		}
	}
	return false
}
