package tencent

import (
	"context"
	"fmt"
	"regexp"
	"strconv"

	tc "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/sms/v20210111"

	"github.com/vislake/speed/pkg/sms"
)

// The template type this carrier files a submitted template under. The vendor's
// own note on AddSmsTemplateRequest.SmsType names the mapping: 1 marketing, 2
// notification, 3 verification.
const (
	smsTypeMarketing    = 1
	smsTypeNotification = 2
	smsTypeVerification = 3
)

// The review states the vendor reports on a template, from its own note on
// DescribeTemplateListStatus.StatusCode: 0 approved and in force, 1 under review,
// 2 approved but not yet in force, -1 turned down.
const (
	statusApproved           = 0
	statusUnderReview        = 1
	statusApprovedNotInForce = 2
	statusRejected           = -1
)

// Offer hands a template to the vendor for review and answers the vendor's
// number for it.
//
// The submission body is written with the seam's named placeholders and this
// vendor reviews a positional body, so the body is rewritten before it goes out.
// The vendor reviews asynchronously, so the number that comes back is a template
// the review of which is still pending; its state is read later through Approval.
func (s *sender) Offer(ctx context.Context, submission sms.Submission) (string, error) {
	content, err := positional(submission.Content, submission.Params)
	if err != nil {
		return "", err
	}
	smsType, err := smsTypeOf(submission.Kind)
	if err != nil {
		return "", err
	}
	request := tc.NewAddSmsTemplateRequest()
	name := submission.Name
	request.TemplateName = &name
	request.TemplateContent = &content
	request.SmsType = &smsType
	if submission.Remark != "" {
		remark := submission.Remark
		request.Remark = &remark
	}
	response, err := s.client.AddSmsTemplateWithContext(ctx, request)
	if err != nil {
		return "", classify(err)
	}
	if response == nil || response.Response == nil || response.Response.AddTemplateStatus == nil ||
		response.Response.AddTemplateStatus.TemplateId == nil {
		return "", fmt.Errorf("%w: %s took the submission but answered with no template number", sms.ErrUncertain, ModuleName)
	}
	return *response.Response.AddTemplateStatus.TemplateId, nil
}

// Approval reads the vendor's review state for one template number.
//
// The number is the vendor's own, and the registry holds it as a string; a value
// that is not a number is a caller defect that reached no vendor call, so it is
// refused as ErrRejected.
//
// The identifier is looked up one at a time on purpose: the request's
// TemplateIdSet reads as a filter only while it is non-empty, and an empty set
// makes the vendor page through every template it holds instead of answering
// that it holds none. A response that names the identifier nowhere means the
// vendor no longer holds the template, which this seam reports as withdrawn.
func (s *sender) Approval(ctx context.Context, code string) (sms.Approval, error) {
	number, err := parseTemplateNumber(code)
	if err != nil {
		return sms.Approval{}, err
	}
	request := tc.NewDescribeSmsTemplateListRequest()
	request.TemplateIdSet = []*uint64{&number}
	response, err := s.client.DescribeSmsTemplateListWithContext(ctx, request)
	if err != nil {
		return sms.Approval{}, classify(err)
	}
	if response == nil || response.Response == nil {
		return sms.Approval{}, sms.NoStatus(ModuleName)
	}
	for _, status := range response.Response.DescribeTemplateStatusSet {
		if status != nil && status.TemplateId != nil && *status.TemplateId == number {
			return approvalOf(status)
		}
	}
	return sms.Approval{State: sms.ApprovalWithdrawn}, nil
}

// Retire removes one template at the vendor.
func (s *sender) Retire(ctx context.Context, code string) error {
	number, err := parseTemplateNumber(code)
	if err != nil {
		return err
	}
	request := tc.NewDeleteSmsTemplateRequest()
	request.TemplateId = &number
	if _, err := s.client.DeleteSmsTemplateWithContext(ctx, request); err != nil {
		return classify(err)
	}
	return nil
}

// approvalOf maps one vendor review status to a state of this package's
// vocabulary.
//
// A status of 2 is approved but not yet in force, and the vendor's own note says
// only a status of 0 may be used to send. It is reported as pending, so that a
// send waits for it rather than going out under a template the vendor will not
// accept yet. A status this code does not name is uncertain: the vendor's
// numbering may have grown a value, and guessing its meaning is worse than
// saying so.
func approvalOf(status *tc.DescribeTemplateListStatus) (sms.Approval, error) {
	if status.StatusCode == nil {
		return sms.Approval{}, fmt.Errorf("%w: %s answered with no review status", sms.ErrUncertain, ModuleName)
	}
	switch *status.StatusCode {
	case statusApproved:
		return sms.Approval{State: sms.ApprovalApproved}, nil
	case statusUnderReview, statusApprovedNotInForce:
		return sms.Approval{State: sms.ApprovalPending}, nil
	case statusRejected:
		return sms.Approval{State: sms.ApprovalRejected, Reason: stringValue(status.ReviewReply)}, nil
	default:
		return sms.Approval{}, fmt.Errorf("%w: %s answered the review status %d",
			sms.ErrUncertain, ModuleName, *status.StatusCode)
	}
}

// parseTemplateNumber reads a template number the registry holds as a string.
func parseTemplateNumber(code string) (uint64, error) {
	number, err := strconv.ParseUint(code, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%w: %q is not a template number", sms.ErrRejected, code)
	}
	return number, nil
}

// smsTypeOf maps a template's kind to the vendor's template type.
func smsTypeOf(kind sms.Kind) (uint64, error) {
	switch kind {
	case sms.KindMarketing:
		return smsTypeMarketing, nil
	case sms.KindNotification:
		return smsTypeNotification, nil
	case sms.KindVerification:
		return smsTypeVerification, nil
	default:
		return 0, fmt.Errorf("%w: the submission names the kind %q, which this carrier has no template type for",
			sms.ErrRejected, kind)
	}
}

// namedPlaceholder matches a ${name} placeholder in a submission body.
var namedPlaceholder = regexp.MustCompile(`\$\{([^}]*)\}`)

// positional rewrites a submission body from the seam's named placeholders into
// the positional ones this vendor's reviewed bodies use: each ${name} becomes
// {1}, {2}, … numbered by the order of the declared parameter names.
//
// A parameter declared twice is refused, because one ${name} would then stand for
// two slots and there is no way to tell which the author meant. A placeholder
// naming a parameter the submission does not declare is refused, because the
// vendor would be handed a slot whose meaning was never stated. A declared
// parameter the body never uses is allowed: it means only that the reviewed body
// has fewer slots than the submission lists, and nothing here can tell whether
// that is the author's intent.
func positional(content string, params []string) (string, error) {
	slot := make(map[string]int, len(params))
	for i, name := range params {
		if _, seen := slot[name]; seen {
			return "", fmt.Errorf("%w: the submission declares the parameter %q twice", sms.ErrRejected, name)
		}
		slot[name] = i + 1
	}
	var undeclared string
	missing := false
	rewritten := namedPlaceholder.ReplaceAllStringFunc(content, func(placeholder string) string {
		name := placeholder[2 : len(placeholder)-1]
		position, ok := slot[name]
		if !ok {
			if !missing {
				undeclared = name
				missing = true
			}
			return placeholder
		}
		return "{" + strconv.Itoa(position) + "}"
	})
	if missing {
		return "", fmt.Errorf("%w: the submission body names the parameter %q, which it does not declare",
			sms.ErrRejected, undeclared)
	}
	return rewritten, nil
}

// stringValue reads a string the vendor may have left absent.
func stringValue(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
