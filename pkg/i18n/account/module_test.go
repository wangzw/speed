package account

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/i18n"
)

type fakeAccounts struct {
	authn.Accounts
	account authn.Account
	err     error
}

func (f *fakeAccounts) Get(_ context.Context, _ authn.AccountID) (authn.Account, error) {
	return f.account, f.err
}

// TestModuleDescriptorNamesTheStoreAndTheTwoSeams checks that the descriptor
// requires the account store and delivers the profile and the provisioning hook.
func TestModuleDescriptorNamesTheStoreAndTheTwoSeams(t *testing.T) {
	m := Module()
	if m.Name != ModuleName {
		t.Errorf("the module is named %q, want %q", m.Name, ModuleName)
	}
	wantRequires := []core.Requirement{{Token: (*authn.Accounts)(nil)}}
	if !slices.Equal(m.Requires, wantRequires) {
		t.Fatalf("the module requires %+v, want the account store required", m.Requires)
	}
	wantProvides := []core.Provision{
		{Token: (*i18n.Profile)(nil)},
		{Token: (*authn.Provisioner)(nil)},
	}
	if !slices.Equal(m.Provides, wantProvides) {
		t.Fatalf("the module provides %+v, want the profile and the provisioning hook, each non-exclusive", m.Provides)
	}
}

// TestLocaleForReadsTheStoredAttribute checks that LocaleFor reads the stored
// attribute and reports each missing case as none.
func TestLocaleForReadsTheStoredAttribute(t *testing.T) {
	full := authn.Account{Attributes: map[string]string{
		i18n.LocaleAttributeKey:   "zh-CN",
		i18n.TimeZoneAttributeKey: "Asia/Shanghai",
	}}
	cases := []struct {
		name    string
		id      string
		account authn.Account
		getErr  error
		want    i18n.Locale
		wantOK  bool
		wantErr bool
	}{
		{"a stored locale reads back", "acct-1", full, nil, i18n.Locale("zh-CN"), true, false},
		{"a malformed identifier reads as none", "not an id!", full, nil, "", false, false},
		{"an absent account reads as none", "acct-1", full, authn.ErrAccountNotFound, "", false, false},
		{"a store failure reports a failed read", "acct-1", full, errors.New("the account store is unavailable"), "", false, true},
		{"an absent attribute reads as none", "acct-1", authn.Account{}, nil, "", false, false},
		{"an empty attribute reads as none", "acct-1", authn.Account{Attributes: map[string]string{i18n.LocaleAttributeKey: ""}}, nil, "", false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := &bridge{accounts: &fakeAccounts{account: c.account, err: c.getErr}}
			got, ok, err := b.LocaleFor(context.Background(), c.id)
			if got != c.want || ok != c.wantOK || (err != nil) != c.wantErr {
				t.Errorf("LocaleFor(%q) = (%q, %v, %v), want (%q, %v, err=%t)",
					c.id, got, ok, err, c.want, c.wantOK, c.wantErr)
			}
		})
	}
}

// TestTimeZoneForReadsTheStoredAttribute checks that TimeZoneFor reads the
// stored attribute and reports a store failure.
func TestTimeZoneForReadsTheStoredAttribute(t *testing.T) {
	full := authn.Account{Attributes: map[string]string{
		i18n.LocaleAttributeKey:   "zh-CN",
		i18n.TimeZoneAttributeKey: "Asia/Shanghai",
	}}
	storeErr := errors.New("the account store is unreachable")
	cases := []struct {
		name    string
		id      string
		account authn.Account
		getErr  error
		want    string
		wantOK  bool
		wantErr error
	}{
		{"a stored zone reads back", "acct-1", full, nil, "Asia/Shanghai", true, nil},
		{"a malformed identifier reads as none", "not an id!", full, nil, "", false, nil},
		{"an absent account reads as none", "acct-1", full, authn.ErrAccountNotFound, "", false, nil},
		{"an absent attribute reads as none", "acct-1", authn.Account{}, nil, "", false, nil},
		{"an empty attribute reads as none", "acct-1", authn.Account{Attributes: map[string]string{i18n.TimeZoneAttributeKey: ""}}, nil, "", false, nil},
		{"a store failure is reported", "acct-1", full, storeErr, "", false, storeErr},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := &bridge{accounts: &fakeAccounts{account: c.account, err: c.getErr}}
			got, ok, err := b.TimeZoneFor(context.Background(), c.id)
			if got != c.want || ok != c.wantOK || !errors.Is(err, c.wantErr) {
				t.Errorf("TimeZoneFor(%q) = (%q, %v, %v), want (%q, %v, %v)",
					c.id, got, ok, err, c.want, c.wantOK, c.wantErr)
			}
		})
	}
}

// TestProvisionSeedsTheContextPreferences checks that Provision seeds the
// context's locale and zone and leaves every other field alone.
func TestProvisionSeedsTheContextPreferences(t *testing.T) {
	localeCtx := i18n.WithLocale(context.Background(), i18n.Locale("zh-CN"))
	zoneCtx := i18n.WithTimeZone(context.Background(), "Asia/Shanghai")
	bothCtx := i18n.WithTimeZone(localeCtx, "Asia/Shanghai")

	newBase := func(attrs map[string]string) authn.CreateAccount {
		return authn.CreateAccount{
			ID:                 "acct-1",
			Kind:               authn.KindUser,
			DisplayName:        "Ada",
			Username:           "ada",
			Email:              "ada@example.com",
			EmailVerifiedVia:   authn.Verification{Via: authn.VerifiedViaAdmin},
			Phone:              "+15551234567",
			PhoneVerifiedVia:   authn.Verification{Via: authn.VerifiedViaAdmin},
			Password:           "hunter2",
			PasswordHash:       "$argon2id$v=19$m=1,t=1,p=1$c2FsdA$aGFzaA",
			MustChangePassword: true,
			MFARequired:        true,
			Attributes:         attrs,
			Via:                "signup",
		}
	}
	cases := []struct {
		name     string
		ctx      context.Context
		baseAttr map[string]string
		wantAttr map[string]string
	}{
		{
			"both preferences are seeded", bothCtx, nil,
			map[string]string{i18n.LocaleAttributeKey: "zh-CN", i18n.TimeZoneAttributeKey: "Asia/Shanghai"},
		},
		{
			"only the locale is seeded", localeCtx, nil,
			map[string]string{i18n.LocaleAttributeKey: "zh-CN"},
		},
		{
			"only the zone is seeded", zoneCtx, nil,
			map[string]string{i18n.TimeZoneAttributeKey: "Asia/Shanghai"},
		},
		{"a context naming neither leaves a nil set nil", context.Background(), nil, nil},
		{
			"a context naming neither leaves an existing set alone", context.Background(),
			map[string]string{"host.plan": "pro"},
			map[string]string{"host.plan": "pro"},
		},
		{
			"seeding keeps the attributes already there", bothCtx,
			map[string]string{"host.plan": "pro"},
			map[string]string{"host.plan": "pro", i18n.LocaleAttributeKey: "zh-CN", i18n.TimeZoneAttributeKey: "Asia/Shanghai"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := &bridge{accounts: &fakeAccounts{}}
			got, err := b.Provision(c.ctx, newBase(c.baseAttr), "signup")
			if err != nil {
				t.Fatalf("Provision returned an error: %v", err)
			}
			want := newBase(c.wantAttr)
			if !reflect.DeepEqual(got, want) {
				t.Errorf("Provision returned %+v, want %+v", got, want)
			}
		})
	}
}
