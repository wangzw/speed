package mfa

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
)

type tenantRequirementStub struct {
	required bool
	err      error
	asked    []authn.AccountID
}

func (s *tenantRequirementStub) RequiresMFA(_ context.Context, id authn.AccountID) (bool, error) {
	s.asked = append(s.asked, id)
	return s.required, s.err
}

// TestRequireAsksWhetherATenantRequiresMultiFactor checks the tenant branch: a requirement makes the account require one, an unreadable one counts as required, and no seam leaves the account and deployment policies.
func TestRequireAsksWhetherATenantRequiresMultiFactor(t *testing.T) {
	cases := []struct {
		name     string
		required bool
		err      error
		want     authn.MFAOutcome
	}{
		{"a tenant that requires multi-factor asks for a registration", true, nil, authn.MFAEnroll},
		{"a tenant that does not leaves the login single", false, nil, authn.MFANone},
		{"a requirement that cannot be read is a requirement", false, errors.New("settings unavailable"), authn.MFAEnroll},
	}
	for i, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			onEachDialect(t, []authn.FactorKind{authn.FactorTOTP}, func(t *testing.T, r *rig) {
				stub := &tenantRequirementStub{required: c.required, err: c.err}
				r.p.tenantMFA = stub
				account := authn.AccountID(fmt.Sprintf("tenant-%d", i))
				got, offered, err := r.p.Require(context.Background(), account, account, byPassword)
				if err != nil {
					t.Fatalf("Require: %v", err)
				}
				if got != c.want {
					t.Errorf("Require = %v, want %v", got, c.want)
				}
				if len(offered) != 0 {
					t.Errorf("the list offers %+v for an enrollment answer, want none", offered)
				}
				if len(stub.asked) != 1 || stub.asked[0] != account {
					t.Errorf("the seam was asked %v, want the requirement account %q once", stub.asked, account)
				}
			})
		})
	}
}

// TestRequireDoesNotAskATenantWhenTheAccountHasAFactor checks that an account with an active factor is decided before the tenant seam.
func TestRequireDoesNotAskATenantWhenTheAccountHasAFactor(t *testing.T) {
	onEachDialect(t, []authn.FactorKind{authn.FactorTOTP}, func(t *testing.T, r *rig) {
		stub := &tenantRequirementStub{required: true}
		r.p.tenantMFA = stub
		r.activeFactor("holder", authn.FactorTOTP, time.Hour)

		got, offered, err := r.p.Require(context.Background(), "holder", "holder", byPassword)
		if err != nil {
			t.Fatalf("Require: %v", err)
		}
		if got != authn.MFAFactor || len(offered) != 1 {
			t.Errorf("Require = %v with %+v, want MFAFactor with the account's factor", got, offered)
		}
		if len(stub.asked) != 0 {
			t.Errorf("the tenant seam was asked %v for an account with a factor, want no read", stub.asked)
		}
	})
}

// TestRequireKeepsTodayBehaviourWithoutTheSeam checks that an assembly without the tenant seam asks only the account flag and deployment switch.
func TestRequireKeepsTodayBehaviourWithoutTheSeam(t *testing.T) {
	onEachDialect(t, []authn.FactorKind{authn.FactorTOTP}, func(t *testing.T, r *rig) {
		if r.p.tenantMFA != nil {
			t.Fatal("the rig wired a tenant seam, so the absence cannot be tested")
		}
		got, _, err := r.p.Require(context.Background(), "bare", "bare", byPassword)
		if err != nil {
			t.Fatalf("Require: %v", err)
		}
		if got != authn.MFANone {
			t.Errorf("Require = %v, want MFANone: neither the account nor the deployment requires a factor", got)
		}
		r.accounts.required["flagged"] = true
		if got, _, err = r.p.Require(context.Background(), "flagged", "flagged", byPassword); err != nil || got != authn.MFAEnroll {
			t.Errorf("an account that requires a factor: Require = %v, %v, want MFAEnroll", got, err)
		}
	})
}

// TestRequireAsksTheTenantsOfTheRequirementAccount checks that the tenant seam is asked about the requirement account, not the operator's.
func TestRequireAsksTheTenantsOfTheRequirementAccount(t *testing.T) {
	onEachDialect(t, []authn.FactorKind{authn.FactorTOTP}, func(t *testing.T, r *rig) {
		stub := &tenantRequirementStub{required: true}
		r.p.tenantMFA = stub
		got, _, err := r.p.Require(context.Background(), "operator", "target", byPassword)
		if err != nil {
			t.Fatalf("Require: %v", err)
		}
		if got != authn.MFAEnroll {
			t.Errorf("Require = %v, want MFAEnroll", got)
		}
		if len(stub.asked) != 1 || stub.asked[0] != "target" {
			t.Errorf("the seam was asked %v, want the requirement account %q", stub.asked, "target")
		}
	})
}
