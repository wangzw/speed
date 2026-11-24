package humanverify

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/internal/configtest"
	"github.com/vislake/speed/pkg/kv"
	"github.com/vislake/speed/pkg/log"
)

// TestTheDescriptorDeliversTheHumanVerifierExclusively checks that the descriptor delivers the human verifier exclusively.
func TestTheDescriptorDeliversTheHumanVerifierExclusively(t *testing.T) {
	m := Module()
	if m.Name != ModuleName {
		t.Errorf("Name = %q, want %q", m.Name, ModuleName)
	}
	token := core.Token((*authn.HumanVerifier)(nil))
	if len(m.Provides) != 1 {
		t.Fatalf("Provides = %+v, want the one verifier", m.Provides)
	}
	if p := m.Provides[0]; p.Token != token || !p.Exclusive {
		t.Errorf("Provides[0] = %+v, want the human-verification seam claimed exclusively", p)
	}
	if len(m.Requires) != 4 {
		t.Fatalf("Requires = %+v, want the three optional dependencies the mechanisms take up plus "+
			"logging", m.Requires)
	}
	var hasLog bool
	for _, requirement := range m.Requires {
		if !requirement.Optional {
			t.Errorf("the requirement %+v is not optional; the off gate starts without it", requirement)
		}
		if requirement.Token == core.Token((*log.Logger)(nil)) {
			hasLog = true
		}
	}
	if !hasLog {
		t.Error("the descriptor takes up no logging capability, so the Turnstile secret could not be redacted")
	}
	if len(m.Resources) != 4 {
		t.Errorf("Resources = %v, want the input-item schema, the proof-of-work and captcha settings "+
			"schemas and the fragment", m.Resources)
	}
}

// TestASecondExclusiveVerifierFailsTheResolution checks that a second exclusive verifier fails resolution.
func TestASecondExclusiveVerifierFailsTheResolution(t *testing.T) {
	reg := registryWithReader(t, body(t, nil))
	reg.Register(Module())
	reg.Register(core.Module{
		Name:     "verifier.other",
		Provides: []core.Provision{{Token: (*authn.HumanVerifier)(nil), Exclusive: true}},
		Prepare: func(context.Context, *core.Registry) (core.Enablement, error) {
			return core.Enablement{State: core.StateEnabled}, nil
		},
		New: func(context.Context, *core.Registry) (any, error) { return otherVerifier{}, nil },
	})
	err := reg.Drive(t.Context(), core.Drive{
		Depth:        core.StagePrepare,
		Participants: []string{ModuleName, "verifier.other", config.ModuleName},
	})
	if !errors.Is(err, core.ErrExclusiveViolated) {
		t.Fatalf("two exclusive verifiers resolved with %v, want ErrExclusiveViolated", err)
	}
}

type otherVerifier struct{}

func (otherVerifier) Required(authn.HumanScope) bool   { return true }
func (otherVerifier) Provider(authn.HumanScope) string { return "other" }
func (otherVerifier) SiteKey(authn.HumanScope) string  { return "" }
func (otherVerifier) Action(authn.HumanScope) string   { return "" }
func (otherVerifier) Verify(context.Context, authn.HumanProof, authn.HumanScope) error {
	return nil
}

const logStandIn = "stand-in.log"

func standInLog() core.Module {
	return core.Module{
		Name:     logStandIn,
		Provides: []core.Provision{{Token: (*log.Logger)(nil)}},
	}
}

type recordingLogger struct {
	patterns map[string]log.Matcher
}

func newRecordingLogger() *recordingLogger {
	return &recordingLogger{patterns: map[string]log.Matcher{}}
}

func (l *recordingLogger) Named(string) *slog.Logger             { return slog.Default() }
func (l *recordingLogger) Redaction() log.Redaction              { return l }
func (l *recordingLogger) AddKeys(...string)                     {}
func (l *recordingLogger) AddPattern(name string, m log.Matcher) { l.patterns[name] = m }

// TestTheTurnstileSecretIsRegisteredAsARedactionRule checks that the Turnstile secret is registered as a literal redaction rule.
func TestTheTurnstileSecretIsRegisteredAsARedactionRule(t *testing.T) {
	const secret = "turnstile-secret-2f7a91c4"
	section := turnstileSection()
	section["secret-key"] = secret
	section[phoneModeItem] = ModeOff

	recorder := newRecordingLogger()
	reg := core.New()
	reg.Register(config.Module())
	reg.Register(Module())
	reg.Register(standInLog())
	reg.Register(standIn(kvStandIn, (*kv.KV)(nil)))
	reg.Supply(configtest.Reader(t, body(t, section), Module()), config.ModuleName)
	reg.Supply(recorder, logStandIn)
	reg.Supply(newStubKV(), kvStandIn)
	err := reg.Drive(t.Context(), core.Drive{
		Depth:        core.StageInit,
		Participants: []string{ModuleName, config.ModuleName, logStandIn, kvStandIn},
		Action:       func(context.Context, *core.Registry) error { return nil },
	})
	if err != nil {
		t.Fatalf("driving the module: %v", err)
	}
	match, ok := recorder.patterns[ModuleName+"."+secretKeyItem]
	if !ok {
		t.Fatalf("no redaction rule was registered under %s.%s", ModuleName, secretKeyItem)
	}
	if spans := match("siteverify rejected secret " + secret + " for the token"); len(spans) == 0 {
		t.Error("the rule does not mask the Turnstile secret")
	}
	if spans := match("nothing here"); len(spans) != 0 {
		t.Errorf("the rule masks a value it should not: %v", spans)
	}
}

// TestTurnstileStartsWithNoLoggingCapability checks that Turnstile starts without a logging capability.
func TestTurnstileStartsWithNoLoggingCapability(t *testing.T) {
	v := driveVerifier(t, body(t, turnstileSection()))
	if v.Provider(authn.HumanScopeOther) != ModeTurnstile {
		t.Errorf("the verifier reports %q, want %q", v.Provider(authn.HumanScopeOther), ModeTurnstile)
	}
}
