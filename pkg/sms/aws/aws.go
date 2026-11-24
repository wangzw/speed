// Package aws delivers the SMS carrier seams over AWS End User Messaging SMS,
// through its Pinpoint SMS Voice v2 API. It binds the vendor SDK and registers
// one carrier module.
package aws

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	awscreds "github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/pinpointsmsvoicev2"
	"github.com/aws/aws-sdk-go-v2/service/pinpointsmsvoicev2/types"

	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/internal/dedupe"
	"github.com/vislake/speed/pkg/sms"
)

// ModuleName is this module's name in the registry.
const ModuleName = "sms.aws"

// ConfigNamespace is where this module's input items are mounted.
const ConfigNamespace = ModuleName

const carrierName = "aws"

// The vendor's target-country parameter keys for an Indian destination; both are
// required for an Indian sender ID.
const (
	inTemplateID = "IN_TEMPLATE_ID"
	inEntityID   = "IN_ENTITY_ID"
)

const (
	indiaCallingCode = "+91"
	indiaCountry     = "IN"
)

type carrier interface {
	SendTextMessage(ctx context.Context, params *pinpointsmsvoicev2.SendTextMessageInput, optFns ...func(*pinpointsmsvoicev2.Options)) (*pinpointsmsvoicev2.SendTextMessageOutput, error)
}

type sender struct {
	client               carrier
	configurationSetName string
	defaultIdentity      string
	identities           []sms.Identity
	byName               map[string]sms.Identity
	dupe                 *dedupe.Table[sms.Receipt]
}

var (
	_ sms.Sender   = (*sender)(nil)
	_ sms.Provider = (*sender)(nil)
)

func init() { core.ProcessRegistry.Register(Module()) }

// Module is the descriptor a host registers. It delivers both carrier seams
// exclusively and takes the logging capability as an optional requirement.
func Module() core.Module {
	return sms.NewModule(sms.Spec[moduleConfig]{
		ModuleName:      ModuleName,
		CarrierName:     carrierName,
		ConfigNamespace: ConfigNamespace,
		Config:          &configDefaults,
		Items:           items(),
		Stance:          stance,
		Secrets:         secrets,
		Open:            open,
	})
}

func stance(cfg *moduleConfig) (core.Enablement, error) {
	if cfg.Region == "" && cfg.DefaultIdentity == "" && len(cfg.Identities) == 0 &&
		cfg.AccessKeyID == "" && cfg.AccessKeySecret == "" {
		return core.Enablement{
			State:  core.StateDisabled,
			Reason: ModuleName + " has no configuration: no region, sending identity or credential",
		}, nil
	}
	var missing []string
	if cfg.Region == "" {
		missing = append(missing, regionItem)
	}
	if len(cfg.Identities) == 0 {
		missing = append(missing, identitiesItem)
	}
	if cfg.DefaultIdentity == "" {
		missing = append(missing, defaultIdentityItem)
	}
	if len(missing) > 0 {
		return core.Enablement{}, fmt.Errorf("%w: %s is partially configured; %s missing",
			sms.ErrInvalidConfig, ConfigNamespace, strings.Join(missing, ", "))
	}
	if _, err := cfg.resolve(); err != nil {
		return core.Enablement{}, err
	}
	return core.Enablement{State: core.StateEnabled}, nil
}

func secrets(cfg *moduleConfig) []string {
	return []string{cfg.AccessKeySecret}
}

func open(ctx context.Context, cfg *moduleConfig) (sms.Product, error) {
	conf, err := cfg.resolve()
	if err != nil {
		return nil, err
	}
	client, err := newClient(ctx, conf)
	if err != nil {
		return nil, fmt.Errorf("%s: building the carrier client: %w", ModuleName, err)
	}
	return newSender(conf, client), nil
}

// The retry count is forced to one attempt: a second call is a second message,
// and the vendor interface is not idempotent.
func newClient(ctx context.Context, conf resolved) (*pinpointsmsvoicev2.Client, error) {
	options := []func(*awsconfig.LoadOptions) error{awsconfig.WithRegion(conf.region)}
	if conf.accessKeyID != "" {
		options = append(options, awsconfig.WithCredentialsProvider(
			awscreds.NewStaticCredentialsProvider(conf.accessKeyID, conf.accessKeySecret, "")))
	}
	if conf.endpoint != "" {
		options = append(options, awsconfig.WithBaseEndpoint(endpointURL(conf.endpoint)))
	}
	base, err := awsconfig.LoadDefaultConfig(ctx, options...)
	if err != nil {
		return nil, err
	}
	return pinpointsmsvoicev2.NewFromConfig(base, func(o *pinpointsmsvoicev2.Options) {
		// One attempt in total, not one retry: the SDK counts the first call.
		o.RetryMaxAttempts = 1
	}), nil
}

func endpointURL(endpoint string) string {
	endpoint = strings.TrimSpace(endpoint)
	if strings.Contains(endpoint, "://") {
		return endpoint
	}
	return "https://" + endpoint
}

func newSender(cfg resolved, client carrier) *sender {
	return &sender{
		client:               client,
		configurationSetName: cfg.configurationSetName,
		defaultIdentity:      cfg.defaultIdentity,
		identities:           cfg.identities,
		byName:               cfg.byName,
		dupe:                 dedupe.New[sms.Receipt](dedupe.DefaultCapacity),
	}
}

// Name reports the name this carrier answers under, on both seams.
func (s *sender) Name() string { return carrierName }

// Model reports what this carrier can carry: the rendered body over a declared
// identity, no per-template review, an asynchronous final state, IN_TEMPLATE_ID and
// a 1600-rune body bound; Countries is empty because each identity states its coverage.
func (s *sender) Model() sms.CarrierModel {
	identities := make([]sms.Identity, len(s.identities))
	for i, identity := range s.identities {
		identity.Countries = append([]string(nil), identity.Countries...)
		attributes := make(map[string]string, len(identity.Attributes))
		for key, value := range identity.Attributes {
			attributes[key] = value
		}
		identity.Attributes = attributes
		identities[i] = identity
	}
	return sms.CarrierModel{
		Content:       sms.ContentSupport{TextBody: true},
		Review:        sms.ReviewSupport{Originator: true},
		Identities:    identities,
		AttributeKeys: []string{inTemplateID},
		MaxBodyRunes:  1600,
		AsyncReceipt:  true,
	}
}

// Send hands one resolved message to the carrier, at most once per stable
// identifier. A repeated identifier is answered with the earlier outcome; an
// empty identifier has nothing to guard by.
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
	identity, err := s.identity(r.Originator, r.To)
	if err != nil {
		return sms.Receipt{}, err
	}
	request := &pinpointsmsvoicev2.SendTextMessageInput{
		DestinationPhoneNumber: awssdk.String(r.To),
		MessageBody:            awssdk.String(r.Body),
		OriginationIdentity:    awssdk.String(identity.Value),
		MessageType:            messageType(r.Kind),
	}
	if s.configurationSetName != "" {
		request.ConfigurationSetName = awssdk.String(s.configurationSetName)
	}
	if r.ValidFor > 0 {
		request.TimeToLive = awssdk.Int32(ttlSeconds(r.ValidFor))
	}
	if parameters := destinationParameters(r, identity); len(parameters) > 0 {
		request.DestinationCountryParameters = parameters
	}

	response, err := s.client.SendTextMessage(ctx, request)
	if err != nil {
		return sms.Receipt{}, classify(err)
	}
	if response == nil {
		return sms.Receipt{}, sms.NoStatus(carrierName)
	}
	return sms.Receipt{ProviderID: awssdk.ToString(response.MessageId)}, nil
}

// Offer answers the empty code and calls no vendor: AWS has no per-template
// review, so there is nothing to create at the vendor.
func (s *sender) Offer(_ context.Context, _ sms.Submission) (string, error) {
	return "", nil
}

// Approval answers ApprovalNotRequired: no vendor review exists for a template
// this carrier sends. It is not ApprovalUnknown.
func (s *sender) Approval(_ context.Context, _ string) (sms.Approval, error) {
	return sms.Approval{State: sms.ApprovalNotRequired}, nil
}

// Retire is a no-op: the vendor holds nothing this carrier created.
func (s *sender) Retire(_ context.Context, _ string) error { return nil }

func (s *sender) identity(name, to string) (sms.Identity, error) {
	if name == "" {
		name = s.defaultIdentity
	}
	identity, ok := s.byName[name]
	if !ok {
		return sms.Identity{}, fmt.Errorf("%w: this deployment has no sending identity %q",
			sms.ErrRejected, name)
	}
	if country, known := destinationCountry(to); known && !serves(identity.Countries, country) {
		return sms.Identity{}, fmt.Errorf("%w: the identity %q does not cover %s",
			sms.ErrUnroutable, identity.Name, country)
	}
	return identity, nil
}

func destinationCountry(to string) (string, bool) {
	if strings.HasPrefix(strings.TrimSpace(to), indiaCallingCode) {
		return indiaCountry, true
	}
	return "", false
}

// An empty list means the identity declares no restriction of its own.
func serves(countries []string, country string) bool {
	if len(countries) == 0 {
		return true
	}
	for _, covered := range countries {
		if strings.EqualFold(strings.TrimSpace(covered), country) {
			return true
		}
	}
	return false
}

func destinationParameters(r sms.Resolved, identity sms.Identity) map[string]string {
	if _, ok := destinationCountry(r.To); !ok {
		return nil
	}
	parameters := make(map[string]string, 2)
	if value := r.Attributes[inTemplateID]; value != "" {
		parameters[inTemplateID] = value
	}
	if value := identity.Attributes[inEntityID]; value != "" {
		parameters[inEntityID] = value
	}
	if len(parameters) == 0 {
		return nil
	}
	return parameters
}

func messageType(kind sms.Kind) types.MessageType {
	if kind == sms.KindMarketing {
		return types.MessageTypePromotional
	}
	return types.MessageTypeTransactional
}

func ttlSeconds(validFor time.Duration) int32 {
	seconds := validFor / time.Second
	switch {
	case seconds < 1:
		return 1
	case seconds > math.MaxInt32:
		return math.MaxInt32
	}
	return int32(seconds)
}
