// Package tencent sends SMS through Tencent Cloud SMS API v20210111 and manages
// the templates it sends under. It binds the vendor SDK and registers one carrier
// module.
package tencent

import (
	"context"
	"fmt"
	"strings"

	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common"
	tcprofile "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common/profile"
	tc "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/sms/v20210111"

	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/internal/dedupe"
	"github.com/vislake/speed/pkg/sms"
)

// ModuleName is this module's name in the registry, and the name its
// configuration namespace takes.
const ModuleName = "sms.tencent"

// ConfigNamespace is where this module's input items are mounted.
const ConfigNamespace = ModuleName

const backendName = "tencent"

func init() { core.ProcessRegistry.Register(Module()) }

// Module is the descriptor a host registers. It delivers both carrier seams
// exclusively and takes the logging capability as an optional requirement.
func Module() core.Module {
	return sms.NewModule(sms.Spec[moduleConfig]{
		ModuleName:      ModuleName,
		CarrierName:     backendName,
		ConfigNamespace: ConfigNamespace,
		Config:          &moduleConfig{},
		Items:           items(),
		Stance:          stance,
		Secrets:         secrets,
		Open:            open,
	})
}

func stance(cfg *moduleConfig) (core.Enablement, error) {
	if cfg.SecretID == "" && cfg.SecretKey == "" && cfg.SDKAppID == "" && cfg.Region == "" && cfg.SignName == "" {
		return core.Enablement{
			State:  core.StateDisabled,
			Reason: ModuleName + " has no configuration: no credential, application, region or signature",
		}, nil
	}
	missing := make([]string, 0, 5)
	for _, item := range []struct {
		name  string
		empty bool
	}{
		{secretIDItem, cfg.SecretID == ""},
		{secretKeyItem, cfg.SecretKey == ""},
		{sdkAppIDItem, cfg.SDKAppID == ""},
		{regionItem, cfg.Region == ""},
		{signNameItem, cfg.SignName == ""},
	} {
		if item.empty {
			missing = append(missing, item.name)
		}
	}
	if len(missing) > 0 {
		return core.Enablement{}, fmt.Errorf("%w: %s is partially configured; %s missing",
			sms.ErrInvalidConfig, ConfigNamespace, strings.Join(missing, ", "))
	}
	return core.Enablement{State: core.StateEnabled}, nil
}

func secrets(cfg *moduleConfig) []string {
	return []string{cfg.SecretID, cfg.SecretKey}
}

func open(_ context.Context, cfg *moduleConfig) (sms.Product, error) {
	return newSender(cfg.resolve(), options{})
}

type options struct {
	endpoint  string
	scheme    string
	dedupeCap int
}

type sender struct {
	client   *tc.Client
	signName string
	sdkAppID string
	dupe     *dedupe.Table[sms.Receipt]
}

var (
	_ sms.Sender   = (*sender)(nil)
	_ sms.Provider = (*sender)(nil)
)

func newSender(cfg resolved, opts options) (*sender, error) {
	profile := tcprofile.NewClientProfile()
	// A retry could deliver and bill a message twice, so the adapter takes the
	// SDK's retries down to none, and the region breaker is left off so the endpoint
	// stays the one this module sets.
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
		dupe:     dedupe.New[sms.Receipt](capacity),
	}, nil
}

// Name reports the carrier this sender is, as the command tree named it.
func (s *sender) Name() string { return backendName }

// Model reports what this carrier can carry: the vendor's approved template
// number over the configured signature, a per-template review, no attribute keys,
// no body bound of its own and a synchronous outcome.
func (s *sender) Model() sms.CarrierModel {
	return sms.CarrierModel{
		Content: sms.ContentSupport{TemplateCode: true},
		Review:  sms.ReviewSupport{PerTemplate: true},
		Identities: []sms.Identity{{
			Name:  s.signName,
			Kind:  sms.IdentitySignName,
			Value: s.signName,
		}},
	}
}

// Send hands one resolved message to the carrier, at most once per stable
// identifier. A repeated identifier is answered with the earlier outcome; a
// timeout or a network error is ErrUncertain.
func (s *sender) Send(ctx context.Context, r sms.Resolved) (sms.Receipt, error) {
	if r.DedupeID == "" {
		return s.deliver(ctx, r)
	}
	entry, first := s.dupe.Begin(r.DedupeID)
	if !first {
		return entry.Wait()
	}
	receipt, err := s.deliver(ctx, r)
	s.dupe.Finish(entry, receipt, err)
	return receipt, err
}

func (s *sender) deliver(ctx context.Context, r sms.Resolved) (sms.Receipt, error) {
	if r.ValidFor != 0 {
		return sms.Receipt{}, fmt.Errorf("%w: %s has no lifetime for a queued message, and this message "+
			"asks for one", sms.ErrRejected, ModuleName)
	}
	request := s.buildRequest(r)
	response, err := s.client.SendSmsWithContext(ctx, request)
	if err != nil {
		return sms.Receipt{}, classify(err)
	}
	return classifyStatus(response)
}

func (s *sender) buildRequest(r sms.Resolved) *tc.SendSmsRequest {
	request := tc.NewSendSmsRequest()
	phone := r.To
	request.PhoneNumberSet = []*string{&phone}
	sdkAppID := s.sdkAppID
	request.SmsSdkAppId = &sdkAppID
	templateID := r.Code
	request.TemplateId = &templateID
	signName := s.signName
	request.SignName = &signName
	values := make([]*string, len(r.Ordered))
	for i := range r.Ordered {
		value := r.Ordered[i]
		values[i] = &value
	}
	request.TemplateParamSet = values
	return request
}
