// Package log delivers the mail sending seam by writing each message to the
// process log.
//
// It reaches no relay. It exists so a deployment that configures no other mail
// carrier still has a working mail channel, and so a local walkthrough can read
// a verification code out of the process output. It is the fallback carrier:
// it runs when no other carrier was explicitly enabled, and core's exclusive
// resolution stands it down as soon as one was. A run that selects it by name
// gets it too. Either way it warns at startup, because the body — and the
// verification code in it — leaves the process through the log stream.
package log

import (
	"context"
	"log/slog"
	"strings"

	"github.com/vislake/speed/pkg/core"
	speedlog "github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/mail"
)

// ModuleName is this module's name in the registry, and the name its
// configuration namespace takes.
const ModuleName = "mail.log"

// ConfigNamespace is where this module's input items are mounted. It declares
// no carrier item of its own; it declares the selection item mail.carrier.
const ConfigNamespace = ModuleName

// CarrierName is what this sender answers Name with and the name mail.carrier
// uses for it.
const CarrierName = "log"

// moduleConfig is the empty configuration this carrier accepts: it needs no
// credential and no endpoint, and every message goes to the process log.
type moduleConfig struct{}

func init() { core.ProcessRegistry.Register(Module()) }

// Module is the descriptor a host registers.
//
// Fallback marks it as the carrier of last resort. DeclaresSelection makes it
// the one module that declares mail.carrier, which is what lets a run name a
// carrier explicitly; it is declared here because this carrier is compiled into
// every build.
func Module() core.Module {
	return mail.NewModule(mail.Spec[moduleConfig]{
		ModuleName:        ModuleName,
		CarrierName:       CarrierName,
		Fallback:          true,
		DeclaresSelection: true,
		ConfigNamespace:   ConfigNamespace,
		Config:            &moduleConfig{},
		Stance: func(*moduleConfig) (core.Enablement, error) {
			return core.Enablement{State: core.StateEnabled}, nil
		},
		Open: open,
	})
}

// open builds the sender and emits the startup warning. The warning belongs
// here rather than in Send: a run whose operator never sees a message still
// needs to know the channel writes bodies to the log, and the log stream can
// leave the machine through collection or export.
func open(ctx context.Context, _ *moduleConfig) (mail.Sender, error) {
	logger := speedlog.FromContext(ctx)
	logger.Warn(
		"mail: the log carrier is running and writes every message, body included, to the process log; "+
			"configure mail.carrier to send through a relay",
		"carrier", CarrierName)
	return Sender{logger: logger}, nil
}

// Sender writes each message to the process log.
type Sender struct {
	logger *slog.Logger
}

var _ mail.Sender = Sender{}

// Name is the carrier's own name.
func (Sender) Name() string { return CarrierName }

// Send writes the rendered message to the process log. No value here is chosen
// by this package: the code was drawn by the login module and rendered by
// authn.emailcode.
//
// The code is reported under verification_code rather than code, because the
// gateway registers "code" as a sensitive key name for the process and the
// logging layer would mask a value logged under it.
func (s Sender) Send(_ context.Context, m mail.Message) (mail.Receipt, error) {
	logger := s.logger
	if logger == nil {
		logger = speedlog.Default()
	}
	logger.Info("log mail: a message was sent",
		"carrier", CarrierName,
		"to", strings.Join(m.To, ", "),
		"subject", m.Subject,
		"text", m.Text,
		"html", m.HTML,
		"verification_code", VerificationCode(m.Text, m.HTML))
	return mail.Receipt{ProviderID: CarrierName}, nil
}

// VerificationCode reads the six-digit code out of the rendered bodies, the
// plain text first. It reports an empty string when no body carries one; a
// longer run of digits is not a code and is not reported.
func VerificationCode(bodies ...string) string {
	for _, body := range bodies {
		if code := SixDigitRun(body); code != "" {
			return code
		}
	}
	return ""
}

// SixDigitRun returns the first run of exactly six digits in text, or "".
func SixDigitRun(text string) string {
	run, start := 0, 0
	for i := 0; i < len(text); i++ {
		if text[i] >= '0' && text[i] <= '9' {
			if run == 0 {
				start = i
			}
			run++
			continue
		}
		if run == 6 {
			return text[start : start+6]
		}
		run = 0
	}
	if run == 6 {
		return text[start : start+6]
	}
	return ""
}
