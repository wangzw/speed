package totp

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/step"
	"github.com/vislake/speed/pkg/core"
)

func enrollFirstStep(t *testing.T, a assembly) string {
	t.Helper()
	var uri string
	err := a.drive(t, func(ctx context.Context, reg *core.Registry, _ *gorm.DB) error {
		m, err := core.Resolve[authn.MFA](reg)
		if err != nil {
			return err
		}
		first, err := m.Enroll(ctx, flow(), authn.FactorTOTP,
			authn.StepInput{Action: authn.InputSubmit, Fields: map[string]string{"kind": "totp"}})
		if err != nil {
			return fmt.Errorf("the first registration step: %w", err)
		}
		if k := step.KindOf(first); k != step.KindData {
			return fmt.Errorf("the first registration step answered %v, want data with the address", k)
		}
		options, ok := step.DataOf(first).(enrollmentOptions)
		if !ok {
			return fmt.Errorf("the registration payload is %T, want enrollmentOptions", step.DataOf(first))
		}
		uri = options.OTPAuthURI
		return nil
	})
	if err != nil {
		t.Fatalf("the drive failed: %v", err)
	}
	return uri
}

type otpauth struct {
	label  string
	issuer string
}

func parseOTPAuth(t *testing.T, uri string) otpauth {
	t.Helper()
	parsed, err := url.Parse(uri)
	if err != nil {
		t.Fatalf("parsing %q: %v", uri, err)
	}
	if parsed.Scheme != "otpauth" || parsed.Host != "totp" {
		t.Fatalf("%q is not a totp otpauth address", uri)
	}
	label, found := strings.CutPrefix(parsed.Path, "/")
	if !found {
		t.Fatalf("%q carries no label", uri)
	}
	return otpauth{label: label, issuer: parsed.Query().Get("issuer")}
}

// TestTheEnrollmentAddressNamesTheAccountByUsername checks that the otpauth label carries the site name and the account's username.
func TestTheEnrollmentAddressNamesTheAccountByUsername(t *testing.T) {
	const site = "Speed Demo"
	got := parseOTPAuth(t, enrollFirstStep(t, assembly{
		key: rootKey, seed: true, register: true, issuer: site,
	}))
	if want := site + ":" + username; got.label != want {
		t.Errorf("the label is %q, want %q", got.label, want)
	}
	if got.issuer != site {
		t.Errorf("the issuer parameter is %q, want the configured %q", got.issuer, site)
	}
}

// TestTheEnrollmentAddressFallsBackToTheAccountIdentifier checks that a missing username labels the account by its identifier.
func TestTheEnrollmentAddressFallsBackToTheAccountIdentifier(t *testing.T) {
	const site = "Speed Demo"
	cases := []struct {
		name     string
		accounts fakeAccounts
	}{
		{
			name: "no username",
			accounts: fakeAccounts{identifiers: map[authn.AccountID][]authn.Identifier{
				acct: {{Kind: authn.IdentifierEmail, Value: "alice@example.test"}},
			}},
		},
		{
			name: "an empty username",
			accounts: fakeAccounts{identifiers: map[authn.AccountID][]authn.Identifier{
				acct: {{Kind: authn.IdentifierUsername, Value: ""}},
			}},
		},
		{
			name:     "the read fails",
			accounts: fakeAccounts{err: errors.New("the account store is down")},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := parseOTPAuth(t, enrollFirstStep(t, assembly{
				key: rootKey, seed: true, register: true, accounts: c.accounts, issuer: site,
			}))
			if want := site + ":" + string(acct); got.label != want {
				t.Errorf("the label is %q, want the account identifier %q", got.label, want)
			}
		})
	}
}
