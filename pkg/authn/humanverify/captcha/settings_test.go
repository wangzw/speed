package captcha

import (
	"errors"
	"testing"
)

// TestTheDeclaredGlobalIssueLimitIsTheProductDefault holds the value a
// deployment that configures nothing runs on.
func TestTheDeclaredGlobalIssueLimitIsTheProductDefault(t *testing.T) {
	if DefaultGlobalIssueLimit != 600 {
		t.Errorf("the default global issuance limit is %d, want 600", DefaultGlobalIssueLimit)
	}
}

// TestTheGlobalIssueLimitDomain holds the declared range: one to a million
// challenges a window, inclusive.
func TestTheGlobalIssueLimitDomain(t *testing.T) {
	for _, value := range []int{GlobalIssueLimitMin, 600, GlobalIssueLimitMax} {
		if !globalIssueLimitInDomain(value) {
			t.Errorf("the domain refuses %d", value)
		}
	}
	for _, value := range []int{GlobalIssueLimitMin - 1, GlobalIssueLimitMax + 1, 0, -1} {
		if globalIssueLimitInDomain(value) {
			t.Errorf("the domain admits %d", value)
		}
	}
}

// TestTheFullKeyPathIsTheNamespacedOne holds the read path to the key the
// schema declares, since a store names an item by its full path and a relative
// key would read as an unknown item and silently fall back to the default.
func TestTheFullKeyPathIsTheNamespacedOne(t *testing.T) {
	if globalIssueLimitPath != "authn.humanverify.captcha.global-issue-limit" {
		t.Errorf("globalIssueLimitPath = %q", globalIssueLimitPath)
	}
}

// TestTheSchemaDeclaresTheItem holds the declaration's shape: the namespace the
// key lives under, the item, and the mounted default.
func TestTheSchemaDeclaresTheItem(t *testing.T) {
	s := Schema()
	if s.Namespace != Namespace {
		t.Errorf("the schema namespace is %q, want %q", s.Namespace, Namespace)
	}
	if _, ok := s.Items[GlobalIssueLimitKey]; !ok {
		t.Errorf("the schema declares no %q item", GlobalIssueLimitKey)
	}
	if len(s.Mounts) != 1 {
		t.Fatalf("the schema mounts %+v, want the one defaults struct", s.Mounts)
	}
	mounted, ok := s.Mounts[0].Value.(*settingsOptions)
	if !ok {
		t.Fatalf("the schema mounts %T, want *settingsOptions", s.Mounts[0].Value)
	}
	if mounted.GlobalIssueLimit != DefaultGlobalIssueLimit {
		t.Errorf("the mounted default is %d, want %d", mounted.GlobalIssueLimit, DefaultGlobalIssueLimit)
	}
}

// TestAnUnreadableOrOutOfDomainSettingFallsBackToTheDefault holds the read
// direction: a store that cannot answer, and a value outside the domain, both
// read as the declared default rather than blocking issuance.
func TestAnUnreadableOrOutOfDomainSettingFallsBackToTheDefault(t *testing.T) {
	for _, reader := range []captchaSettings{
		{err: errors.New("the snapshot is not loaded")},
		{global: GlobalIssueLimitMax + 1},
	} {
		p, err := New(newStore(), reader)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		if got := p.globalIssueLimit(); got != DefaultGlobalIssueLimit {
			t.Errorf("globalIssueLimit = %d, want the default %d", got, DefaultGlobalIssueLimit)
		}
	}
	p, err := New(newStore(), captchaSettings{global: 42})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if got := p.globalIssueLimit(); got != 42 {
		t.Errorf("globalIssueLimit = %d, want the configured 42", got)
	}
	if p, err := New(newStore(), nil); err != nil {
		t.Fatalf("New with no reader: %v", err)
	} else if got := p.globalIssueLimit(); got != DefaultGlobalIssueLimit {
		t.Errorf("globalIssueLimit without a reader = %d, want the default %d", got, DefaultGlobalIssueLimit)
	}
}
