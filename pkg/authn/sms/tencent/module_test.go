package tencent

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/log"
)

// driveSender assembles the module the way a host does, with the real
// configuration loader, and returns the sender it delivers.
func driveSender(t *testing.T, document string) *sender {
	t.Helper()
	reader := authntest.ConfigReader(t, Module(), document)
	reg := core.New()
	reg.Register(config.Module())
	reg.Supply(reader, config.ModuleName)
	reg.Register(Module())
	var found *sender
	err := reg.Drive(context.Background(), core.Drive{
		Depth:        core.StageInit,
		Participants: []string{ModuleName},
		Action: func(_ context.Context, reg *core.Registry) error {
			v, err := core.Resolve[authn.SMSSender](reg)
			if err != nil {
				return err
			}
			found, _ = v.(*sender)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("driving the module: %v", err)
	}
	if found == nil {
		t.Fatal("the module delivered no sender")
	}
	return found
}

// The module registers under its name and delivers a sender that reports the
// carrier it is.
func TestTheModuleDeliversTheSenderNamedTencent(t *testing.T) {
	s := driveSender(t, body(t, section(validTemplates(), nil)))
	if got := s.Name(); got != "tencent" {
		t.Errorf("the sender reports %q, want tencent", got)
	}
	if ModuleName != "authn.sms.tencent" || ConfigNamespace != "authn.sms.tencent" {
		t.Errorf("the module name %q and namespace %q, want authn.sms.tencent", ModuleName, ConfigNamespace)
	}
}

// The claim is exclusive: a run that also holds another carrier fails the
// resolution rather than letting two senders stand.
func TestASecondExclusiveCarrierFailsTheResolution(t *testing.T) {
	reader := authntest.ConfigReader(t, Module(), body(t, section(validTemplates(), nil)))
	reg := core.New()
	reg.Register(config.Module())
	reg.Supply(reader, config.ModuleName)
	reg.Register(Module())
	reg.Register(core.Module{
		Name:     "carrier.other",
		Provides: []core.Provision{{Token: (*authn.SMSSender)(nil), Exclusive: true}},
		Prepare: func(context.Context, *core.Registry) (core.Enablement, error) {
			return core.Enablement{State: core.StateEnabled}, nil
		},
		New: func(context.Context, *core.Registry) (any, error) { return otherCarrier{}, nil },
	})
	err := reg.Drive(context.Background(), core.Drive{
		Depth:        core.StagePrepare,
		Participants: []string{ModuleName, "carrier.other"},
	})
	if !errors.Is(err, core.ErrExclusiveViolated) {
		t.Fatalf("two exclusive carriers resolved with %v, want ErrExclusiveViolated", err)
	}
}

// otherCarrier is a stand-in carrying the other exclusive claim.
type otherCarrier struct{}

func (otherCarrier) Name() string { return "other" }

func (otherCarrier) Send(context.Context, authn.SMS) (authn.SendReceipt, error) {
	return authn.SendReceipt{}, nil
}

// capturingRedaction records the rules registered with it.
type capturingRedaction struct {
	names   []string
	matches []log.Matcher
}

func (c *capturingRedaction) AddKeys(...string) {}

func (c *capturingRedaction) AddPattern(name string, match log.Matcher) {
	c.names = append(c.names, name)
	c.matches = append(c.matches, match)
}

// capturingLogger is a logging capability that records redaction registrations.
type capturingLogger struct{ red *capturingRedaction }

func (l *capturingLogger) Named(string) *slog.Logger { return slog.Default() }

func (l *capturingLogger) Redaction() log.Redaction { return l.red }

// The credentials are registered as literal redaction rules, so a credential
// that turns up in any log line of the process is masked.
func TestTheCredentialsAreRegisteredAsRedactionRules(t *testing.T) {
	red := &capturingRedaction{}
	reg := core.New()
	reg.Register(core.Module{
		Name:     "stand-in.logger",
		Provides: []core.Provision{{Token: (*log.Logger)(nil)}},
		New:      func(context.Context, *core.Registry) (any, error) { return &capturingLogger{red}, nil },
	})
	reg.Supply(&capturingLogger{red}, "stand-in.logger")

	registerSecrets(reg, testConfig())

	if len(red.matches) == 0 {
		t.Fatal("no redaction rule was registered for the credentials")
	}
	match := red.matches[0]
	for _, secret := range []string{testConfig().secretID, testConfig().secretKey} {
		line := "a request carried " + secret + " somewhere"
		if spans := match(line); len(spans) == 0 {
			t.Errorf("the credential %q is not masked in %q", secret, line)
		}
	}
}

// With no logging capability the registration is skipped, not failed: a module
// without logs has no log to protect.
func TestWithoutLoggingTheRegistrationIsSkipped(t *testing.T) {
	reg := core.New()
	registerSecrets(reg, testConfig()) // must not panic
}
