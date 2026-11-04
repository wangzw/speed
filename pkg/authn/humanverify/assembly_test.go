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

// The descriptor claims the human-verification seam exclusively, so a command
// names this module or a verifier of its own, never both.
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

// A run that also holds another verifier claiming the seam exclusively fails
// the resolution rather than letting two gates stand.
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

// otherVerifier is a stand-in carrying the other exclusive claim.
type otherVerifier struct{}

func (otherVerifier) Required(authn.HumanScope) bool   { return true }
func (otherVerifier) Provider(authn.HumanScope) string { return "other" }
func (otherVerifier) SiteKey(authn.HumanScope) string  { return "" }
func (otherVerifier) Action(authn.HumanScope) string   { return "" }
func (otherVerifier) Verify(context.Context, authn.HumanProof, authn.HumanScope) error {
	return nil
}

// The logging capability's stand-in, with which the Turnstile secret is
// registered as a redaction rule.
const logStandIn = "stand-in.log"

// standInLog declares the logging capability the way the log module does.
func standInLog() core.Module {
	return core.Module{
		Name:     logStandIn,
		Provides: []core.Provision{{Token: (*log.Logger)(nil)}},
	}
}

// recordingLogger is a logging capability that records the redaction rules
// registered with it.
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

// The Turnstile secret is registered with the logging capability as a literal
// rule, so it is masked wherever it turns up in a log line, and a build with no
// logging capability still starts.
func TestTheTurnstileSecretIsRegisteredAsARedactionRule(t *testing.T) {
	const secret = "turnstile-secret-2f7a91c4"
	section := turnstileSection()
	section["secret-key"] = secret
	// Turnstile needs the key-value store it records spent tokens in, so the
	// case supplies one; the secret is what the case is about.
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

// An assembly with no logging capability still starts the Turnstile mode, with
// nothing to register the secret with.
func TestTurnstileStartsWithNoLoggingCapability(t *testing.T) {
	v := driveVerifier(t, body(t, turnstileSection()))
	if v.Provider(authn.HumanScopeOther) != ModeTurnstile {
		t.Errorf("the verifier reports %q, want %q", v.Provider(authn.HumanScopeOther), ModeTurnstile)
	}
}
