// Package twilio delivers the SMS carrier seams over Twilio Messages and Content
// APIs. It binds the vendor SDK and registers one carrier module.
package twilio

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	twilio "github.com/twilio/twilio-go"
	twclient "github.com/twilio/twilio-go/client"
	messages "github.com/twilio/twilio-go/rest/api/v2010"
	content "github.com/twilio/twilio-go/rest/content/v1"

	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/internal/dedupe"
	"github.com/vislake/speed/pkg/sms"
)

// ModuleName is this module's name in the registry.
const ModuleName = "sms.twilio"

// ConfigNamespace is where this module's input items are mounted.
const ConfigNamespace = ModuleName

const carrierName = "twilio"

const fromAttribute = "from"

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
	if cfg.AccountSID == "" && cfg.AuthToken == "" && cfg.DefaultIdentity == "" && len(cfg.Identities) == 0 {
		return core.Enablement{
			State:  core.StateDisabled,
			Reason: ModuleName + " has no configuration: no account, sending identity or credential",
		}, nil
	}
	var missing []string
	if cfg.AccountSID == "" {
		missing = append(missing, accountSIDItem)
	}
	if cfg.AuthToken == "" {
		missing = append(missing, authTokenItem)
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
	return []string{cfg.AuthToken}
}

func open(_ context.Context, cfg *moduleConfig) (sms.Product, error) {
	conf, err := cfg.resolve()
	if err != nil {
		return nil, err
	}
	return newSender(conf), nil
}

// The generated services hard-code the public hosts and the SDK offers no setter, so this wrapper rewrites the host and delegates the rest.
type endpointClient struct {
	inner       twclient.BaseClient
	apiBase     *url.URL
	contentBase *url.URL
}

var _ twclient.BaseClient = (*endpointClient)(nil)

func (c *endpointClient) AccountSid() string { return c.inner.AccountSid() }

func (c *endpointClient) SetTimeout(timeout time.Duration) { c.inner.SetTimeout(timeout) }

func (c *endpointClient) SetOauth(auth twclient.OAuth) { c.inner.SetOauth(auth) }

func (c *endpointClient) OAuth() twclient.OAuth { return c.inner.OAuth() }

// SendRequest rewrites a request aimed at one of the public endpoints onto the
// configured access point and hands it to the SDK's own client.
func (c *endpointClient) SendRequest(method, rawURL string, data url.Values,
	headers map[string]interface{}, body ...byte,
) (*http.Response, error) {
	rewritten, err := c.rewrite(rawURL)
	if err != nil {
		return nil, err
	}
	return c.inner.SendRequest(method, rewritten, data, headers, body...)
}

func (c *endpointClient) rewrite(rawURL string) (string, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}
	var base *url.URL
	switch {
	case isEndpoint(parsed.Host, "api"):
		base = c.apiBase
	case isEndpoint(parsed.Host, "content"):
		base = c.contentBase
	default:
		return rawURL, nil
	}
	parsed.Scheme = base.Scheme
	parsed.Host = base.Host
	parsed.Path = strings.TrimSuffix(base.Path, "/") + parsed.Path
	return parsed.String(), nil
}

func isEndpoint(host, product string) bool {
	return strings.HasPrefix(host, product+".") && strings.HasSuffix(host, ".twilio.com")
}

type sender struct {
	messages        *messages.ApiService
	content         *content.ApiService
	identities      []sms.Identity
	identityByName  map[string]sms.Identity
	defaultIdentity string
	dupe            *dedupe.Table[sms.Receipt]
}

var (
	_ sms.Sender   = (*sender)(nil)
	_ sms.Provider = (*sender)(nil)
)

// The client keeps the SDK's default of not following redirects, so a redirect
// cannot turn one send into two.
func newSender(cfg resolved) *sender {
	inner := &twclient.Client{Credentials: twclient.NewCredentials(cfg.accountSID, cfg.authToken)}
	inner.SetAccountSid(cfg.accountSID)
	client := &endpointClient{inner: inner, apiBase: cfg.apiBase, contentBase: cfg.contentBase}
	rest := twilio.NewRestClientWithParams(twilio.ClientParams{Client: client})
	return &sender{
		messages:        rest.Api,
		content:         rest.ContentV1,
		identities:      cfg.identities,
		identityByName:  cfg.identityByName,
		defaultIdentity: cfg.defaultIdentity,
		dupe:            dedupe.New[sms.Receipt](dedupe.DefaultCapacity),
	}
}

// Name reports the name this carrier answers under, on both seams.
func (s *sender) Name() string { return carrierName }

// Model reports what this carrier can carry: the vendor content template SID, no
// per-template review, an identity chosen by the send and an asynchronous final
// status; the identities carry the countries they serve.
func (s *sender) Model() sms.CarrierModel {
	identities := make([]sms.Identity, 0, len(s.identities))
	for _, identity := range s.identities {
		identities = append(identities, sms.Identity{
			Name:       identity.Name,
			Kind:       identity.Kind,
			Value:      identity.Value,
			Countries:  append([]string(nil), identity.Countries...),
			Attributes: copyAttributes(identity.Attributes),
		})
	}
	return sms.CarrierModel{
		Content:      sms.ContentSupport{TemplateCode: true},
		Review:       sms.ReviewSupport{Originator: true},
		Identities:   identities,
		AsyncReceipt: true,
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
	identity, err := s.identityFor(r.Originator)
	if err != nil {
		return sms.Receipt{}, err
	}
	if r.Code == "" {
		return sms.Receipt{}, fmt.Errorf("%w: %s sends by the vendor's content template, and the resolved "+
			"message carries no template code", sms.ErrRejected, carrierName)
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return sms.Receipt{}, fmt.Errorf("%w: %s: the context ended before the call", sms.ErrUncertain, carrierName)
	}

	params := &messages.CreateMessageParams{}
	params.SetTo(r.To)
	params.SetContentSid(r.Code)
	if sid := messagingServiceSID(identity); sid != "" {
		params.SetMessagingServiceSid(sid)
	}
	if from := senderOf(identity); from != "" {
		params.SetFrom(from)
	}
	if len(r.Ordered) > 0 {
		variables, marshalErr := json.Marshal(contentVariables(r.Ordered))
		if marshalErr != nil {
			return sms.Receipt{}, fmt.Errorf("%w: the template values cannot be encoded as JSON", sms.ErrRejected)
		}
		params.SetContentVariables(string(variables))
	}
	if r.ValidFor > 0 {
		params.SetValidityPeriod(validityPeriod(r.ValidFor))
	}

	message, err := s.messages.CreateMessage(params)
	if err != nil {
		return sms.Receipt{}, classify(err)
	}
	if message == nil || message.Sid == nil || *message.Sid == "" {
		return sms.Receipt{}, fmt.Errorf("%w: %s took the message and answered no message identifier",
			sms.ErrUncertain, carrierName)
	}
	return sms.Receipt{ProviderID: *message.Sid}, nil
}

const maxValiditySeconds = 36000

func validityPeriod(validFor time.Duration) int {
	seconds := int64(validFor / time.Second)
	switch {
	case seconds < 1:
		return 1
	case seconds > maxValiditySeconds:
		return maxValiditySeconds
	default:
		return int(seconds)
	}
}

// Countries are not compared here: a country value has no fixed form, and the
// vendor judges reach.
func (s *sender) identityFor(name string) (sms.Identity, error) {
	if name == "" {
		name = s.defaultIdentity
	}
	identity, ok := s.identityByName[name]
	if !ok {
		return sms.Identity{}, fmt.Errorf("%w: %s has no sending identity %q", sms.ErrRejected, carrierName, name)
	}
	return identity, nil
}

func messagingServiceSID(identity sms.Identity) string {
	if identity.Kind == sms.IdentityMessagingService {
		return identity.Value
	}
	return ""
}

func senderOf(identity sms.Identity) string {
	if from := identity.Attributes[fromAttribute]; from != "" {
		return from
	}
	if identity.Kind == sms.IdentityMessagingService {
		return ""
	}
	return identity.Value
}

func contentVariables(ordered []string) map[string]string {
	variables := make(map[string]string, len(ordered))
	for i, value := range ordered {
		variables[strconv.Itoa(i+1)] = value
	}
	return variables
}
