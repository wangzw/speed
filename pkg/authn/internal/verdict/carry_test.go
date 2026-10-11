package verdict_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn/internal/model"
	"github.com/vislake/speed/pkg/authn/internal/verdict"
)

// A backend hands over every field of a Principal; the gateway reads every one
// back, the impersonation and IdP ones included.
func TestAVerdictCarriesEveryPrincipalField(t *testing.T) {
	p := model.Principal{
		Account: "acct", Actor: "admin", Session: "sess",
		Methods: []model.Method{model.MethodPassword, model.MethodTOTP}, Assurance: model.AssuranceMulti, IDPAssured: true,
		AuthTime: time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC), Scope: "acme", Provider: "prov", Attributes: map[string]string{"k": "v"},
	}
	if got := verdict.PrincipalOf(verdict.NewAuthenticated(p)); !reflect.DeepEqual(got, p) {
		t.Errorf("PrincipalOf = %+v, want %+v", got, p)
	}
	single := model.Principal{Account: "a", Assurance: model.AssuranceSingle, Methods: []model.Method{model.MethodStatic}}
	if got := verdict.PrincipalOf(verdict.NewAuthenticated(single)); !reflect.DeepEqual(got, single) {
		t.Errorf("PrincipalOf = %+v, want %+v", got, single)
	}
}
