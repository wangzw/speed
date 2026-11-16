package invite

import (
	"errors"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/mount"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/kv"
	"github.com/vislake/speed/pkg/log"
)

// The instance a host builds has to satisfy the capability the module claims.
var _ Invites = (*product)(nil)

func TestTheDescriptorNamesWhatItRequiresAndDelivers(t *testing.T) {
	m := Module()
	if m.Name != ModuleName || ModuleName != "authn.invite" {
		t.Errorf("module name = %q (constant %q), want authn.invite", m.Name, ModuleName)
	}
	if len(m.Provides) != 2 {
		t.Errorf("Provides = %+v, want the Invites capability and the invitation revoker delivered exclusively", m.Provides)
	}
	providesInvites, providesRevoker := false, false
	for _, p := range m.Provides {
		if !p.Exclusive {
			t.Errorf("provision %v is not exclusive", p.Token)
		}
		switch p.Token {
		case (*Invites)(nil):
			providesInvites = true
		case (*authn.InvitationRevoker)(nil):
			providesRevoker = true
		}
	}
	if !providesInvites || !providesRevoker {
		t.Errorf("Provides = %+v, want Invites and authn.InvitationRevoker", m.Provides)
	}

	wantRequired := []core.Token{
		(*db.Database)(nil), (*mount.Mounter)(nil), (*kv.KV)(nil), (*authn.Accounts)(nil),
		(*authn.SignupVerification)(nil), (*authn.Memberships)(nil),
		(*authn.TenantDescriptionResolver)(nil),
		(*audit.Audit)(nil),
	}
	var required []core.Token
	optionalLogger := false
	for _, r := range m.Requires {
		if r.Optional {
			if r.Token == (*log.Logger)(nil) {
				optionalLogger = true
			}
			continue
		}
		required = append(required, r.Token)
	}
	if len(required) != len(wantRequired) {
		t.Fatalf("Requires = %+v, want the required capabilities %v", m.Requires, wantRequired)
	}
	for i, tk := range wantRequired {
		if required[i] != tk {
			t.Errorf("Required[%d] = %v, want %v", i, required[i], tk)
		}
	}
	if !optionalLogger {
		t.Error("the logger is not the one optional requirement")
	}
}

// TestTheDescriptorRequiresTheTenantDescriptionResolver holds the dependency:
// the issuance refuses the system tenant and the public link names the
// organization through this one read, so an assembly without it cannot serve
// the module's contract. The roster's catalog delivers it exclusively, so it is
// a required capability rather than one taken up optionally.
func TestTheDescriptorRequiresTheTenantDescriptionResolver(t *testing.T) {
	found := false
	for _, r := range Module().Requires {
		if r.Token != (*authn.TenantDescriptionResolver)(nil) {
			continue
		}
		found = true
		if r.Optional {
			t.Error("the tenant description resolver is optional, but the module cannot refuse the " +
				"system tenant or name an organization without it")
		}
	}
	if !found {
		t.Error("Requires does not name authn.TenantDescriptionResolver")
	}
}

func TestTheLifetimeSettingHasADomain(t *testing.T) {
	if !ttlInDomain(DefaultTTL) {
		t.Errorf("the declared default %s is outside the domain", DefaultTTL)
	}
	for _, bad := range []time.Duration{0, -time.Second, minTTL - time.Second, maxTTL + time.Second} {
		if ttlInDomain(bad) {
			t.Errorf("lifetime %s reads as inside the domain", bad)
		}
	}
}

func TestTheHostVisibleValuesAreFrozen(t *testing.T) {
	if tokenPrefix != "spd_inv_" {
		t.Errorf("the token prefix is %q, want spd_inv_", tokenPrefix)
	}
	if SourceInvite != "invite" {
		t.Errorf("the membership source is %q, want invite", SourceInvite)
	}
	if VerifiedViaInvite != "invite" {
		t.Errorf("the verification channel is %q, want invite", VerifiedViaInvite)
	}
	if ttlKey != "authn.invite.ttl" {
		t.Errorf("the configuration key is %q, want authn.invite.ttl", ttlKey)
	}
}

func TestStatusValidAndString(t *testing.T) {
	for _, s := range []InvitationStatus{InvitationPending, InvitationAccepted, InvitationRevoked} {
		if !s.Valid() {
			t.Errorf("%d is not Valid", s)
		}
	}
	if InvitationStatus(9).Valid() {
		t.Error("a value outside the set reads valid")
	}
	if got := InvitationRevoked.String(); got != "revoked" {
		t.Errorf("InvitationRevoked.String() = %q, want revoked", got)
	}
	if got := InvitationStatus(9).String(); got != "unknown" {
		t.Errorf("an out-of-set status reads %q, want unknown", got)
	}
}

func TestNormalizeEmailBounds(t *testing.T) {
	long := make([]byte, 0, emailMaxBytes+1)
	for i := 0; i < emailMaxBytes; i++ {
		long = append(long, 'a')
	}
	long = append(long, '@')
	if _, err := normalizeEmail(string(long)); !errors.Is(err, ErrInvalidEmail) {
		t.Errorf("an over-long address gave %v, want ErrInvalidEmail", err)
	}
	if _, err := normalizeEmail("a b@example.com"); !errors.Is(err, ErrInvalidEmail) {
		t.Errorf("an address with a space gave %v, want ErrInvalidEmail", err)
	}
	if got, err := normalizeEmail("  Mixed@Example.COM "); err != nil || got != "mixed@example.com" {
		t.Errorf("normalizeEmail = %q, %v; want mixed@example.com", got, err)
	}
}

func TestTheSettingsSchemaDeclaresTheOneKey(t *testing.T) {
	s := settingsSchema()
	if s.Namespace != ConfigNamespace {
		t.Errorf("namespace = %q, want %q", s.Namespace, ConfigNamespace)
	}
	if _, ok := s.Items[ttlItem]; !ok {
		t.Errorf("the schema does not declare %q", ttlItem)
	}
}
