package smtp

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/mail"
)

// passwordSentinel is a value no other text in these cases holds, so finding it
// anywhere is the leak this file is about.
const passwordSentinel = "s3cr3t-smtp-password-sentinel-1f2e3d4c"

// TestAConfigurationErrorNeverRepeatsThePassword holds the discipline that a
// module holding a secret keeps it out of the text of every failure on the way
// to a runnable configuration, whichever item the failure actually names.
func TestAConfigurationErrorNeverRepeatsThePassword(t *testing.T) {
	cases := map[string]map[string]any{
		"an unknown tls value":          {"tls": "plaintext", "username": "the-user", "password": passwordSentinel},
		"a sender that does not parse":  {"from": "not-an-address", "username": "the-user", "password": passwordSentinel},
		"an allowed sender that is bad": {"from-allowed": []string{"bad"}, "username": "the-user", "password": passwordSentinel},
	}
	for name, override := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := readBody(t, body(t, section(override)))
			if !errors.Is(err, mail.ErrInvalidConfig) {
				t.Fatalf("the section gave %v, want one wrapping ErrInvalidConfig", err)
			}
			if strings.Contains(err.Error(), passwordSentinel) {
				t.Errorf("the failure %q repeats the password", err)
			}
		})
	}
}

// TestASendErrorNeverRepeatsThePassword holds the other half at send time: a
// relay that refuses the message answers with a code, never with anything this
// module wrote, so the password reaches no error text.
func TestASendErrorNeverRepeatsThePassword(t *testing.T) {
	srv := newFakeServer(t, modePlain, func(s *fakeServer) { s.rejectRCPTCode = "550" })
	s := newTestSender(t, srv, func(cfg *moduleConfig) {
		cfg.Username = "the-user"
		cfg.Password = passwordSentinel
	})

	_, err := s.Send(t.Context(), textOnly())
	if !errors.Is(err, mail.ErrRejected) {
		t.Fatalf("the refused send gave %v, want ErrRejected", err)
	}
	if strings.Contains(err.Error(), passwordSentinel) {
		t.Errorf("the failure %q repeats the password", err)
	}
	// The password did reach the relay, which is the point of AUTH, so its
	// absence from the error is a choice about what is written and not about
	// what is sent.
	if auth := srv.lastAuth(t); auth.authUser != "the-user" || auth.authPass != passwordSentinel {
		t.Errorf("the relay read %q/%q, want the configured identity",
			auth.authUser, auth.authPass)
	}
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

// providingLoggerModule is a logging capability a drive can construct.
func providingLoggerModule(red *capturingRedaction) core.Module {
	return core.Module{
		Name:     "stand-in.logger",
		Provides: []core.Provision{{Token: (*log.Logger)(nil)}},
		New:      func(context.Context, *core.Registry) (any, error) { return &capturingLogger{red}, nil },
	}
}

// TestThePasswordIsRegisteredAsARedactionRule holds that the AUTH secret leaves
// the factory as a literal redaction rule, so a password that turns up in any
// log line of the process is masked.
func TestThePasswordIsRegisteredAsARedactionRule(t *testing.T) {
	red := &capturingRedaction{}
	reg := registryWithReader(t, body(t, section(map[string]any{
		"username": "the-user",
		"password": passwordSentinel,
	})))
	reg.Register(providingLoggerModule(red))
	reg.Register(Module())

	err := reg.Drive(t.Context(), core.Drive{
		Depth:        core.StageInit,
		Participants: []string{ModuleName, config.ModuleName, "stand-in.logger"},
	})
	if err != nil {
		t.Fatalf("driving the module: %v", err)
	}
	if len(red.matches) == 0 {
		t.Fatal("no redaction rule was registered for the password")
	}
	line := "a send carried " + passwordSentinel + " somewhere"
	if spans := red.matches[0](line); len(spans) == 0 {
		t.Errorf("the password is not masked in %q", line)
	}
}
