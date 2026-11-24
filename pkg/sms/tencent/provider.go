package tencent

import (
	"context"
	"fmt"
	"regexp"
	"strconv"

	tc "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/sms/v20210111"

	"github.com/vislake/speed/pkg/sms"
)

const (
	smsTypeMarketing    = 1
	smsTypeNotification = 2
	smsTypeVerification = 3
)

const (
	statusApproved           = 0
	statusUnderReview        = 1
	statusApprovedNotInForce = 2
	statusRejected           = -1
)

// Offer hands a template to the vendor for review and answers the vendor's number
// for it. The body is rewritten from named placeholders to the positional ones
// this vendor reviews.
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

// Approval reads the vendor's review state for one template number. The identifier
// is looked up one at a time because an empty TemplateIdSet pages through every
// template; a response naming the number nowhere means withdrawn.
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

func parseTemplateNumber(code string) (uint64, error) {
	number, err := strconv.ParseUint(code, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%w: %q is not a template number", sms.ErrRejected, code)
	}
	return number, nil
}

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

var namedPlaceholder = regexp.MustCompile(`\$\{([^}]*)\}`)

// positional rewrites a submission body from ${name} placeholders to the
// positional {1}, {2}, … the vendor reviews, numbered by the declared parameter
// order.
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

func stringValue(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
