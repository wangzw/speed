// Package aliyun delivers the SMS carrier seams over Aliyun's Dysmsapi.
//
// It is a carrier module and nothing else. The sending seam lays a resolved
// message's parameters out the way Aliyun wants them, builds one SendSms request,
// calls the carrier once and classifies what comes back into the sending
// sentinels; the template-management seam offers a template for review, reads the
// review state and retires a template, all over the same client. It holds no
// challenge state, counts nothing, retries nothing and knows no template set of
// its own: which template a message goes out under is decided by the template
// registry, and this carrier holds only the vendor code it is handed.
//
// A host takes it in by writing ModuleName into a command. It delivers
// sms.Sender and sms.Provider exclusively, so a command names exactly one carrier
// and two at once fail in resolution. The whole leaf package binds the vendor SDK
// and can be dropped without touching any other module.
//
// The carrier never learns who the caller is and this module never logs what it
// sends: the access-key secret is registered as a literal redaction rule, and the
// message code and the credentials reach no log, no error text and no metric.
package aliyun

import (
	"context"
	"encoding/json"
	"fmt"

	openapiutil "github.com/alibabacloud-go/darabonba-openapi/v2/utils"
	dysmsapi "github.com/alibabacloud-go/dysmsapi-20170525/v5/client"
	"github.com/alibabacloud-go/tea/dara"

	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/internal/dedupe"
	"github.com/vislake/speed/pkg/sms"
)

// ModuleName is this module's name in the registry.
const ModuleName = "sms.aliyun"

// ConfigNamespace is where this module's input items are mounted.
const ConfigNamespace = ModuleName

// carrierName is the name this carrier reports through sms.Sender.Name and
// sms.Provider.Name. It is a constant of the code: the assembly names exactly one
// carrier, and a host reading a diagnostic has to be able to tell which.
const carrierName = "aliyun"

// okCode is the status code the carrier answers with once it has taken a request.
// Every other value, the empty one included, is a refusal.
const okCode = "OK"

// carrier is the set of operations this module calls on the vendor client. It is
// an interface rather than the client itself so that a case can drive the adapter
// over the same client pointed at a loopback endpoint, and so that no vendor type
// appears on this package's surface. The sending call and the template calls are
// one interface because they are one client: a carrier delivers both seams over a
// single vendor account.
type carrier interface {
	SendSmsWithContext(ctx context.Context, request *dysmsapi.SendSmsRequest, runtime *dara.RuntimeOptions) (*dysmsapi.SendSmsResponse, error)
	CreateSmsTemplateWithContext(ctx context.Context, request *dysmsapi.CreateSmsTemplateRequest, runtime *dara.RuntimeOptions) (*dysmsapi.CreateSmsTemplateResponse, error)
	GetSmsTemplateWithContext(ctx context.Context, request *dysmsapi.GetSmsTemplateRequest, runtime *dara.RuntimeOptions) (*dysmsapi.GetSmsTemplateResponse, error)
	DeleteSmsTemplateWithContext(ctx context.Context, request *dysmsapi.DeleteSmsTemplateRequest, runtime *dara.RuntimeOptions) (*dysmsapi.DeleteSmsTemplateResponse, error)
}

// sender is this module's product: it answers both carrier seams over one client.
type sender struct {
	client carrier
	// signName is the approved signature every message is sent under, and the
	// signature a template is offered under.
	signName string
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
// logging capability is taken up as an optional requirement, because the
// access-key secret is registered as a literal redaction rule and an assembly
// with no logging has nothing to register it with.
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
// one before this is reached, and there is nothing left here to refuse.
func stance(_ *moduleConfig) (core.Enablement, error) {
	return core.Enablement{State: core.StateEnabled}, nil
}

// secrets returns the values that have to be masked wherever the process logs
// them: the access-key secret, which signs every call.
func secrets(cfg *moduleConfig) []string {
	return []string{cfg.AccessKeySecret}
}

// open builds the product over a carrier client. Production passes the vendor
// client; a case passes the same client pointed at a loopback endpoint.
func open(_ context.Context, cfg *moduleConfig) (sms.Product, error) {
	conf := cfg.resolve()
	client, err := dysmsapi.NewClient(&openapiutil.Config{
		AccessKeyId:     dara.String(conf.accessKeyID),
		AccessKeySecret: dara.String(conf.accessKeySecret),
		Endpoint:        dara.String(conf.endpoint),
	})
	if err != nil {
		return nil, fmt.Errorf("%s: building the carrier client: %w", ModuleName, err)
	}
	return newSender(conf, client), nil
}

// newSender builds the product over a carrier client.
func newSender(cfg resolved, client carrier) *sender {
	return &sender{
		client:   client,
		signName: cfg.signName,
		dupe:     dedupe.New[sms.Receipt](dedupe.DefaultCapacity),
	}
}

// Name reports the name this carrier answers under, on both seams. It is a
// constant: the assembly names exactly one carrier, and the name identifies it in
// a diagnostic.
func (s *sender) Name() string { return carrierName }

// Model reports what this carrier can carry.
//
// Aliyun sends by the vendor's approved template code and reviews every
// template, so a template is sendable only once the review has come back
// approved. The deployment's one sending identity is the configured signature:
// this process sends under exactly one, so the identity's name and value are
// that signature, and a message cannot pick another — the vendor has no way to
// choose one (Review.Originator is false, so Message.Originator has no effect).
// It declares no attribute keys, no body bound of its own and no asynchronous
// receipt: the synchronous answer is the whole outcome this carrier reports.
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

// deliver builds one SendSms request from a resolved message and calls the
// carrier once.
//
// The template code and the parameters are already decided: which template a
// message goes out under is the registry's, and this carrier only lays the
// parameters out the way its vendor wants them. Aliyun takes them as a JSON
// object keyed by the parameter name, so the declared order does not matter here.
// The call carries the caller's context, so a context that ends cancels the
// in-flight request and is answered with the uncertain sentinel, the same
// direction as a timeout the carrier itself reports. Nothing here is retried: the
// vendor interface is not idempotent, and a second call would deliver the message
// twice and bill it twice.
func (s *sender) deliver(ctx context.Context, r sms.Resolved) (sms.Receipt, error) {
	params, err := json.Marshal(r.Params)
	if err != nil {
		return sms.Receipt{}, fmt.Errorf("%w: the template parameters cannot be encoded as JSON", sms.ErrRejected)
	}
	request := &dysmsapi.SendSmsRequest{
		PhoneNumbers:  dara.String(r.To),
		SignName:      dara.String(s.signName),
		TemplateCode:  dara.String(r.Code),
		TemplateParam: dara.String(string(params)),
	}

	resp, err := s.client.SendSmsWithContext(ctx, request, noRetry())
	return interpreted(answered{resp: resp, err: err})
}

// Offer hands a template to the carrier for review and answers the vendor code it
// filed it under.
//
// Aliyun's approved templates are written with ${name} placeholders, which is the
// form sms.Submission.Content is written in, so the body is passed through
// verbatim; a carrier whose vendor took its placeholders positionally would be
// the one to rewrite it here. The kind is mapped to the vendor's own template
// type, and the signature is this module's configured one. Nothing is retried: a
// call that fell over may still have created the template, so a failed call is
// uncertain and never sent again.
func (s *sender) Offer(ctx context.Context, sub sms.Submission) (string, error) {
	templateType, ok := templateTypes[sub.Kind]
	if !ok {
		return "", fmt.Errorf("%w: the submission names the kind %q, which this carrier has no "+
			"Aliyun template type for", sms.ErrRejected, sub.Kind)
	}
	request := &dysmsapi.CreateSmsTemplateRequest{
		TemplateName:    dara.String(sub.Name),
		TemplateContent: dara.String(sub.Content),
		TemplateType:    dara.Int32(templateType),
		RelatedSignName: dara.String(s.signName),
		Remark:          dara.String(sub.Remark),
	}
	resp, err := s.client.CreateSmsTemplateWithContext(ctx, request, noRetry())
	if err != nil {
		return "", sms.Unsettled(carrierName)
	}
	if resp == nil || resp.Body == nil {
		return "", sms.NoStatus(carrierName)
	}
	if code := value(resp.Body.Code); code != okCode {
		return "", sms.Classify(verifiedSentinels, carrierName, code)
	}
	if code := value(resp.Body.TemplateCode); code != "" {
		return code, nil
	}
	return "", fmt.Errorf("%w: %s created a template and answered no code for it", sms.ErrUncertain, carrierName)
}

// Approval reads the carrier's review verdict for one template code.
//
// Aliyun answers a status code; this maps it into this package's own review
// vocabulary, and a code the mapping does not name is uncertain rather than
// guessed. The refusal reason, when the carrier gave one, is the review's own
// reject text.
func (s *sender) Approval(ctx context.Context, code string) (sms.Approval, error) {
	request := &dysmsapi.GetSmsTemplateRequest{TemplateCode: dara.String(code)}
	resp, err := s.client.GetSmsTemplateWithContext(ctx, request, noRetry())
	if err != nil {
		return sms.Approval{}, sms.Unsettled(carrierName)
	}
	if resp == nil || resp.Body == nil {
		return sms.Approval{}, sms.NoStatus(carrierName)
	}
	if status := value(resp.Body.Code); status != okCode {
		return sms.Approval{}, sms.Classify(verifiedSentinels, carrierName, status)
	}
	status := value(resp.Body.TemplateStatus)
	state, ok := approvalStates[status]
	if !ok {
		return sms.Approval{}, fmt.Errorf("%w: %s answered the review status %q, which this carrier "+
			"does not know", sms.ErrUncertain, carrierName, status)
	}
	return sms.Approval{State: state, Reason: rejectReason(resp.Body)}, nil
}

// Retire removes a template at the carrier, by its code.
//
// Aliyun deletes a recalled, rejected or approved template and refuses one that
// is under review; a refusal is uncertain rather than a definite failure, and the
// call is not retried.
func (s *sender) Retire(ctx context.Context, code string) error {
	request := &dysmsapi.DeleteSmsTemplateRequest{TemplateCode: dara.String(code)}
	resp, err := s.client.DeleteSmsTemplateWithContext(ctx, request, noRetry())
	if err != nil {
		return sms.Unsettled(carrierName)
	}
	if resp == nil || resp.Body == nil {
		return sms.NoStatus(carrierName)
	}
	if status := value(resp.Body.Code); status != okCode {
		return sms.Classify(verifiedSentinels, carrierName, status)
	}
	return nil
}

// noRetry is the runtime options every call is made with: the SDK's own retry
// switched off, because a second call is a second message, or a second template
// nobody approved.
func noRetry() *dara.RuntimeOptions {
	return &dara.RuntimeOptions{Autoretry: dara.Bool(false)}
}

// rejectReason reads the carrier's refusal text from a review, when it gave one.
func rejectReason(body *dysmsapi.GetSmsTemplateResponseBody) string {
	if body.AuditInfo == nil {
		return ""
	}
	return value(body.AuditInfo.RejectInfo)
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
func interpreted(a answered) (sms.Receipt, error) {
	if a.err != nil {
		return sms.Receipt{}, sms.Unsettled(carrierName)
	}
	if a.resp == nil || a.resp.Body == nil {
		return sms.Receipt{}, sms.NoStatus(carrierName)
	}
	code := value(a.resp.Body.Code)
	if code == okCode {
		return sms.Receipt{ProviderID: value(a.resp.Body.BizId)}, nil
	}
	return sms.Receipt{}, sms.Classify(verifiedSentinels, carrierName, code)
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

// templateTypes maps a template's kind to Aliyun's own TemplateType code.
//
// The values are the ones the SDK documents on
// CreateSmsTemplateRequest.TemplateType: 0 is a verification code, 1 a
// notification and 2 a promotional message. The kind's three values are this
// package's vocabulary, and this is where they take the vendor's spelling. A
// kind the map does not name is a caller defect, refused before the carrier is
// reached.
var templateTypes = map[sms.Kind]int32{
	sms.KindVerification: 0,
	sms.KindNotification: 1,
	sms.KindMarketing:    2,
}

// approvalStates maps Aliyun's TemplateStatus code to this package's review
// state.
//
// The mapping is an assumption: the vendor's numbering comes from the SDK's own
// doc comment on GetSmsTemplateResponseBody.TemplateStatus — 0 is under review, 1
// approved, 2 rejected and 10 recalled — but has not been confirmed against live
// credentials. TestTheApprovalStatesFollowTheReviewCode holds it to those
// vectors, and a code verified online is added here and to that case together. A
// code the map does not name is uncertain rather than guessed.
var approvalStates = map[string]sms.ApprovalState{
	"0":  sms.ApprovalPending,
	"1":  sms.ApprovalApproved,
	"2":  sms.ApprovalRejected,
	"10": sms.ApprovalWithdrawn,
}

// value reads a string the carrier may have left absent.
func value(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
