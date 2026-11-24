package captcha

import (
	"errors"
	"testing"
)

// TestTheDeclaredGlobalIssueLimitIsTheProductDefault checks that the declared global issue limit is the product default.
func TestTheDeclaredGlobalIssueLimitIsTheProductDefault(t *testing.T) {
	if DefaultGlobalIssueLimit != 600 {
		t.Errorf("the default global issuance limit is %d, want 600", DefaultGlobalIssueLimit)
	}
}

// TestTheGlobalIssueLimitDomain checks the global issue limit domain.
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

// TestTheFullKeyPathIsTheNamespacedOne checks that the full key path is the namespaced one.
func TestTheFullKeyPathIsTheNamespacedOne(t *testing.T) {
	if globalIssueLimitPath != "authn.humanverify.captcha.global-issue-limit" {
		t.Errorf("globalIssueLimitPath = %q", globalIssueLimitPath)
	}
}

// TestTheSchemaDeclaresTheItem checks that the schema declares the item.
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

// TestAnUnreadableOrOutOfDomainSettingFallsBackToTheDefault checks that an unreadable or out-of-domain setting falls back to the default.
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
