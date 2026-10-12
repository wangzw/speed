// Package aliyun delivers the SMS sending seam over Aliyun's Dysmsapi.
//
// It is a carrier module and nothing else: it maps this side's (purpose,
// language) pair to an approved template code, builds one SendSms request,
// calls the carrier once and classifies what comes back into the sending
// sentinels. It holds no challenge state, counts nothing, retries nothing and
// knows no purpose beyond the template key it is handed.
//
// A host takes it in by writing ModuleName into a command. It delivers
// authn.SMSSender exclusively, so a command names exactly one carrier and two
// at once fail in resolution. The whole leaf package binds the vendor SDK and
// can be dropped without touching any other module
// (ADR-authn-packaging-2026-10-10).
//
// The carrier never learns who the caller is and this module never logs what it
// sends: the access-key secret is registered as a literal redaction rule, and
// the message code and the credentials reach no log, no error text and no
// metric.
package aliyun

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	openapiutil "github.com/alibabacloud-go/darabonba-openapi/v2/utils"
	dysmsapi "github.com/alibabacloud-go/dysmsapi-20170525/v5/client"
	"github.com/alibabacloud-go/tea/dara"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/literal"
	"github.com/vislake/speed/pkg/authn/sms/internal/dedupe"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/log"
)

// ModuleName is this module's name in the registry.
const ModuleName = "authn.sms.aliyun"

// ConfigNamespace is where this module's input items are mounted.
const ConfigNamespace = ModuleName

// carrierName is the name this carrier reports through authn.SMSSender.Name. It
// is a constant of the code: the assembly names exactly one carrier, and a host
// reading a diagnostic has to be able to tell which.
const carrierName = "aliyun"

// okCode is the status code the carrier answers with once it has taken the
// message. Every other value, the empty one included, is a refusal.
const okCode = "OK"

// carrier is the one operation this module calls on the vendor client. It is an
// interface rather than the client itself so that a case can drive the adapter
// over the same client pointed at a loopback endpoint, and so that no vendor
// type appears on this package's surface.
type carrier interface {
	SendSmsWithContext(ctx context.Context, request *dysmsapi.SendSmsRequest, runtime *dara.RuntimeOptions) (*dysmsapi.SendSmsResponse, error)
}

// sender is this module's product.
type sender struct {
	client carrier
	// signName is the approved signature every message is sent under.
	signName string
	// templates maps this side's template key, "purpose/locale", to the
	// carrier's approved template code. It covers every (purpose, locale)
	// combination, which the constructor checked.
	templates map[string]string
	// dupe remembers the sends this instance has made, keyed by the message's
	// stable identifier, so the non-idempotent carrier is called at most once
	// per identifier.
	dupe *dedupe.Table[authn.SendReceipt]
}

// sender must satisfy the capability it delivers.
var _ authn.SMSSender = (*sender)(nil)

// init registers this module with the process registry. A host chooses its
// carrier by importing it, and what enters its go.mod follows the packages it
// imported: a host that takes only this carrier binds only this vendor's SDK.
func init() { core.ProcessRegistry.Register(Module()) }

// Module is the descriptor a host registers.
//
// Exclusive is set on the capability, which is what makes a command name exactly
// one carrier: two of them enabled at once fail in resolution. Prepare is
// declared and reads the configuration, so a section this module cannot run on —
// a required item nobody gave, a templates map that misses a (purpose, locale)
// combination — is a startup failure ahead of every construction, not a
// surprise when a code is first sent.
//
// The logging capability is taken up as an optional requirement, because the
// access-key secret is registered as a literal redaction rule and an assembly
// with no logging has nothing to register it with.
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
			client, err := dysmsapi.NewClient(&openapiutil.Config{
				AccessKeyId:     dara.String(cfg.accessKeyID),
				AccessKeySecret: dara.String(cfg.accessKeySecret),
				Endpoint:        dara.String(cfg.endpoint),
			})
			if err != nil {
				return nil, fmt.Errorf("%s: building the carrier client: %w", ModuleName, err)
			}
			if err := registerSecret(reg, cfg.accessKeySecret); err != nil {
				return nil, err
			}
			return newSender(cfg, client), nil
		},
	}
}

// readerOf takes up the configuration reader. Its absence means the host never
// handed one to the registry, which is core's own failure and is carried out as
// it stands; reading it as "nothing was configured" would lose the carrier in
// silence.
func readerOf(reg *core.Registry) (config.Reader, error) {
	reader, err := core.Resolve[config.Reader](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: the carrier settings are read from configuration, and no reader "+
			"was handed to the registry before the assembly began: %w", ModuleName, err)
	}
	return reader, nil
}

// registerSecret registers the access-key secret as a literal redaction rule,
// so it is masked wherever it turns up in a log line. An assembly with no
// logging has nothing to register it with and registers nothing; a registration
// after a logger has bound an attribute would not reach what was bound, so this
// runs in New, before any logger this module would take.
func registerSecret(reg *core.Registry, secret string) error {
	delivered, err := core.Resolve[log.Logger](reg)
	switch {
	case errors.Is(err, core.ErrMissingProvider):
		return nil
	case err != nil:
		return fmt.Errorf("%s: taking up the logging capability to register the access-key "+
			"secret with: %w", ModuleName, err)
	}
	literal.Register(delivered.Redaction(), ModuleName+".secret", secret)
	return nil
}

// newSender builds the product over a carrier client. Production passes the
// vendor client; a case passes the same client pointed at a loopback endpoint.
func newSender(cfg resolved, client carrier) *sender {
	return &sender{
		client:    client,
		signName:  cfg.signName,
		templates: cfg.templates,
		dupe:      dedupe.New[authn.SendReceipt](dedupe.DefaultCapacity),
	}
}

// Name reports the name this carrier answers under. It is a constant: the
// assembly names exactly one carrier, and the name identifies it in a
// diagnostic.
func (s *sender) Name() string { return carrierName }

// Send hands one message to the carrier, at most once per stable identifier.
//
// A message whose identifier was seen before is not sent again: the caller is
// answered with the earlier call's outcome, so the non-idempotent carrier is
// reached at most once per identifier. An empty identifier has nothing to guard
// by, so the message is delivered directly; the caller is expected to set one
// from the challenge.
func (s *sender) Send(ctx context.Context, m authn.SMS) (authn.SendReceipt, error) {
	if m.DedupeID == "" {
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

// deliver builds one SendSms request from the message and calls the carrier once.
//
// The template is chosen by this side's (purpose, locale) pair; the parameters
// are the message's own, carried as the JSON string the carrier takes. The call
// carries the caller's context, so a context that ends cancels the in-flight
// request and is answered with the uncertain sentinel, the same direction as a
// timeout the carrier itself reports. Nothing here is retried: the vendor
// interface is not idempotent, and a second call would deliver the message twice
// and bill it twice.
//
// A (purpose, locale) pair the constructor did not cover, and parameters that
// cannot be encoded, are refused before the carrier is reached and are not one
// of the uncertain cases: no message was composed and none was sent.
func (s *sender) deliver(ctx context.Context, m authn.SMS) (authn.SendReceipt, error) {
	templateCode, ok := s.templateFor(m.Template, m.Locale)
	if !ok {
		return authn.SendReceipt{}, fmt.Errorf("%w: no template is configured for the purpose and "+
			"locale this message names", authn.ErrSMSRejected)
	}
	params, err := json.Marshal(m.Params)
	if err != nil {
		return authn.SendReceipt{}, fmt.Errorf("%w: the template parameters cannot be encoded as "+
			"JSON", authn.ErrSMSRejected)
	}
	request := &dysmsapi.SendSmsRequest{
		PhoneNumbers:  dara.String(m.To),
		SignName:      dara.String(s.signName),
		TemplateCode:  dara.String(templateCode),
		TemplateParam: dara.String(string(params)),
	}
	// The SDK's own retry is switched off: it would call the non-idempotent
	// interface again, which this module must never do.
	runtime := &dara.RuntimeOptions{Autoretry: dara.Bool(false)}

	resp, err := s.client.SendSmsWithContext(ctx, request, runtime)
	return interpreted(answered{resp: resp, err: err})
}

// templateFor looks up the carrier template code for a (purpose, locale) pair.
// An empty locale reads zh-CN, as the code the caller fills in does.
func (s *sender) templateFor(purpose, locale string) (string, bool) {
	if locale == "" {
		locale = defaultLocale
	}
	code, ok := s.templates[purpose+"/"+locale]
	return code, ok && code != ""
}

// answered is one carrier answer: the response, or the error the call failed
// with.
type answered struct {
	resp *dysmsapi.SendSmsResponse
	err  error
}

// interpreted turns a carrier answer into a receipt or a sending sentinel.
//
// A call that failed, a response with no body and a status that is not OK are
// all uncertain unless the table says otherwise: the vendor interface is not
// idempotent, so a call whose outcome cannot be read may still have delivered
// the message. The vendor error is not wrapped into what is returned: its text
// carries the request, and the request carries the message code.
func interpreted(a answered) (authn.SendReceipt, error) {
	if a.err != nil {
		return authn.SendReceipt{}, fmt.Errorf("%w: the carrier call did not complete", authn.ErrSMSUncertain)
	}
	if a.resp == nil || a.resp.Body == nil {
		return authn.SendReceipt{}, fmt.Errorf("%w: the carrier answered with no status", authn.ErrSMSUncertain)
	}
	code := value(a.resp.Body.Code)
	if code == okCode {
		return authn.SendReceipt{ProviderID: value(a.resp.Body.BizId)}, nil
	}
	return authn.SendReceipt{}, classified(code)
}

// verifiedSentinels maps a carrier status code to the sending sentinel it means.
//
// It is empty: no Aliyun status code is known yet to mean throttled or rejected,
// so every code the carrier can answer falls to the uncertain sentinel. The
// direction is deliberate — it counts one more budget rather than treating an
// unconfirmed failure as a definite throttle or refusal, and it never resends.
// A code verified against the vendor's list is added here, and the case that
// holds this table gains that code in the same change.
var verifiedSentinels = map[string]error{}

// classified turns a carrier status code into a sending sentinel, naming the
// code in the text. The code is the carrier's own word and carries no credential
// and no message code.
func classified(code string) error {
	sentinel, verified := verifiedSentinels[code]
	if !verified {
		sentinel = authn.ErrSMSUncertain
	}
	return fmt.Errorf("%w: the carrier answered %q", sentinel, code)
}

// value reads a string the carrier may have left absent.
func value(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
