// Package aws delivers the SMS carrier seams over AWS End User Messaging SMS,
// through its Pinpoint SMS Voice v2 API.
//
// It is a carrier module and nothing else. The sending seam sends the body the
// template registry rendered, from one of the deployment's declared identities,
// builds one SendTextMessage request, calls the carrier once and classifies what
// comes back into the sending sentinels. The template-management seam is a
// declaration and no vendor work: AWS has no per-template review, so a template
// is recorded by the registry alone, Approval answers ApprovalNotRequired and
// Retire removes nothing at the vendor. It holds no challenge state, counts
// nothing, retries nothing and knows no template set of its own: which template a
// message goes out under is decided by the template registry, and this carrier is
// handed the body it rendered.
//
// A host takes it in by writing ModuleName into a command. It delivers
// sms.Sender and sms.Provider exclusively, so a command names exactly one carrier
// and two at once fail in resolution. The whole leaf package binds the vendor SDK
// and can be dropped without touching any other module.
//
// The carrier never learns who the caller is and this module never logs what it
// sends: the secret access key is registered as a literal redaction rule, and the
// message body, the parameter values and the credentials reach no log, no error
// text and no metric.
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

// carrierName is the name this carrier reports through sms.Sender.Name and
// sms.Provider.Name. It is a constant of the code: the assembly names exactly one
// carrier, and a host reading a diagnostic has to be able to tell which.
const carrierName = "aws"

// The vendor's target-country parameter keys for an Indian destination. AWS takes
// them in DestinationCountryParameters and requires both for an Indian sender ID:
// the template identifier the sender ID was registered with, and the entity that
// owns the registration. The template identifier is a template attribute, bounded
// by the carrier model; the entity identifier lives on the sending identity.
const (
	inTemplateID = "IN_TEMPLATE_ID"
	inEntityID   = "IN_ENTITY_ID"
)

// indiaCallingCode and indiaCountry are the one destination this carrier derives a
// country for: the Indian DLT parameters have to be laid out for it, so the
// calling code is already read here. Country lists use ISO 3166-1 alpha-2 codes.
const (
	indiaCallingCode = "+91"
	indiaCountry     = "IN"
)

// carrier is the set of operations this module calls on the vendor client. It is
// an interface rather than the client itself so that no vendor type appears on
// this package's surface, and so that a case can hand the adapter a stand-in
// without a network. It holds only the sending call: AWS has no per-template
// review, so the management seam does not reach the vendor at all.
type carrier interface {
	SendTextMessage(ctx context.Context, params *pinpointsmsvoicev2.SendTextMessageInput, optFns ...func(*pinpointsmsvoicev2.Options)) (*pinpointsmsvoicev2.SendTextMessageOutput, error)
}

// sender is this module's product: it answers both carrier seams over one client.
type sender struct {
	client carrier
	// configurationSetName is the vendor configuration set sends are filed
	// under, empty for the vendor's own default.
	configurationSetName string
	// defaultIdentity names the identity a message that names none goes out
	// from.
	defaultIdentity string
	// identities are the declared sending identities, in declaration order, and
	// byName is the same set keyed by the name a message names one by.
	identities []sms.Identity
	byName     map[string]sms.Identity
	// dupe remembers the sends this instance has made, keyed by the resolved
	// message's stable identifier, so the non-idempotent carrier is called at most
	// once per identifier.
	dupe *dedupe.Table[sms.Receipt]
}

// sender must satisfy the seams it delivers.
var (
	_ sms.Sender   = (*sender)(nil)
	_ sms.Provider = (*sender)(nil)
)

// init registers this module with the process registry. A host chooses its
// carrier by importing it, and what enters its go.mod follows the packages it
// imported: a host that takes only this carrier binds only this vendor's SDK.
func init() { core.ProcessRegistry.Register(Module()) }

// Module is the descriptor a host registers.
//
// Exclusive is set on both seams, which is what makes a command name exactly one
// carrier: two of them enabled at once fail in resolution. The stance reads the
// configuration, so a section this module cannot run on is a startup failure
// ahead of every construction, not a surprise when a message is first sent. The
// logging capability is taken up as an optional requirement, because the secret
// access key is registered as a literal redaction rule and an assembly with no
// logging has nothing to register it with.
func Module() core.Module {
	return sms.NewModule(sms.Spec[moduleConfig]{
		ModuleName:      ModuleName,
		ConfigNamespace: ConfigNamespace,
		Config:          &configDefaults,
		Items:           items(),
		Stance:          stance,
		Secrets:         secrets,
		Open:            open,
	})
}

// stance states that the module runs once its configuration is sound: a carrier
// named on a command tree is taken as running. The items it cannot run without
// are declared required, so the loader has already refused a section that omits
// one before this is reached; the identity list's own consistency is checked in
// resolve, which open calls before the client is built.
func stance(_ *moduleConfig) (core.Enablement, error) {
	return core.Enablement{State: core.StateEnabled}, nil
}

// secrets returns the values that have to be masked wherever the process logs
// them: the secret access key, which signs every call. The access key identifier
// is not a secret and is not registered.
func secrets(cfg *moduleConfig) []string {
	return []string{cfg.AccessKeySecret}
}

// open builds the product over a carrier client. Production passes the vendor
// client; a case passes the same client pointed at a loopback endpoint.
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

// newClient builds the vendor client this carrier calls through.
//
// The region is always configured; the endpoint and the credentials only when the
// host names them. With no access key the SDK's own default credential chain is
// left in place — the roles, profiles and environment a deployment on AWS already
// has — and the client is otherwise identical. The retry count is forced to one
// attempt: a second call is a second message, and the vendor interface is not
// idempotent.
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

// endpointURL reads a configured endpoint as the SDK takes it.
//
// The SDK's base endpoint is a URL, while an operator is as likely to write a
// bare host as a full one. A value with no scheme takes https, which is what an
// AWS service endpoint is; a value carrying one — a loopback server in a case, a
// gateway over http in a deployment — is left as written.
func endpointURL(endpoint string) string {
	endpoint = strings.TrimSpace(endpoint)
	if strings.Contains(endpoint, "://") {
		return endpoint
	}
	return "https://" + endpoint
}

// newSender builds the product over a carrier client. It takes the values a send
// reads and not the whole configuration: the credentials belong to the client.
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

// Name reports the name this carrier answers under, on both seams. It is a
// constant: the assembly names exactly one carrier, and the name identifies it in
// a diagnostic.
func (s *sender) Name() string { return carrierName }

// Model reports what this carrier can carry.
//
// AWS sends the body the registry rendered, so Content.TextBody is set and
// Content.TemplateCode is not. It reviews no template per se — the registry
// records the body locally and nothing waits on a vendor verdict — so
// Review.PerTemplate is false, and a send may name any of the deployment's
// identities, so Review.Originator is true. The identities are the ones the
// configuration declares, with their kinds, values, countries and attributes.
// IN_TEMPLATE_ID is the one template attribute this carrier accepts: the Indian
// DLT template identifier. A body is bounded at 1600 runes, and the vendor's final
// delivery state arrives asynchronously, so a synchronous answer is acceptance and
// not delivery.
//
// Countries is empty: the carrier reaches every country this deployment's
// identities do, and each identity states its own coverage. The list is copied so
// a caller cannot change what this process holds.
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
// identifier.
//
// A message whose identifier was seen before is not sent again: the caller is
// answered with the earlier call's outcome, so the non-idempotent carrier is
// reached at most once per identifier. An empty identifier has nothing to guard
// by, so the message is delivered directly; the caller is expected to set one
// from the challenge.
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

// deliver builds one SendTextMessage request from a resolved message and calls
// the carrier once.
//
// The body and its parameters are already decided: which template a message goes
// out under is the registry's, and the registry has already rendered the declared
// body. This carrier chooses the sending identity, lays out the vendor's fields
// and nothing else. MessageType is TRANSACTIONAL for a verification or a
// notification and PROMOTIONAL for a marketing message; TimeToLive carries the
// requested lifetime, and the vendor's own default stands when none was asked
// for. An Indian destination also carries the DLT pair AWS requires. The call
// carries the caller's context, so a context that ends cancels the in-flight
// request and is answered with the uncertain sentinel, the same direction as a
// timeout the carrier itself reports. Nothing here is retried: the vendor
// interface is not idempotent, and a second call would deliver the message twice
// and bill it twice.
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

// Offer answers the empty code and calls no vendor.
//
// AWS has no per-template review: a template's body lives in the registry's own
// record, and there is nothing to create at the vendor for the registry to be
// handed back. The empty code is the one the registry files the submission under.
func (s *sender) Offer(_ context.Context, _ sms.Submission) (string, error) {
	return "", nil
}

// Approval answers ApprovalNotRequired.
//
// No vendor review exists for a template this carrier sends, so there is nothing
// to wait on. It is not ApprovalUnknown: that state says a review exists and this
// process does not follow it.
func (s *sender) Approval(_ context.Context, _ string) (sms.Approval, error) {
	return sms.Approval{State: sms.ApprovalNotRequired}, nil
}

// Retire is a no-op: the vendor holds nothing this carrier created.
func (s *sender) Retire(_ context.Context, _ string) error { return nil }

// identity chooses the sending identity for one message.
//
// An empty name is the deployment's default identity; a name it does not declare
// is a caller defect and is refused with ErrRejected before any vendor call. An
// identity that declares coverage and does not cover the destination's country is
// a deployment gap and is refused with ErrUnroutable. The country is derived only
// for an Indian destination, the one country this carrier already has to
// understand for the DLT parameters; every other destination is left to the
// vendor, whose own registration checks are out of this capability's scope.
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

// destinationCountry reports the country a destination belongs to, for the
// countries this carrier derives. Only India is derived, because only India needs
// the DLT parameters; the rest is the vendor's to judge.
func destinationCountry(to string) (string, bool) {
	if strings.HasPrefix(strings.TrimSpace(to), indiaCallingCode) {
		return indiaCountry, true
	}
	return "", false
}

// serves reports whether an identity covers a country. An empty list means the
// identity declares no restriction of its own, so it serves every destination the
// carrier reaches.
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

// destinationParameters lays out the vendor's target-country parameters for an
// Indian destination: the template identifier the template carries and the entity
// identifier the sending identity carries. Any other destination carries none. A
// value the declaration did not give is left out rather than sent empty, so the
// vendor's own answer names what is missing.
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

// messageType maps a template's kind to the vendor's message type: a message the
// recipient has to act on now is transactional, and a marketing message is
// promotional. Verification and notification are both transactional, and a
// declaration that names no kind gets notification.
func messageType(kind sms.Kind) types.MessageType {
	if kind == sms.KindMarketing {
		return types.MessageTypePromotional
	}
	return types.MessageTypeTransactional
}

// ttlSeconds reads a requested lifetime as the whole seconds the vendor takes.
//
// The vendor's field is a count of seconds, so a lifetime is rounded down; one
// below a second becomes one second rather than the zero that would mean "the
// vendor's default", and a lifetime beyond the field's range is clamped to its
// maximum rather than wrapping negative.
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
