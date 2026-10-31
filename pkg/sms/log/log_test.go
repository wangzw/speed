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
	"github.com/vislake/speed/pkg/sms"
	smslog "github.com/vislake/speed/pkg/sms/log"
)

// capture is a logging capability that writes every record to a buffer.
type capture struct{ buf *bytes.Buffer }

func (c *capture) Named(name string) *slog.Logger {
	return slog.New(slog.NewTextHandler(c.buf, &slog.HandlerOptions{Level: slog.LevelDebug})).
		With("module", name)
}

func (c *capture) Redaction() speedlog.Redaction { return noRedaction{} }

// noRedaction accepts registrations and masks nothing; these cases hold the
// warning, not the redaction layer.
type noRedaction struct{}

func (noRedaction) AddKeys(...string)                   {}
func (noRedaction) AddPattern(string, speedlog.Matcher) {}

// loggerModule provides the logging capability over buf.
func loggerModule(buf *bytes.Buffer) core.Module {
	return core.Module{
		Name:     "test.logger",
		Provides: []core.Provision{{Token: (*speedlog.Logger)(nil)}},
		New: func(context.Context, *core.Registry) (any, error) {
			return &capture{buf: buf}, nil
		},
	}
}

// absentCarrier is a non-fallback carrier whose section is wholly unset, so it
// stands itself down and leaves the log carrier as the only candidate.
func absentCarrier() core.Module {
	prototype := struct{}{}
	return sms.NewModule(sms.Spec[struct{}]{
		ModuleName:      "test.absent",
		CarrierName:     "absent",
		ConfigNamespace: "test.absent",
		Config:          &prototype,
		Stance: func(*struct{}) (core.Enablement, error) {
			return core.Enablement{State: core.StateDisabled, Reason: "the section is unset"}, nil
		},
		Open: func(context.Context, *struct{}) (sms.Product, error) {
			return nil, errors.New("an unconfigured carrier must not be opened")
		},
	})
}

// drive runs an assembly over body and answers the registry it built.
func drive(t *testing.T, body string, buf *bytes.Buffer, participants ...string) *core.Registry {
	t.Helper()
	modules := []core.Module{smslog.Module(), loggerModule(buf), absentCarrier()}
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

// TestTheLogCarrierIsTheFallbackAndWarns holds the fallback and the warning: with
// no other carrier enabled the log carrier runs, and it says the channel puts
// messages into the process log.
func TestTheLogCarrierIsTheFallbackAndWarns(t *testing.T) {
	var buf bytes.Buffer
	reg := drive(t, `{}`, &buf,
		smslog.ModuleName, "test.absent", "test.logger", config.ModuleName)

	stance, ok := reg.Enablement(smslog.ModuleName)
	if !ok || stance.State != core.StateEnabled {
		t.Fatalf("the log carrier = %+v (reported %t), want enabled as the fallback", stance, ok)
	}
	out := buf.String()
	for _, want := range []string{"WARN", "process log"} {
		if !strings.Contains(out, want) {
			t.Errorf("the startup log %q does not carry %q", out, want)
		}
	}
}

// TestTheFallbackCarrierWritesTheMessageToTheProcessLog holds the channel
// itself: a resolved message handed to the sender the fallback resolves to
// lands in the process log, number, template and verification code included.
// Without it the fallback would be a warning about a channel nothing proves
// carries anything.
func TestTheFallbackCarrierWritesTheMessageToTheProcessLog(t *testing.T) {
	var buf bytes.Buffer
	reg := drive(t, `{}`, &buf,
		smslog.ModuleName, "test.absent", "test.logger", config.ModuleName)

	sender, err := core.Resolve[sms.Sender](reg)
	if err != nil {
		t.Fatalf("resolving the fallback sender: %v", err)
	}
	receipt, err := sender.Send(t.Context(), sms.Resolved{
		To:       "+15551234567",
		Template: "signin",
		Locale:   "en-US",
		Kind:     sms.KindVerification,
		Params:   map[string]string{"code": "654321"},
		DedupeID: "d1",
	})
	if err != nil {
		t.Fatalf("Send through the fallback: %v", err)
	}
	if got, want := receipt.ProviderID, "log:signin:d1"; got != want {
		t.Errorf("the receipt names %q, want %q", got, want)
	}
	out := buf.String()
	for _, want := range []string{"+15551234567", "signin", "654321", "verification_code"} {
		if !strings.Contains(out, want) {
			t.Errorf("the process log %q does not carry %q", out, want)
		}
	}
}

// TestSelectionNoneLeavesNoCarrierAndNoWarning holds the explicit off value:
// none switches the capability off without falling back, and no carrier is
// constructed, so no warning claims a log channel is running.
func TestSelectionNoneLeavesNoCarrierAndNoWarning(t *testing.T) {
	var buf bytes.Buffer
	reg := drive(t, `{"sms":{"carrier":"none"}}`, &buf,
		smslog.ModuleName, "test.absent", "test.logger", config.ModuleName)

	stance, _ := reg.Enablement(smslog.ModuleName)
	if stance.State != core.StateDisabled {
		t.Errorf("the log carrier = %+v, want disabled under sms.carrier=none", stance)
	}
	if _, err := core.Resolve[sms.Sender](reg); !errors.Is(err, core.ErrMissingProvider) {
		t.Errorf("resolving a sender = %v, want the missing-provider sentinel", err)
	}
	if out := buf.String(); strings.Contains(out, "process log") {
		t.Errorf("the startup log %q warns about a log channel that is off", out)
	}
}
