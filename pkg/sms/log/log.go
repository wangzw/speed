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
// and the configured verdicts are the only thing it reports. A code the
// configuration names answers with that verdict; every other code answers
// pending. Offer and Retire leave the answers untouched: the carrier keeps no
// review state of its own, so a runtime template starts pending in each
// language it was registered in and stays there unless the configuration gives
// its code a verdict.
package log

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strings"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	speedlog "github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/sms"
)

// ModuleName is this module's name in the registry, and the name its
// configuration namespace takes.
const ModuleName = "sms.log"

// ConfigNamespace is where this module's input items are mounted: the approvals
// item and the sms.carrier selection item it declares.
const ConfigNamespace = ModuleName

// CarrierName is what this product answers Name with and the name sms.carrier
// uses for it.
const CarrierName = "log"

// approvalsItem is the configuration item holding the review verdicts an
// operator pinned for the carrier's vendor template codes.
const approvalsItem = "approvals"

// approvalConfig is one configured verdict: the review state and, optionally,
// the reason the vendor gave.
type approvalConfig struct {
	State  sms.ApprovalState `config:"state"`
	Reason string            `config:"reason"`
}

// moduleConfig is what this carrier accepts from configuration. It needs no
// credential and no endpoint; the one item pins the verdicts the carrier
// answers, keyed by the template code Offer returns, so a runtime template can
// be given a review outcome by configuration.
type moduleConfig struct {
	Approvals map[string]approvalConfig `config:"approvals"`
}

// configDefaults is what the carrier accepts when no source gives the item: an
// empty verdict map, which leaves every code at the pending answer it had.
var configDefaults = moduleConfig{Approvals: map[string]approvalConfig{}}

// resolve turns the configured verdicts into what the carrier answers with,
// refusing a state the per-template review does not define. A refusal names the
// item and the code, so an operator can find the entry; it wraps
// sms.ErrInvalidConfig, the sentinel this directory uses for a configuration it
// cannot run on.
func (c moduleConfig) resolve() (map[string]sms.Approval, error) {
	if len(c.Approvals) == 0 {
		return nil, nil
	}
	verdicts := make(map[string]sms.Approval, len(c.Approvals))
	for _, code := range slices.Sorted(maps.Keys(c.Approvals)) {
		if code == "" {
			return nil, fmt.Errorf("%w: %s.%s holds a verdict under an empty template code, and a "+
				"configured verdict is keyed by the code the carrier answers Offer with",
				sms.ErrInvalidConfig, ConfigNamespace, approvalsItem)
		}
		entry := c.Approvals[code]
		switch entry.State {
		case sms.ApprovalPending, sms.ApprovalApproved, sms.ApprovalRejected, sms.ApprovalWithdrawn:
		default:
			return nil, fmt.Errorf("%w: %s.%s[%q].state is %q, and a configured verdict is "+
				"pending, approved, rejected or withdrawn", sms.ErrInvalidConfig, ConfigNamespace,
				approvalsItem, code, entry.State)
		}
		verdicts[code] = sms.Approval{State: entry.State, Reason: entry.Reason}
	}
	return verdicts, nil
}

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
		Config:            &configDefaults,
		Items: map[string]config.Item{
			approvalsItem: {
				// A container leaf: the map takes its origin set from the
				// primary config source, and its keys are the module's own
				// data rather than names this module owns.
				Origins: config.OriginPrimary,
				Description: "the review verdicts this carrier answers, keyed by the vendor " +
					"template code Offer returns; each takes a state of pending, approved, " +
					"rejected or withdrawn and an optional reason. A code not named here " +
					"answers pending",
			},
		},
		Stance: func(cfg *moduleConfig) (core.Enablement, error) {
			if _, err := cfg.resolve(); err != nil {
				return core.Enablement{}, err
			}
			return core.Enablement{State: core.StateEnabled}, nil
		},
		Open: func(ctx context.Context, cfg *moduleConfig) (sms.Product, error) {
			verdicts, err := cfg.resolve()
			if err != nil {
				return nil, err
			}
			logger := speedlog.FromContext(ctx)
			logger.Warn(
				"sms: the log carrier is running and writes every message, code included, to the process log; "+
					"configure sms.carrier to send through a vendor",
				"carrier", CarrierName)
			return newProduct(logger, verdicts), nil
		},
	})
}

// Product is the carrier: it answers both seams over the process log and holds
// the verdicts the per-template review reports — the ones the configuration
// pinned, read-only for the life of the process. It keeps no review state that
// Offer or Retire could change.
type Product struct {
	// configured holds the verdicts the configuration pinned, keyed by code.
	// Nothing writes it after construction, so Approval reads it without
	// synchronisation.
	configured map[string]sms.Approval
	logger     *slog.Logger
}

// New builds the carrier with no configured verdicts. A nil logger makes its
// records go to the process default logger.
func New(logger *slog.Logger) *Product {
	return newProduct(logger, nil)
}

// newProduct builds the carrier over the verdicts the configuration pinned. It
// copies them, so no caller keeps a handle into what Approval reads.
func newProduct(logger *slog.Logger, configured map[string]sms.Approval) *Product {
	return &Product{
		configured: maps.Clone(configured),
		logger:     logger,
	}
}

var _ sms.Product = (*Product)(nil)

// Name is the carrier's own name.
func (*Product) Name() string { return CarrierName }

// Model declares what this carrier can carry. It writes whatever the registry
// resolved to the process log, so it accepts both a vendor code and a rendered
// body. It reviews every template: the configuration pins a verdict or the code
// starts pending, which is the state the template registry reads back per
// language. It names no identity: every message goes to the process log, so
// there is nothing to choose between.
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

// Offer answers with a template code derived from the submission's name. The
// code is the identifier the vendor's review is read under later, so the carrier
// keeps no record of it: Approval answers the configured verdict for the code,
// or pending when the configuration does not name it.
func (*Product) Offer(_ context.Context, sub sms.Submission) (string, error) {
	return CarrierName + "-" + strings.ReplaceAll(sub.Name, " ", "-"), nil
}

// Approval answers one code's review. A configured verdict is the only answer
// this carrier has: a code the configuration names answers with it, and every
// other code answers pending — the state the template registry starts a language
// in when a runtime template is registered, and the one it keeps unless the
// configuration gives its code a verdict.
func (p *Product) Approval(_ context.Context, code string) (sms.Approval, error) {
	if approval, configured := p.configured[code]; configured {
		return approval, nil
	}
	return sms.Approval{State: sms.ApprovalPending}, nil
}

// Retire is a no-op: the carrier holds nothing at the vendor and keeps no review
// state of its own, so removing a code changes no answer. A configured verdict
// keeps answering for the code.
func (*Product) Retire(_ context.Context, _ string) error {
	return nil
}
