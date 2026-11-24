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

const passwordSentinel = "s3cr3t-smtp-password-sentinel-1f2e3d4c"

// TestAConfigurationErrorNeverRepeatsThePassword checks that no configuration
// failure repeats the password.
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

// TestASendErrorNeverRepeatsThePassword checks that a refused send names a reply
// code and never the password, though AUTH did carry it to the relay.
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
	if auth := srv.lastAuth(t); auth.authUser != "the-user" || auth.authPass != passwordSentinel {
		t.Errorf("the relay read %q/%q, want the configured identity",
			auth.authUser, auth.authPass)
	}
}

type capturingRedaction struct {
	names   []string
	matches []log.Matcher
}

func (c *capturingRedaction) AddKeys(...string) {}

func (c *capturingRedaction) AddPattern(name string, match log.Matcher) {
	c.names = append(c.names, name)
	c.matches = append(c.matches, match)
}

type capturingLogger struct{ red *capturingRedaction }

func (l *capturingLogger) Named(string) *slog.Logger { return slog.Default() }

func (l *capturingLogger) Redaction() log.Redaction { return l.red }

func providingLoggerModule(red *capturingRedaction) core.Module {
	return core.Module{
		Name:     "stand-in.logger",
		Provides: []core.Provision{{Token: (*log.Logger)(nil)}},
		New:      func(context.Context, *core.Registry) (any, error) { return &capturingLogger{red}, nil },
	}
}

// TestThePasswordIsRegisteredAsARedactionRule checks that the AUTH secret becomes
// a literal redaction rule that masks it in a log line.
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
