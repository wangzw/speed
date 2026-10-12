// Package tencent sends SMS verification codes through Tencent Cloud's SMS API
// (v20210111).
//
// It is one of the carriers a host may pick for the authn.sms module. It
// delivers the SMSSender seam exclusively, so a command tree names this package
// or the other carrier and never both: two exclusive claims in one run fail the
// startup. The engine that owns the challenge state machine takes up whatever the
// tree enabled and knows nothing about Tencent.
//
// What this package does is three things: it maps a (purpose, locale) pair to the
// template number configured for it, builds the SendSms request out of an
// authn.SMS, and sorts the carrier's answer into one of the SMS sentinels. It
// never retries: the carrier's send call is not idempotent, so a timeout leaves
// delivery unknown and is reported as ErrSMSUncertain rather than tried again.
// The verification code and the credentials it holds are written to no log, no
// error text and no metric; the credentials are registered as literal redaction
// rules so they cannot reach the process's logs by any other path either.
package tencent

import (
	"context"
	"fmt"

	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common"
	tcprofile "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common/profile"
	tc "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/sms/v20210111"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/literal"
	"github.com/vislake/speed/pkg/authn/sms/internal/dedupe"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/log"
)

// ModuleName is this module's name in the registry, and the name its
// configuration namespace takes.
const ModuleName = "authn.sms.tencent"

// ConfigNamespace is where this module's input items are mounted.
const ConfigNamespace = "authn.sms.tencent"

// backendName is the name this sender reports. It is a constant of the code: the
// engine names one carrier on the command tree and reads the name back to say
// which one ran.
const backendName = "tencent"

// init registers this module with the process registry, so a host that imports
// the package has the carrier available to name on a command tree.
func init() { core.ProcessRegistry.Register(Module()) }

// Module is the descriptor a host registers.
//
// The SMSSender claim is exclusive: only one carrier may be in a run, so the
// resolver fails a run that holds both. Prepare is declared because the module
// platform requires every module to state its own stance, and it states the one
// stance it has — a module named on a command tree is taken as running — while
// reading the configuration early so a missing section or an incomplete template
// table is a startup failure rather than one construction later.
//
// The logging capability is taken up as an optional requirement, because the
// credentials are registered as literal redaction rules and an assembly with no
// logging has nothing to register them with.
func Module() core.Module {
	return core.Module{
		Name:      ModuleName,
		Requires:  []core.Requirement{{Token: (*log.Logger)(nil), Optional: true}},
		Provides:  []core.Provision{{Token: (*authn.SMSSender)(nil), Exclusive: true}},
		Resources: []any{schema()},

		Prepare: func(_ context.Context, reg *core.Registry) (core.Enablement, error) {
			reader, err := readerOf(reg)
			if err != nil {
				return core.Enablement{}, err
			}
			if _, err := readConfig(reader); err != nil {
				return core.Enablement{}, err
			}
			return core.Enablement{State: core.StateEnabled}, nil
		},

		New: func(_ context.Context, reg *core.Registry) (any, error) {
			reader, err := readerOf(reg)
			if err != nil {
				return nil, err
			}
			cfg, err := readConfig(reader)
			if err != nil {
				return nil, err
			}
			// The credentials are registered before any logger binds an attribute,
			// which is the timing a redaction rule needs.
			registerSecrets(reg, cfg)
			return newSender(cfg, options{})
		},
	}
}

// readerOf takes up the configuration reader. Its absence is core's own failure
// and is carried out as it stands, so that a host that forgot the reader is not
// left with a lost carrier and no word about it.
func readerOf(reg *core.Registry) (config.Reader, error) {
	reader, err := core.Resolve[config.Reader](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: the carrier's credentials are read from configuration and no "+
			"reader was handed to the registry before the assembly began: %w", ModuleName, err)
	}
	return reader, nil
}

// registerSecrets registers the credentials as literal redaction rules with the
// logging capability, when the assembly carries one. A credential has no shape to
// match, so the only thing known about it is the value; without a rule it would
// be masked only where the code remembered to, and a request that failed would
// carry it into a log line. Registration is best effort: with no logging there is
// no log to protect.
func registerSecrets(reg *core.Registry, cfg resolved) {
	delivered, err := core.Resolve[log.Logger](reg)
	if err != nil {
		return
	}
	literal.Register(delivered.Redaction(), ModuleName+".credentials", cfg.secretID, cfg.secretKey)
}

// options carries the values a test fixes that the production path takes from the
// SDK's defaults: the endpoint the client posts to, the scheme, and how many
// identifiers the dedupe table remembers.
type options struct {
	endpoint  string
	scheme    string
	dedupeCap int
}

// sender is this module's product.
type sender struct {
	client    *tc.Client
	signName  string
	sdkAppID  string
	templates map[string]string
	dupe      *dedupe.Table[authn.SendReceipt]
}

// sender must satisfy the seam it delivers.
var _ authn.SMSSender = (*sender)(nil)

// newSender builds the product: a carrier client that never retries, and the
// template table and dedupe table it sends through.
func newSender(cfg resolved, opts options) (*sender, error) {
	profile := tcprofile.NewClientProfile()
	// A retry could deliver and bill a message twice, so the adapter takes the
	// SDK's retries down to none. The region breaker would move the endpoint
	// behind the caller's back, and the endpoint is this module's to fix.
	profile.NetworkFailureMaxRetries = 0
	profile.RateLimitExceededMaxRetries = 0
	profile.UnsafeRetryOnConnectionFailure = false
	profile.DisableRegionBreaker = true
	if opts.endpoint != "" {
		profile.HttpProfile.Endpoint = opts.endpoint
		if opts.scheme != "" {
			profile.HttpProfile.Scheme = opts.scheme
		}
	}
	client, err := tc.NewClient(common.NewCredential(cfg.secretID, cfg.secretKey), cfg.region, profile)
	if err != nil {
		return nil, fmt.Errorf("%s: building the carrier client: %w", ModuleName, err)
	}
	capacity := opts.dedupeCap
	if capacity <= 0 {
		capacity = dedupe.DefaultCapacity
	}
	return &sender{
		client:    client,
		signName:  cfg.signName,
		sdkAppID:  cfg.sdkAppID,
		templates: cfg.templates,
		dupe:      dedupe.New[authn.SendReceipt](capacity),
	}, nil
}

// Name reports the carrier this sender is, as the command tree named it.
func (s *sender) Name() string { return backendName }

// Send hands one message to the carrier.
//
// A message whose identifier was seen before is not sent again: the caller is
// answered with the earlier call's outcome, so the carrier is reached at most
// once per identifier. The carrier's call carries the caller's context and is not
// retried inside this call; how it answered decides which sentinel comes back,
// and a timeout or a network error is ErrSMSUncertain.
func (s *sender) Send(ctx context.Context, m authn.SMS) (authn.SendReceipt, error) {
	if m.DedupeID == "" {
		// No identifier to guard by; the caller is expected to set one from the
		// challenge, so this is the best effort of a malformed call rather than
		// the ordinary path.
		return s.deliver(ctx, m)
	}
	entry, first := s.dupe.Begin(m.DedupeID)
	if !first {
		return entry.Wait()
	}
	receipt, err := s.deliver(ctx, m)
	s.dupe.Finish(entry, receipt, err)
	return receipt, err
}

// deliver maps the message to a template number, builds the request and calls the
// carrier once.
func (s *sender) deliver(ctx context.Context, m authn.SMS) (authn.SendReceipt, error) {
	templateID, ok := s.templates[templateKey(m.Template, localeOf(m.Locale))]
	if !ok {
		// Construction checks the table against the closed sets, so a miss here
		// is a message whose template key or language is outside them: the caller
		// breached the contract, and nothing is sent.
		return authn.SendReceipt{}, fmt.Errorf("%w: %s has no template for the message it was "+
			"handed", authn.ErrSMSRejected, ModuleName)
	}
	request := s.buildRequest(m, templateID)
	response, err := s.client.SendSmsWithContext(ctx, request)
	if err != nil {
		return authn.SendReceipt{}, classify(err)
	}
	return classifyStatus(response)
}

// buildRequest turns a message into a SendSms request. The template parameters
// are positional, in the order the approved template expects: the verification
// code, then the code's remaining minutes.
func (s *sender) buildRequest(m authn.SMS, templateID string) *tc.SendSmsRequest {
	request := tc.NewSendSmsRequest()
	phone := m.To
	request.PhoneNumberSet = []*string{&phone}
	sdkAppID := s.sdkAppID
	request.SmsSdkAppId = &sdkAppID
	request.TemplateId = &templateID
	signName := s.signName
	request.SignName = &signName
	code := m.Params["code"]
	ttlMinutes := m.Params["ttl_minutes"]
	request.TemplateParamSet = []*string{&code, &ttlMinutes}
	return request
}
