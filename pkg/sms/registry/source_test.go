package registry

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/sms"
)

// TestARuntimeRowWhoseNameTheConfigurationAlsoHoldsStopsTheLoad checks that a
// runtime row whose name the configuration also holds fails the load.
func TestARuntimeRowWhoseNameTheConfigurationAlsoHoldsStopsTheLoad(t *testing.T) {
	forEachDialect(t, func(t *testing.T, database db.Database, _ string) {
		cfg := settingsOf(t, map[string]sms.TemplateSpec{
			"authn.code.login": spec([]string{"code"}, map[string]string{"zh-CN": "SMS_1"}),
		}, time.Minute)
		r := newRig(t, database, newProvider(), cfg)
		r.plant("authn.code.login", []string{"code"}, map[string]string{"zh-CN": "SMS_9"},
			map[string]sms.Approval{"zh-CN": {State: sms.ApprovalApproved}}, true, base)

		err := r.s.load(context.Background())
		if err == nil {
			t.Fatal("a name the configuration and the table both hold loaded without a failure")
		}
		if !strings.Contains(err.Error(), "authn.code.login") {
			t.Fatalf("the failure does not name the template in both places: %v", err)
		}
	})
}

// TestOfferRefusesAHeldLanguageWithoutReachingTheVendorOnAStaleSnapshot checks that a conflict is
// decided against the table, not only the stale snapshot, before the vendor is reached.
func TestOfferRefusesAHeldLanguageWithoutReachingTheVendorOnAStaleSnapshot(t *testing.T) {
	forEachDialect(t, func(t *testing.T, database db.Database, _ string) {
		provider := newProvider()
		r := newRig(t, database, provider, settingsOf(t, nil, time.Minute))
		r.load()
		r.plant("promo", []string{"code"}, map[string]string{"zh-CN": "SMS_9"},
			map[string]sms.Approval{"zh-CN": {State: sms.ApprovalApproved}}, true, base)

		_, err := r.s.Offer(context.Background(), sms.Submission{
			Name:    "promo",
			Content: "your code is ${code}",
			Kind:    sms.KindVerification,
			Locale:  "zh-CN",
			Params:  []string{"code"},
		})
		if !errors.Is(err, sms.ErrTemplateExists) {
			t.Fatalf("offering a language the table already holds answered %v, want ErrTemplateExists", err)
		}
		if subs := provider.submissions(); len(subs) != 0 {
			t.Fatalf("the vendor was reached %d times for a submission the table already holds", len(subs))
		}
	})
}
