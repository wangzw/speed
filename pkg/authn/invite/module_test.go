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
	"github.com/vislake/speed/pkg/log"
)

// The instance a host builds has to satisfy the capability the module claims.
var _ Invites = (*product)(nil)

func TestTheDescriptorNamesWhatItRequiresAndDelivers(t *testing.T) {
	m := Module()
	if m.Name != ModuleName || ModuleName != "authn.invite" {
		t.Errorf("module name = %q (constant %q), want authn.invite", m.Name, ModuleName)
	}
	if len(m.Provides) != 1 || m.Provides[0].Token != (*Invites)(nil) || !m.Provides[0].Exclusive {
		t.Errorf("Provides = %+v, want the Invites capability delivered exclusively", m.Provides)
	}

	wantRequired := []core.Token{
		(*db.Database)(nil), (*mount.Mounter)(nil), (*authn.Accounts)(nil),
		(*authn.AccountAdmin)(nil), (*authn.Memberships)(nil), (*audit.Audit)(nil),
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

func TestTheConfiguredLifetimeHasADomain(t *testing.T) {
	if _, err := (moduleConfig{TTL: DefaultTTL}).resolve(); err != nil {
		t.Errorf("the declared default %s is refused: %v", DefaultTTL, err)
	}
	for _, bad := range []time.Duration{0, -time.Second, minTTL - time.Second, maxTTL + time.Second} {
		if _, err := (moduleConfig{TTL: bad}).resolve(); !errors.Is(err, authn.ErrInvalidConfig) {
			t.Errorf("lifetime %s gave %v, want ErrInvalidConfig", bad, err)
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

func TestTheConfigSchemaDeclaresTheOneKey(t *testing.T) {
	s := schema()
	if s.Namespace != ConfigNamespace {
		t.Errorf("namespace = %q, want %q", s.Namespace, ConfigNamespace)
	}
	if _, ok := s.Items[ttlItem]; !ok {
		t.Errorf("the schema does not declare %q", ttlItem)
	}
}
