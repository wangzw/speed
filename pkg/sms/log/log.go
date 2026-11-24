// Package log delivers the SMS carrier seams by writing each resolved message
// to the process log.
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

const approvalsItem = "approvals"

type approvalConfig struct {
	State  sms.ApprovalState `config:"state"`
	Reason string            `config:"reason"`
}

type moduleConfig struct {
	Approvals map[string]approvalConfig `config:"approvals"`
}

var configDefaults = moduleConfig{Approvals: map[string]approvalConfig{}}

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

// Module is the descriptor a host registers. It declares the sms.carrier
// selection item and is the carrier of last resort.
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

// Product is the carrier: it answers both carrier seams over the process log
// and reports the review verdicts configured for it.
type Product struct {
	// Nothing writes it after construction, so Approval reads it without synchronisation.
	configured map[string]sms.Approval
	logger     *slog.Logger
}

// New builds the carrier with no configured verdicts. A nil logger makes its
// records go to the process default logger.
func New(logger *slog.Logger) *Product {
	return newProduct(logger, nil)
}

func newProduct(logger *slog.Logger, configured map[string]sms.Approval) *Product {
	return &Product{
		configured: maps.Clone(configured),
		logger:     logger,
	}
}

var _ sms.Product = (*Product)(nil)

// Name is the carrier's own name.
func (*Product) Name() string { return CarrierName }

// Model declares that this carrier accepts both a vendor code and a rendered
// body, reviews every template, and names no identity.
func (*Product) Model() sms.CarrierModel {
	return sms.CarrierModel{
		Content: sms.ContentSupport{TemplateCode: true, TextBody: true},
		Review:  sms.ReviewSupport{PerTemplate: true},
	}
}

// Send writes the resolved message to the process log. The attribute is spelled
// verification_code because "code" is a registered sensitive key name and would
// be masked.
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
// and its attributes.
func Record(r sms.Resolved) (string, []any) {
	attrs := []any{
		"sender", CarrierName,
		"to", r.To,
		"template", r.Template,
		"locale", r.Locale,
	}
	if r.Kind != sms.KindVerification {
		return "log sms: a message was sent", attrs
	}
	return "log sms: a verification code was sent",
		append(attrs, "verification_code", r.Params["code"])
}

// Offer answers with a template code derived from the submission's name.
func (*Product) Offer(_ context.Context, sub sms.Submission) (string, error) {
	return CarrierName + "-" + strings.ReplaceAll(sub.Name, " ", "-"), nil
}

// Approval answers the configured verdict for code, or pending when the
// configuration does not name it.
func (p *Product) Approval(_ context.Context, code string) (sms.Approval, error) {
	if approval, configured := p.configured[code]; configured {
		return approval, nil
	}
	return sms.Approval{State: sms.ApprovalPending}, nil
}

// Retire is a no-op: the carrier keeps no review state of its own.
func (*Product) Retire(_ context.Context, _ string) error {
	return nil
}
