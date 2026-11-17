package authn_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/admin"
	"github.com/vislake/speed/pkg/authn/profile"
)

// TestTheProvenDescriptionsNameTheProvingWays holds the two identifier views'
// `proven` descriptions to Verification.Proves: each way the method reports
// true is named on the true side of the sentence and only there, and each way
// it reports false is named on the false side and only there. A description
// that named invite among the proofs, or left a proof out, fails here.
func TestTheProvenDescriptionsNameTheProvingWays(t *testing.T) {
	ways := []struct {
		via   string
		named string
	}{
		{authn.VerifiedViaSMSCode, "sms-code"},
		{authn.VerifiedViaEmailCode, "email-code"},
		{authn.VerifiedViaIDPPrefix + "github", "idp:<provider>"},
		{authn.VerifiedViaInvite, "invite"},
		{authn.VerifiedViaAdmin, "admin"},
		{authn.VerifiedViaImportUnverified, "import-unverified"},
		{authn.VerifiedViaSelf, "self"},
	}
	views := []struct {
		name string
		view reflect.Type
	}{
		{"profile", reflect.TypeOf(profile.AuthnProfileIdentifier{})},
		{"admin", reflect.TypeOf(admin.AuthnAdminIdentifier{})},
	}
	for _, view := range views {
		t.Run(view.name, func(t *testing.T) {
			field, ok := view.view.FieldByName("Proven")
			if !ok {
				t.Fatalf("%s view has no Proven field", view.name)
			}
			description := strings.ToLower(field.Tag.Get("description"))
			split := strings.Index(description, "false for")
			if split < 0 {
				t.Fatalf("%s description has no false half: %q", view.name, description)
			}
			trueHalf, falseHalf := description[:split], description[split:]
			for _, way := range ways {
				proves := authn.Verification{Via: way.via}.Proves()
				inTrue := strings.Contains(trueHalf, way.named)
				inFalse := strings.Contains(falseHalf, way.named)
				if inTrue != proves || inFalse == proves {
					t.Errorf(
						"%s description puts %q on the wrong side: Proves()=%v, true half=%v, false half=%v",
						view.name, way.named, proves, inTrue, inFalse,
					)
				}
			}
		})
	}
}
