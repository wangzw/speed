package saml

import "testing"

func TestOnlyTheKeyFileItemIsSensitive(t *testing.T) {
	s := configSchema()
	key, ok := s.Items[spKeyFileItem]
	if !ok {
		t.Fatalf("the schema does not declare %s", spKeyFileItem)
	}
	if !key.Sensitive {
		t.Errorf("the %s item is not marked sensitive", spKeyFileItem)
	}
	for _, name := range []string{spCertFileItem, spNextCertFileItem} {
		item, ok := s.Items[name]
		if !ok {
			t.Fatalf("the schema does not declare %s", name)
		}
		if item.Sensitive {
			t.Errorf("the %s item is marked sensitive, want the certificate published and not secret", name)
		}
	}
}
