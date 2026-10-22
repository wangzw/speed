// Package twilio delivers the SMS carrier seams over Twilio's Messages and
// Content APIs.
//
// It is a carrier module and nothing else. The sending seam lays a resolved
// message out the way the Messages API wants it — the destination, the content
// template the registry resolved, the ordered values as the template's
// positional content variables, and the identity the message goes out from —
// calls the vendor once and classifies what comes back into the sending
// sentinels. The management seam creates a content template over the Content
// API, answers the absence of a per-template review, and removes content. It
// holds no challenge state, counts nothing, retries nothing and knows no
// template set of its own: which template a message goes out under is decided by
// the template registry, and this carrier holds only the content SID it is
// handed.
//
// A host takes it in by writing ModuleName into a command. It delivers
// sms.Sender and sms.Provider exclusively, so a command names exactly one carrier
// and two at once fail in resolution. The whole leaf package binds the vendor SDK
// and can be dropped without touching any other module.
//
// The carrier never learns who the caller is and this module never logs what it
// sends: the auth token is registered as a literal redaction rule, and the
// rendered body, the parameter values and the credentials reach no log, no error
// text and no metric. Registration — a US 10DLC brand or campaign, toll-free
// verification, a short code — and delivery receipts stay outside this package:
// it declares the identities and the countries they serve and tracks neither
// state, and the vendor's final delivery status arrives out of band.
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

// carrierName is the name this carrier reports through sms.Sender.Name and
// sms.Provider.Name. It is a constant of the code: the assembly names exactly one
// carrier, and a host reading a diagnostic has to be able to tell which.
const carrierName = "twilio"

// fromAttribute is the identity attribute that pins the sender a message goes
// out from. Beside a messaging service it names one sender of the service's
// pool; it may also carry the sender of any other identity.
const fromAttribute = "from"

// init registers this module with the process registry. A host chooses its
// carrier by importing it, and what enters its go.mod follows the packages it
// imported: a host that takes only this carrier binds only this vendor's SDK.
func init() { core.ProcessRegistry.Register(Module()) }

// Module is the descriptor a host registers.
//
// Exclusive is set on both seams, which is what makes a command name exactly one
// carrier: two of them enabled at once fail in resolution. The stance reads the
// configuration, so a section this module cannot run on is a startup failure
// ahead of every construction. The logging capability is taken up as an optional
// requirement, because the auth token is registered as a literal redaction rule
// and an assembly with no logging has nothing to register it with.
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
// one before this is reached.
func stance(_ *moduleConfig) (core.Enablement, error) {
	return core.Enablement{State: core.StateEnabled}, nil
}

// secrets returns the values that have to be masked wherever the process logs
// them: the auth token, which authenticates every call.
func secrets(cfg *moduleConfig) []string {
	return []string{cfg.AuthToken}
}

// open builds the product over the vendor clients.
func open(_ context.Context, cfg *moduleConfig) (sms.Product, error) {
	conf, err := cfg.resolve()
	if err != nil {
		return nil, err
	}
	return newSender(conf), nil
}

// endpointClient is the SDK's base client with its two public access points
// replaced by the configured ones.
//
// The generated services hard-code https://api.twilio.com and
// https://content.twilio.com, and the SDK offers no setter for either. The one
// seam it does offer is the base client, which receives the finished URL: this
// wrapper rewrites the host of a request aimed at one of the two public
// endpoints and delegates everything else — credentials, form encoding, error
// decoding — to the SDK's own client. It is what lets a host point the carrier at
// a gateway, and what lets a case point it at a loopback server.
type endpointClient struct {
	inner       twclient.BaseClient
	apiBase     *url.URL
	contentBase *url.URL
}

// endpointClient must satisfy the seam the SDK builds its request handler over.
var _ twclient.BaseClient = (*endpointClient)(nil)

// AccountSid reports the account the request paths are built under.
func (c *endpointClient) AccountSid() string { return c.inner.AccountSid() }

// SetTimeout sets the timeout the underlying HTTP client applies.
func (c *endpointClient) SetTimeout(timeout time.Duration) { c.inner.SetTimeout(timeout) }

// SetOauth sets the OAuth provider the underlying client authenticates with.
func (c *endpointClient) SetOauth(auth twclient.OAuth) { c.inner.SetOauth(auth) }

// OAuth reports the OAuth provider the underlying client authenticates with.
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

// rewrite swaps the public host of a request for the configured one. A URL that
// names neither public endpoint is left alone: nothing this module sends aims
// anywhere else, and guessing would send a request to a host the operator did not
// configure.
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

// isEndpoint reports whether a host is the vendor's public endpoint for a
// product: api.twilio.com or content.twilio.com, and the region and edge
// variants the SDK's request handler can build from them.
func isEndpoint(host, product string) bool {
	return strings.HasPrefix(host, product+".") && strings.HasSuffix(host, ".twilio.com")
}

// sender is this module's product: it answers both carrier seams over one
// account.
type sender struct {
	messages *messages.ApiService
	content  *content.ApiService
	// identities is the deployment's identity declaration in a stable order, and
	// identityByName is what Send looks an originator up in.
	identities     []sms.Identity
	identityByName map[string]sms.Identity
	// defaultIdentity is the identity a message that names none goes out from.
	defaultIdentity string
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

// newSender builds the product over the configured access points.
//
// The SDK's own client holds the credentials and does the form encoding and error
// decoding; the wrapper only redirects the two public hosts. The client is left
// with the SDK's default timeout, which bounds a call that never answers and is
// classified as uncertain, and with its default of not following redirects, so a
// redirect cannot turn one send into two.
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

// Name reports the name this carrier answers under, on both seams. It is a
// constant: the assembly names exactly one carrier, and the name identifies it in
// a diagnostic.
func (s *sender) Name() string { return carrierName }

// Model reports what this carrier can carry.
//
// Twilio sends by the vendor's content template SID, so a template this carrier
// carries holds a code per language. The vendor has no per-template review for
// the content resources this module creates, so a template is sendable without
// waiting on one, and Approval answers ApprovalNotRequired. A send may pick its
// identity among the deployment's declared ones (Review.Originator), and the
// vendor's final delivery status arrives asynchronously (AsyncReceipt): the
// synchronous answer is the message SID, and acceptance is not delivery. It
// declares no attribute keys, no body bound of its own and no carrier-wide
// country restriction; the identities carry the countries they serve.
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

// deliver builds one CreateMessage request from a resolved message and calls the
// carrier once.
//
// The content template and its ordered values are already decided: which
// template a message goes out under is the registry's, and this carrier only
// lays the values out the way its vendor wants them. The vendor has no body
// form for a content template — the content resource holds the text — so no Body
// is sent beside the ContentSid. Nothing here is retried: the vendor interface is
// not idempotent, and a second call would deliver the message twice and bill it
// twice.
//
// The caller's context is checked before the call. The SDK's request path takes
// no context of its own, so a context that ends after this point cannot cancel
// the in-flight request; the call then runs to the SDK's timeout and is
// classified by what came back.
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

// identityFor reads the identity a message goes out from: the one it names, or
// the deployment's default when it names none. A name this deployment does not
// declare is the caller's defect and is refused before the carrier is reached.
//
// The identity's declared countries are carried into the carrier declaration but
// not compared here: the design fixes no form for a country value, and a
// comparison against an opaque string would refuse messages for a reason the
// operator cannot predict. Whether an identity reaches a destination is the
// vendor's answer, classified by Send.
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

// messagingServiceSID reports the messaging service a send goes through, when the
// identity is one. The service owns a pool of senders, so the message names the
// service rather than a number.
func messagingServiceSID(identity sms.Identity) string {
	if identity.Kind == sms.IdentityMessagingService {
		return identity.Value
	}
	return ""
}

// senderOf reports the From value a send carries.
//
// The "from" attribute wins when it is set: beside a messaging service it pins
// one sender of the service's pool, and beside any other identity it is the
// deployment's own override. With no attribute, a non-service identity sends
// From its value — a number, a short code, a sender ID — and a service identity
// sends no From at all, letting the vendor pick from the pool.
func senderOf(identity sms.Identity) string {
	if from := identity.Attributes[fromAttribute]; from != "" {
		return from
	}
	if identity.Kind == sms.IdentityMessagingService {
		return ""
	}
	return identity.Value
}

// contentVariables lays the ordered values out as the template's positional
// content variables: the first value is "1", the second "2", and so on. The
// order comes from the declaration, so it is the order the template was
// approved with.
func contentVariables(ordered []string) map[string]string {
	variables := make(map[string]string, len(ordered))
	for i, value := range ordered {
		variables[strconv.Itoa(i+1)] = value
	}
	return variables
}
