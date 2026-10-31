// Package log delivers the SMS carrier seams by writing each resolved message
// to the process log.
//
// It reaches no vendor. It exists so a deployment that configures no other SMS
// carrier still has a working SMS channel, and so a local walkthrough can read
// a verification code out of the process output. It is the fallback carrier:
// it runs when no other carrier was explicitly enabled, and core's exclusive
// resolution stands it down as soon as one was. A run that selects it by name
// gets it too. Either way it warns at startup, because the message — and the
// verification code in it — leaves the process through the log stream.
//
// The verification code is not chosen here: the SMS module generates it and
// hands it over as the message's declared "code" parameter. The carrier's whole
// work is writing that line and answering success. Every number still goes
// through the real budgets, the challenge row and the verification, so the
// flow exercises the product's own state machine end to end.
//
// It answers a real per-template review: its Model declares Review.PerTemplate,
// every language it does not know about answers pending, and Review moves one
// code's verdict. A runtime template therefore starts pending in each language
// it was registered in and settles language by language. Review is an input the
// review assembly drives; the carrier itself is present in every build.
package log

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/vislake/speed/pkg/core"
	speedlog "github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/sms"
)

// ModuleName is this module's name in the registry, and the name its
// configuration namespace takes.
const ModuleName = "sms.log"

// ConfigNamespace is where this module's input items are mounted. It declares
// no carrier item of its own; it declares the selection item sms.carrier.
const ConfigNamespace = ModuleName

// CarrierName is what this product answers Name with and the name sms.carrier
// uses for it.
const CarrierName = "log"

// moduleConfig is the empty configuration this carrier accepts: it needs no
// credential and no endpoint, and every message goes to the process log.
type moduleConfig struct{}

func init() { core.ProcessRegistry.Register(Module()) }

// Module is the descriptor a host registers.
//
// Fallback marks it as the carrier of last resort. DeclaresSelection makes it
// the one module that declares sms.carrier, which is what lets a run name a
// carrier explicitly; it is declared here because this carrier is compiled into
// every build.
func Module() core.Module {
	return sms.NewModule(sms.Spec[moduleConfig]{
		ModuleName:        ModuleName,
		CarrierName:       CarrierName,
		Fallback:          true,
		DeclaresSelection: true,
		ConfigNamespace:   ConfigNamespace,
		Config:            &moduleConfig{},
		Stance: func(*moduleConfig) (core.Enablement, error) {
			return core.Enablement{State: core.StateEnabled}, nil
		},
		Open: func(ctx context.Context, _ *moduleConfig) (sms.Product, error) {
			logger := speedlog.FromContext(ctx)
			logger.Warn(
				"sms: the log carrier is running and writes every message, code included, to the process log; "+
					"configure sms.carrier to send through a vendor",
				"carrier", CarrierName)
			return New(logger), nil
		},
	})
}

// Reviewer is the control the review assembly drives: it moves one vendor
// code's verdict. It is exported so a review-only control can reach the
// fallback product without the package exposing its concrete type.
type Reviewer interface {
	Review(code string, state sms.ApprovalState, reason string) error
}

// Product is the carrier: it answers both seams over the process log and holds
// the in-memory verdicts the per-template review reports.
type Product struct {
	mu       sync.Mutex
	verdicts map[string]sms.Approval
	logger   *slog.Logger
}

// New builds the carrier with an empty verdict store. A nil logger makes its
// records go to the process default logger.
func New(logger *slog.Logger) *Product {
	return &Product{verdicts: make(map[string]sms.Approval), logger: logger}
}

var (
	_ sms.Product = (*Product)(nil)
	_ Reviewer    = (*Product)(nil)
)

// Name is the carrier's own name.
func (*Product) Name() string { return CarrierName }

// Model declares what this carrier can carry. It writes whatever the registry
// resolved to the process log, so it accepts both a vendor code and a rendered
// body. It reviews every template: a code starts pending and Review moves it,
// which is the state the template registry reads back per language. It names no
// identity: every message goes to the process log, so there is nothing to
// choose between.
func (*Product) Model() sms.CarrierModel {
	return sms.CarrierModel{
		Content: sms.ContentSupport{TemplateCode: true, TextBody: true},
		Review:  sms.ReviewSupport{PerTemplate: true},
	}
}

// Send writes the resolved message to the process log. The code is the SMS
// module's own, handed over as the message's declared parameter; this line is
// where a reviewer reads it, and no value here is chosen by this package.
//
// The line names the message's kind. A verification message carries the code
// the recipient proves and keeps the verification_code field; a notice carries
// no code, so its line says a message was sent and omits that field rather than
// claiming a verification that never happened. The attribute is spelled
// verification_code rather than code because the gateway registers "code" as a
// sensitive key name for the process, and the logging layer would mask this
// value.
func (p *Product) Send(_ context.Context, r sms.Resolved) (sms.Receipt, error) {
	msg, attrs := Record(r)
	logger := p.logger
	if logger == nil {
		logger = speedlog.Default()
	}
	logger.Info(msg, attrs...)
	return sms.Receipt{ProviderID: CarrierName + ":" + r.Template + ":" + r.DedupeID}, nil
}

// Record builds the log record one resolved message produces: the message text
// and its attributes. The kind is the template's, set by the registry from the
// declaration, so a configured authn code template and a runtime notice
// template are told apart even when both arrive through this same carrier.
func Record(r sms.Resolved) (string, []any) {
	attrs := []any{
		"sender", CarrierName,
		"to", r.To,
		"template", r.Template,
		"locale", r.Locale,
	}
	if r.Kind != sms.KindVerification {
		// The kind is what the vendor would be told; a carrier that writes to
		// the log instead must not report a code a non-verification message
		// never carried.
		return "log sms: a message was sent", attrs
	}
	return "log sms: a verification code was sent",
		append(attrs, "verification_code", r.Params["code"])
}

// Offer answers with a template code derived from the submission's name and
// records that code as pending: a template just offered to the vendor has not
// been reviewed yet.
func (p *Product) Offer(_ context.Context, sub sms.Submission) (string, error) {
	code := CarrierName + "-" + strings.ReplaceAll(sub.Name, " ", "-")
	p.mu.Lock()
	if p.verdicts == nil {
		p.verdicts = make(map[string]sms.Approval)
	}
	p.verdicts[code] = sms.Approval{State: sms.ApprovalPending}
	p.mu.Unlock()
	return code, nil
}

// Approval answers one code's review. A code the carrier has not been told
// about is pending: the vendor has taken the template and has not answered yet,
// which is the state a language starts in when a runtime template is
// registered.
func (p *Product) Approval(_ context.Context, code string) (sms.Approval, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if approval, held := p.verdicts[code]; held {
		return approval, nil
	}
	return sms.Approval{State: sms.ApprovalPending}, nil
}

// Review moves one vendor code's verdict, leaving every other code — and
// therefore every other language — untouched. It is the review assembly's own
// control over the carrier; the control route that calls it exists only in the
// review build. The states it accepts are the per-template review states;
// Unknown and NotRequired describe carriers whose review this process does not
// track or that have none.
func (p *Product) Review(code string, state sms.ApprovalState, reason string) error {
	if code == "" {
		return fmt.Errorf("log carrier: a review names no vendor code")
	}
	switch state {
	case sms.ApprovalPending, sms.ApprovalApproved, sms.ApprovalRejected, sms.ApprovalWithdrawn:
	default:
		return fmt.Errorf("log carrier: %q is not a per-template review state", state)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.verdicts == nil {
		p.verdicts = make(map[string]sms.Approval)
	}
	p.verdicts[code] = sms.Approval{State: state, Reason: reason}
	return nil
}

// Retire forgets one code's verdict, so a code offered again starts pending.
func (p *Product) Retire(_ context.Context, code string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.verdicts, code)
	return nil
}
