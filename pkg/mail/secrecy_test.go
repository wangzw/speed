package mail

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/log"
)

// TestARejectionNeverRepeatsTheMessage holds the discipline that this package
// names the field at fault and never the value: a caller's address, subject or
// body must not survive into the text of the failure, whichever rule refused it.
func TestARejectionNeverRepeatsTheMessage(t *testing.T) {
	const (
		badAddr = "s3cr3t-recipient-sentinel"
		subject = "s3cr3t-subject-sentinel"
		body    = "s3cr3t-body-sentinel"
		attach  = "s3cr3t-attachment-sentinel"
	)
	cases := map[string]Message{
		"no recipient":        {Subject: subject, Text: body},
		"a malformed address": {To: []string{badAddr}, Subject: subject, Text: body},
		"a header-writing subject": {
			To: []string{"a@b.com"}, Subject: subject + "\r\nBcc: " + badAddr, Text: body,
		},
		"both bodies empty": {To: []string{"a@b.com"}, Subject: subject},
		"a header-writing attachment name": {
			To: []string{"a@b.com"}, Subject: subject, Text: body,
			Attachments: []Attachment{{Name: attach + "\nBcc: " + badAddr, Data: strings.NewReader("x")}},
		},
		"an attachment with no name": {
			To: []string{"a@b.com"}, Subject: subject, Text: body,
			Attachments: []Attachment{{Data: strings.NewReader("x")}},
		},
	}
	for name, m := range cases {
		t.Run(name, func(t *testing.T) {
			err := m.Validate()
			if !errors.Is(err, ErrRejected) {
				t.Fatalf("Validate = %v, want one wrapping ErrRejected", err)
			}
			for _, secret := range []string{badAddr, subject, body, attach} {
				if strings.Contains(err.Error(), secret) {
					t.Errorf("the failure %q repeats the message content %q", err, secret)
				}
			}
		})
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

// secretModule registers a carrier whose configuration holds one credential, so
// the factory registers it as a literal redaction rule.
func secretModule(credential string) core.Module {
	prototype := stubConfig{}
	return NewModule(Spec[stubConfig]{
		ModuleName:      "mail.secret",
		CarrierName:     "secret",
		ConfigNamespace: "mail.secret",
		Config:          &prototype,
		Stance: func(*stubConfig) (core.Enablement, error) {
			return core.Enablement{State: core.StateEnabled}, nil
		},
		Secrets: func(*stubConfig) []string { return []string{credential} },
		Open:    func(context.Context, *stubConfig) (Sender, error) { return stubSender{}, nil },
	})
}

// TestACredentialIsRegisteredAsARedactionRule holds that a carrier's credential
// leaves the factory as a literal redaction rule, so a credential that turns up
// in any log line of the process is masked.
func TestACredentialIsRegisteredAsARedactionRule(t *testing.T) {
	const credential = "s3cr3t-mail-credential-sentinel"
	red := &capturingRedaction{}
	reg := registryWithReader(t, completeBody())
	reg.Register(providingLoggerModule(red))
	reg.Register(secretModule(credential))

	err := reg.Drive(t.Context(), core.Drive{
		Depth:        core.StageInit,
		Participants: []string{"mail.secret", config.ModuleName, "stand-in.logger"},
	})
	if err != nil {
		t.Fatalf("driving the module: %v", err)
	}
	if len(red.matches) == 0 {
		t.Fatal("no redaction rule was registered for the credential")
	}
	line := "a call carried " + credential + " somewhere"
	if spans := red.matches[0](line); len(spans) == 0 {
		t.Errorf("the credential %q is not masked in %q", credential, line)
	}
}
