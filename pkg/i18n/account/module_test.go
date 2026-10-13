package account

import (
	"context"
	"reflect"
	"slices"
	"testing"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/i18n"
)

// fakeAccounts is the account store the bridge reads through. It embeds the
// interface so a method nobody overrode panics rather than answering, and it
// serves Get from a fixed account and error.
type fakeAccounts struct {
	authn.Accounts
	account authn.Account
	err     error
}

func (f *fakeAccounts) Get(_ context.Context, _ authn.AccountID) (authn.Account, error) {
	return f.account, f.err
}

// The descriptor names the account store as a required dependency and delivers
// the profile and the provisioning hook, neither exclusively.
func TestModuleDescriptorNamesTheStoreAndTheTwoSeams(t *testing.T) {
	m := Module()
	if m.Name != ModuleName {
		t.Errorf("the module is named %q, want %q", m.Name, ModuleName)
	}
	wantRequires := []core.Requirement{{Token: (*authn.Accounts)(nil)}}
	if !slices.Equal(m.Requires, wantRequires) {
		t.Fatalf("the module requires %+v, want the account store required", m.Requires)
	}
	// Non-exclusive: a host may stand its own profile in the engine's place and
	// the login engine takes every provisioner, so Exclusive must stay false.
	wantProvides := []core.Provision{
		{Token: (*i18n.Profile)(nil)},
		{Token: (*authn.Provisioner)(nil)},
	}
	if !slices.Equal(m.Provides, wantProvides) {
		t.Fatalf("the module provides %+v, want the profile and the provisioning hook, each non-exclusive", m.Provides)
	}
}

// LocaleFor reads the account's stored i18n.locale attribute. A malformed
// identifier, an absent account and an attribute that is absent or empty all
// read as none.
func TestLocaleForReadsTheStoredAttribute(t *testing.T) {
	// The account the fake answers with carries both preferences, so a case
	// that must read as "none" fails whenever the bridge skipped the step that
	// keeps it away from that value: the identifier's shape or the lookup's
	// error.
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
	}{
		{"a stored locale reads back", "acct-1", full, nil, i18n.LocaleZH, true},
		{"a malformed identifier reads as none", "not an id!", full, nil, "", false},
		{"an absent account reads as none", "acct-1", full, authn.ErrAccountNotFound, "", false},
		{"an absent attribute reads as none", "acct-1", authn.Account{}, nil, "", false},
		{"an empty attribute reads as none", "acct-1", authn.Account{Attributes: map[string]string{i18n.LocaleAttributeKey: ""}}, nil, "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := &bridge{accounts: &fakeAccounts{account: c.account, err: c.getErr}}
			got, ok := b.LocaleFor(context.Background(), c.id)
			if got != c.want || ok != c.wantOK {
				t.Errorf("LocaleFor(%q) = (%q, %v), want (%q, %v)", c.id, got, ok, c.want, c.wantOK)
			}
		})
	}
}

// TimeZoneFor reads the account's stored i18n.tz attribute, and reads none for
// the same three ways LocaleFor does.
func TestTimeZoneForReadsTheStoredAttribute(t *testing.T) {
	full := authn.Account{Attributes: map[string]string{
		i18n.LocaleAttributeKey:   "zh-CN",
		i18n.TimeZoneAttributeKey: "Asia/Shanghai",
	}}
	cases := []struct {
		name    string
		id      string
		account authn.Account
		getErr  error
		want    string
		wantOK  bool
	}{
		{"a stored zone reads back", "acct-1", full, nil, "Asia/Shanghai", true},
		{"a malformed identifier reads as none", "not an id!", full, nil, "", false},
		{"an absent account reads as none", "acct-1", full, authn.ErrAccountNotFound, "", false},
		{"an absent attribute reads as none", "acct-1", authn.Account{}, nil, "", false},
		{"an empty attribute reads as none", "acct-1", authn.Account{Attributes: map[string]string{i18n.TimeZoneAttributeKey: ""}}, nil, "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := &bridge{accounts: &fakeAccounts{account: c.account, err: c.getErr}}
			got, ok := b.TimeZoneFor(context.Background(), c.id)
			if got != c.want || ok != c.wantOK {
				t.Errorf("TimeZoneFor(%q) = (%q, %v), want (%q, %v)", c.id, got, ok, c.want, c.wantOK)
			}
		})
	}
}

// Provision seeds the locale and time zone the request carried into the account
// about to be created, changes no other field, and leaves the attribute set
// untouched when the context names neither.
func TestProvisionSeedsTheContextPreferences(t *testing.T) {
	localeCtx := i18n.WithLocale(context.Background(), i18n.LocaleZH)
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
			// Every field but Attributes must come back as it went in.
			want := newBase(c.wantAttr)
			if !reflect.DeepEqual(got, want) {
				t.Errorf("Provision returned %+v, want %+v", got, want)
			}
		})
	}
}
