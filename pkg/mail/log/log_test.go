package log_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/internal/configtest"
	speedlog "github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/mail"
	maillog "github.com/vislake/speed/pkg/mail/log"
)

type capture struct{ buf *bytes.Buffer }

func (c *capture) Named(name string) *slog.Logger {
	return slog.New(slog.NewTextHandler(c.buf, &slog.HandlerOptions{Level: slog.LevelDebug})).
		With("module", name)
}

func (c *capture) Redaction() speedlog.Redaction { return noRedaction{} }

type noRedaction struct{}

func (noRedaction) AddKeys(...string)                   {}
func (noRedaction) AddPattern(string, speedlog.Matcher) {}

func loggerModule(buf *bytes.Buffer) core.Module {
	return core.Module{
		Name:     "test.logger",
		Provides: []core.Provision{{Token: (*speedlog.Logger)(nil)}},
		New: func(context.Context, *core.Registry) (any, error) {
			return &capture{buf: buf}, nil
		},
	}
}

func absentCarrier() core.Module {
	prototype := struct{}{}
	return mail.NewModule(mail.Spec[struct{}]{
		ModuleName:      "test.absent",
		CarrierName:     "absent",
		ConfigNamespace: "test.absent",
		Config:          &prototype,
		Stance: func(*struct{}) (core.Enablement, error) {
			return core.Enablement{State: core.StateDisabled, Reason: "the section is unset"}, nil
		},
		Open: func(context.Context, *struct{}) (mail.Sender, error) {
			return nil, errors.New("an unconfigured carrier must not be opened")
		},
	})
}

func drive(t *testing.T, body string, buf *bytes.Buffer, participants ...string) *core.Registry {
	t.Helper()
	modules := []core.Module{maillog.Module(), loggerModule(buf), absentCarrier()}
	reader := configtest.Reader(t, body, modules...)
	reg := core.New()
	reg.Register(config.Module())
	reg.Supply(reader, config.ModuleName)
	for _, module := range modules {
		reg.Register(module)
	}
	if err := reg.Drive(t.Context(), core.Drive{
		Depth:        core.StageInit,
		Participants: participants,
	}); err != nil {
		t.Fatalf("the drive failed: %v", err)
	}
	return reg
}

// TestTheLogCarrierIsTheFallbackAndWarns checks that with no other carrier enabled
// the log carrier runs and warns that messages reach the process log.
func TestTheLogCarrierIsTheFallbackAndWarns(t *testing.T) {
	var buf bytes.Buffer
	reg := drive(t, `{}`, &buf,
		maillog.ModuleName, "test.absent", "test.logger", config.ModuleName)

	stance, ok := reg.Enablement(maillog.ModuleName)
	if !ok || stance.State != core.StateEnabled {
		t.Fatalf("the log carrier = %+v (reported %t), want enabled as the fallback", stance, ok)
	}
	absent, _ := reg.Enablement("test.absent")
	if absent.State != core.StateDisabled {
		t.Errorf("the unconfigured carrier = %+v, want disabled", absent)
	}
	out := buf.String()
	for _, want := range []string{"WARN", "process log"} {
		if !strings.Contains(out, want) {
			t.Errorf("the startup log %q does not carry %q", out, want)
		}
	}
}

// TestTheFallbackCarrierWritesTheMessageToTheProcessLog checks that a message sent
// through the fallback lands in the process log with its code.
func TestTheFallbackCarrierWritesTheMessageToTheProcessLog(t *testing.T) {
	var buf bytes.Buffer
	reg := drive(t, `{}`, &buf,
		maillog.ModuleName, "test.absent", "test.logger", config.ModuleName)

	sender, err := core.Resolve[mail.Sender](reg)
	if err != nil {
		t.Fatalf("resolving the fallback sender: %v", err)
	}
	receipt, err := sender.Send(t.Context(), mail.Message{
		To:      []string{"ada@example.com"},
		Subject: "Your sign-in code",
		Text:    "Use 123456 to sign in to the staff portal.",
	})
	if err != nil {
		t.Fatalf("Send through the fallback: %v", err)
	}
	if receipt.ProviderID != maillog.CarrierName {
		t.Errorf("the receipt names %q, want %q", receipt.ProviderID, maillog.CarrierName)
	}
	out := buf.String()
	for _, want := range []string{"ada@example.com", "Your sign-in code", "staff portal", "123456", "verification_code"} {
		if !strings.Contains(out, want) {
			t.Errorf("the process log %q does not carry %q", out, want)
		}
	}
}

// TestSelectionNoneLeavesNoCarrierAndNoWarning checks that the none selection
// leaves no carrier and writes no warning about a log channel.
func TestSelectionNoneLeavesNoCarrierAndNoWarning(t *testing.T) {
	var buf bytes.Buffer
	reg := drive(t, `{"mail":{"carrier":"none"}}`, &buf,
		maillog.ModuleName, "test.absent", "test.logger", config.ModuleName)

	stance, _ := reg.Enablement(maillog.ModuleName)
	if stance.State != core.StateDisabled {
		t.Errorf("the log carrier = %+v, want disabled under mail.carrier=none", stance)
	}
	if _, err := core.Resolve[mail.Sender](reg); !errors.Is(err, core.ErrMissingProvider) {
		t.Errorf("resolving a sender = %v, want the missing-provider sentinel", err)
	}
	if out := buf.String(); strings.Contains(out, "process log") {
		t.Errorf("the startup log %q warns about a log channel that is off", out)
	}
}
