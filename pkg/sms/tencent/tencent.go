// Package tencent sends SMS through Tencent Cloud's SMS API (v20210111).
//
// It is a carrier module and nothing else: it maps a message's template and
// language to an approved template number, builds one SendSms request, calls the
// carrier once and classifies what comes back into the sending sentinels. It
// holds no challenge state, counts nothing, retries nothing and knows no
// template beyond the keys it is handed.
//
// A host takes it in by writing ModuleName into a command. It delivers
// sms.Sender exclusively, so a command names exactly one carrier and two at once
// fail in resolution. The whole leaf package binds the vendor SDK and can be
// dropped without touching any other module.
//
// The verification code and the credentials this module holds are written to no
// log, no error text and no metric; the credentials are registered as literal
// redaction rules so they cannot reach the process's logs by any other path
// either.
package tencent

import (
	"context"
	"fmt"

	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common"
	tcprofile "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common/profile"
	tc "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/sms/v20210111"

	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/sms"
	"github.com/vislake/speed/pkg/sms/internal/dedupe"
)

// ModuleName is this module's name in the registry, and the name its
// configuration namespace takes.
const ModuleName = "sms.tencent"

// ConfigNamespace is where this module's input items are mounted.
const ConfigNamespace = ModuleName

// backendName is the name this sender reports. It is a constant of the code: the
// assembly names one carrier on the command tree and reads the name back to say
// which one ran.
const backendName = "tencent"

// init registers this module with the process registry, so a host that imports
// the package has the carrier available to name on a command tree.
func init() { core.ProcessRegistry.Register(Module()) }

// Module is the descriptor a host registers.
//
// The sms.Sender claim is exclusive: only one carrier may be in a run, so the
// resolver fails a run that holds both. The stance states that the module runs
// once its configuration is sound, while reading the configuration early so a
// missing required item or an inconsistent template set is a startup failure
// rather than one construction later. The logging capability is taken up as an
// optional requirement, because the credentials are registered as literal
// redaction rules and an assembly with no logging has nothing to register them
// with.
func Module() core.Module {
	return sms.NewModule(sms.Spec[moduleConfig]{
		ModuleName:      ModuleName,
		ConfigNamespace: ConfigNamespace,
		Config:          &moduleConfig{},
		Items:           items(),
		Stance:          stance,
		Secrets:         secrets,
		Open:            open,
	})
}

// stance states that the module runs once its configuration is sound: a carrier
// named on a command tree is taken as running. Reading the configuration here
// makes an unsound section a startup failure, one stage ahead of construction.
func stance(cfg *moduleConfig) (core.Enablement, error) {
	if _, err := cfg.resolve(); err != nil {
		return core.Enablement{}, err
	}
	return core.Enablement{State: core.StateEnabled}, nil
}

// secrets returns the values that have to be masked wherever the process logs
// them: the credential identifier and its secret, both of which a failed request
// can carry into a log line.
func secrets(cfg *moduleConfig) []string {
	return []string{cfg.SecretID, cfg.SecretKey}
}

// open builds the product with the SDK's defaults.
func open(_ context.Context, cfg *moduleConfig) (sms.Sender, error) {
	conf, err := cfg.resolve()
	if err != nil {
		return nil, err
	}
	return newSender(conf, options{})
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
	client   *tc.Client
	signName string
	sdkAppID string
	decl     sms.Declaration
	dupe     *dedupe.Table[sms.Receipt]
}

// sender must satisfy the seam it delivers.
var _ sms.Sender = (*sender)(nil)

// newSender builds the product: a carrier client that never retries, and the
// template declaration and dedupe table it sends through.
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
		client:   client,
		signName: cfg.signName,
		sdkAppID: cfg.sdkAppID,
		decl:     cfg.decl,
		dupe:     dedupe.New[sms.Receipt](capacity),
	}, nil
}

// Name reports the carrier this sender is, as the command tree named it.
func (s *sender) Name() string { return backendName }

// Declared reports the templates this carrier holds, so a consumer can check its
// own needs at startup.
func (s *sender) Declared() []sms.Template { return s.decl.Declared() }

// Send hands one message to the carrier.
//
// A message whose identifier was seen before is not sent again: the caller is
// answered with the earlier call's outcome, so the carrier is reached at most
// once per identifier. The carrier's call carries the caller's context and is not
// retried inside this call; how it answered decides which sentinel comes back,
// and a timeout or a network error is ErrUncertain.
func (s *sender) Send(ctx context.Context, m sms.Message) (sms.Receipt, error) {
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

// deliver resolves the message against the declaration, builds the request and
// calls the carrier once.
func (s *sender) deliver(ctx context.Context, m sms.Message) (sms.Receipt, error) {
	resolved, err := s.decl.Resolve(m)
	if err != nil {
		// The message is not one the declaration holds: the caller breached the
		// contract, and nothing is sent.
		return sms.Receipt{}, err
	}
	request := s.buildRequest(m, resolved)
	response, err := s.client.SendSmsWithContext(ctx, request)
	if err != nil {
		return sms.Receipt{}, classify(err)
	}
	return classifyStatus(response)
}

// buildRequest turns a message into a SendSms request. The template parameters
// are positional, in the order the approved template expects: the declaration's
// ordered parameters, laid out as the template was approved.
func (s *sender) buildRequest(m sms.Message, resolved sms.Resolved) *tc.SendSmsRequest {
	request := tc.NewSendSmsRequest()
	phone := m.To
	request.PhoneNumberSet = []*string{&phone}
	sdkAppID := s.sdkAppID
	request.SmsSdkAppId = &sdkAppID
	templateID := resolved.Code
	request.TemplateId = &templateID
	signName := s.signName
	request.SignName = &signName
	values := make([]*string, len(resolved.Ordered))
	for i := range resolved.Ordered {
		value := resolved.Ordered[i]
		values[i] = &value
	}
	request.TemplateParamSet = values
	return request
}
