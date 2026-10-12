package wechat

import (
	"testing"

	"github.com/vislake/speed/pkg/authn"
)

// TestAUnionIDIsTheSubjectAndTheOpenIDIsTheAlias holds the key-upgrade rule: an
// identity with a union id is bound under the union id and offers the open-id
// key as an alias, so a binding made under the old key is upgraded in place.
func TestAUnionIDIsTheSubjectAndTheOpenIDIsTheAlias(t *testing.T) {
	c := New(Options{AppID: testAppID, AppSecret: testAppSecret})
	p := c.Proof(Identity{OpenID: "OPEN-9", UnionID: "UNION-9"})

	if p.External == nil {
		t.Fatal("the proof carries no external identity")
	}
	if p.External.Provider != "wechat" || p.External.Subject != "UNION-9" {
		t.Errorf("external = {%q %q}, want wechat / UNION-9", p.External.Provider, p.External.Subject)
	}
	if p.External.DisplayName != "微信用户" {
		t.Errorf("display name = %q, want 微信用户", p.External.DisplayName)
	}
	if p.External.Email != "" || p.External.EmailVerified {
		t.Errorf("a WeChat identity must carry no email, got %+v", p.External)
	}
	if len(p.Methods) != 1 || p.Methods[0] != authn.MethodOAuth {
		t.Errorf("methods = %v, want [oauth]", p.Methods)
	}
	if len(p.Aliases) != 1 {
		t.Fatalf("aliases = %+v, want exactly the open-id key", p.Aliases)
	}
	wantKey := ExternalKey(testAppID, "OPEN-9")
	if p.Aliases[0] != wantKey {
		t.Errorf("alias = %+v, want %+v", p.Aliases[0], wantKey)
	}
	if wantKey.Provider != "wechat-openid:"+testAppID || wantKey.Subject != "OPEN-9" {
		t.Errorf("the open-id key = %+v, want the application namespace and the open id", wantKey)
	}
}

// TestWithoutAUnionIDTheSubjectIsTheOpenIDKey holds the other half: no union id
// means the subject is the open-id key and there is no alias.
func TestWithoutAUnionIDTheSubjectIsTheOpenIDKey(t *testing.T) {
	c := New(Options{AppID: testAppID, AppSecret: testAppSecret})
	p := c.Proof(Identity{OpenID: "OPEN-9"})

	if p.External == nil {
		t.Fatal("the proof carries no external identity")
	}
	if p.External.Provider != "wechat-openid:"+testAppID || p.External.Subject != "OPEN-9" {
		t.Errorf("external = {%q %q}, want wechat-openid:%s / OPEN-9",
			p.External.Provider, p.External.Subject, testAppID)
	}
	if len(p.Aliases) != 0 {
		t.Errorf("aliases = %+v, want none without a union id", p.Aliases)
	}
}

// TestTwoApplicationsDoNotCollide holds that the open-id key carries the
// application identifier, so two applications' open ids are different keys.
func TestTwoApplicationsDoNotCollide(t *testing.T) {
	a := ExternalKey("app-a", "OPEN-1")
	b := ExternalKey("app-b", "OPEN-1")
	if a == b {
		t.Errorf("the same open id under two applications gave one key %+v", a)
	}
}
