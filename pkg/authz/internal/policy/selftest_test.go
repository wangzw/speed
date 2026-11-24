package policy

import (
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/authz"
)

// TestSelfCheckAcceptsTheModelText checks that self check accepts the model text.
func TestSelfCheckAcceptsTheModelText(t *testing.T) {
	if err := SelfCheck(authz.ModelText); err != nil {
		t.Fatalf("SelfCheck refused the model text: %v", err)
	}
}

// TestSelfCheckRefusesATamperedModel checks that selfCheck refuses a tampered model.
func TestSelfCheckRefusesATamperedModel(t *testing.T) {
	cases := []struct {
		name     string
		text     string
		reported []string
	}{
		{
			name:     "the grant's domain clause is dropped",
			text:     strings.Replace(authz.ModelText, " && r.dom == p.dom", "", 1),
			reported: []string{"a grant in another domain does not apply to a query in this one"},
		},
		{
			name: "the binding's domain argument is dropped from the matcher",
			text: strings.Replace(authz.ModelText, "g(r.sub, p.sub, r.dom)", "g(r.sub, p.sub)", 1),
			reported: []string{
				"a role's grant reaches the account bound to it in the same domain",
				"the same query is answered the same way twice",
			},
		},
		{
			name: "the binding's domain argument is dropped from its definition",
			text: strings.Replace(
				strings.Replace(authz.ModelText, "g = _, _, _", "g = _, _", 1),
				"g(r.sub, p.sub, r.dom)", "g(r.sub, p.sub)", 1),
			reported: []string{"a binding in another domain does not resolve the subject's role here"},
		},
		{
			name:     "the object clause is made always true",
			text:     strings.Replace(authz.ModelText, "r.obj == p.obj", "1 == 1", 1),
			reported: []string{"an object other than the grant's is not matched"},
		},
		{
			name:     "the action clause is made always true",
			text:     strings.Replace(authz.ModelText, "r.act == p.act", "1 == 1", 1),
			reported: []string{"an action other than the grant's is not matched"},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.text == authz.ModelText {
				t.Fatal("the corruption did not change the text, so the check would pass on the model it is meant to change")
			}
			err := SelfCheck(c.text)
			if err == nil {
				t.Fatalf("SelfCheck accepted a model with %s", c.name)
			}
			for _, want := range c.reported {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("SelfCheck reported %q, which does not name the fixture %q it should catch this with",
						err.Error(), want)
				}
			}
		})
	}
}
