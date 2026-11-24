// Package aliyun delivers the SMS carrier seams over Aliyun's Dysmsapi.
// It binds the vendor SDK and registers one carrier module.
package aliyun

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

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

const carrierName = "aliyun"

// Every value but OK, the empty one included, is a refusal.
const okCode = "OK"

type carrier interface {
	SendSmsWithContext(ctx context.Context, request *dysmsapi.SendSmsRequest, runtime *dara.RuntimeOptions) (*dysmsapi.SendSmsResponse, error)
	CreateSmsTemplateWithContext(ctx context.Context, request *dysmsapi.CreateSmsTemplateRequest, runtime *dara.RuntimeOptions) (*dysmsapi.CreateSmsTemplateResponse, error)
	GetSmsTemplateWithContext(ctx context.Context, request *dysmsapi.GetSmsTemplateRequest, runtime *dara.RuntimeOptions) (*dysmsapi.GetSmsTemplateResponse, error)
	DeleteSmsTemplateWithContext(ctx context.Context, request *dysmsapi.DeleteSmsTemplateRequest, runtime *dara.RuntimeOptions) (*dysmsapi.DeleteSmsTemplateResponse, error)
}

type sender struct {
	client   carrier
	signName string
	dupe     *dedupe.Table[sms.Receipt]
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
	if cfg.AccessKeyID == "" && cfg.AccessKeySecret == "" && cfg.SignName == "" {
		return core.Enablement{
			State:  core.StateDisabled,
			Reason: ModuleName + " has no configuration: no access key and no approved signature",
		}, nil
	}
	var missing []string
	if cfg.AccessKeyID == "" {
		missing = append(missing, accessKeyIDItem)
	}
	if cfg.AccessKeySecret == "" {
		missing = append(missing, accessKeyValueItem)
	}
	if cfg.SignName == "" {
		missing = append(missing, signNameItem)
	}
	if len(missing) > 0 {
		return core.Enablement{}, fmt.Errorf("%w: %s is partially configured; %s missing",
			sms.ErrInvalidConfig, ConfigNamespace, strings.Join(missing, ", "))
	}
	return core.Enablement{State: core.StateEnabled}, nil
}

func secrets(cfg *moduleConfig) []string {
	return []string{cfg.AccessKeySecret}
}

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

func newSender(cfg resolved, client carrier) *sender {
	return &sender{
		client:   client,
		signName: cfg.signName,
		dupe:     dedupe.New[sms.Receipt](dedupe.DefaultCapacity),
	}
}

// Name reports the name this carrier answers under, on both seams.
func (s *sender) Name() string { return carrierName }

// Model reports what this carrier can carry: it sends by the vendor's approved
// template code, reviews every template, and declares the configured signature
// as its one identity.
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
	if r.ValidFor != 0 {
		return sms.Receipt{}, fmt.Errorf("%w: %s has no lifetime for a queued message, and this message "+
			"asks for one", sms.ErrRejected, ModuleName)
	}
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

// Offer hands a template to the carrier for review and answers the vendor code
// it filed it under; the body is passed through verbatim because Aliyun's
// approved templates use the same ${name} placeholders.
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

// Approval reads the carrier's review verdict for one template code. A code the
// mapping does not name is uncertain rather than guessed.
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

// Retire removes a template at the carrier, by its code. A refusal is uncertain
// rather than a definite failure.
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

func noRetry() *dara.RuntimeOptions {
	return &dara.RuntimeOptions{Autoretry: dara.Bool(false)}
}

func rejectReason(body *dysmsapi.GetSmsTemplateResponseBody) string {
	if body.AuditInfo == nil {
		return ""
	}
	return value(body.AuditInfo.RejectInfo)
}

type answered struct {
	resp *dysmsapi.SendSmsResponse
	err  error
}

// The vendor error is not wrapped because its text carries the request, and the request carries the message code.
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
// It is empty: no Aliyun status code is known to mean throttled or rejected, so
// every code falls to the uncertain sentinel.
var verifiedSentinels = map[string]error{}

var templateTypes = map[sms.Kind]int32{
	sms.KindVerification: 0,
	sms.KindNotification: 1,
	sms.KindMarketing:    2,
}

// The numbering is the SDK's own and unverified against live credentials; a
// code verified online is added here and to the test that holds this table.
var approvalStates = map[string]sms.ApprovalState{
	"0":  sms.ApprovalPending,
	"1":  sms.ApprovalApproved,
	"2":  sms.ApprovalRejected,
	"10": sms.ApprovalWithdrawn,
}

func value(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
