package twilio

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	content "github.com/twilio/twilio-go/rest/content/v1"

	"github.com/vislake/speed/pkg/sms"
)

// Offer creates a content template for a submission and answers the content SID
// the vendor filed it under. The body is rewritten from named placeholders into
// double-brace positional ones numbered by the declared parameter order.
func (s *sender) Offer(ctx context.Context, submission sms.Submission) (string, error) {
	language, err := contentLanguage(submission.Locale)
	if err != nil {
		return "", err
	}
	body, err := doubleBrace(submission.Content, submission.Params)
	if err != nil {
		return "", err
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return "", fmt.Errorf("%w: %s: the context ended before the call", sms.ErrUncertain, carrierName)
	}
	request := content.ContentCreateRequest{
		FriendlyName: submission.Name,
		Language:     language,
		Types: &content.Types{
			TwilioText: &content.TwilioText{Body: body},
		},
	}
	created, err := s.content.CreateContent((&content.CreateContentParams{}).SetContentCreateRequest(request))
	if err != nil {
		return "", classify(err)
	}
	if created == nil || created.Sid == nil || *created.Sid == "" {
		return "", fmt.Errorf("%w: %s created a content template and answered no SID for it",
			sms.ErrUncertain, carrierName)
	}
	return *created.Sid, nil
}

// Approval answers ApprovalNotRequired: this carrier files every template without
// a per-template review. It is not ApprovalUnknown.
func (s *sender) Approval(_ context.Context, _ string) (sms.Approval, error) {
	return sms.Approval{State: sms.ApprovalNotRequired}, nil
}

// Retire removes one content template at the vendor. A 404 is success: the
// content is not there either way.
func (s *sender) Retire(ctx context.Context, code string) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("%w: %s: the context ended before the call", sms.ErrUncertain, carrierName)
	}
	if err := s.content.DeleteContent(code); err != nil {
		if alreadyGone(err) {
			return nil
		}
		return classify(err)
	}
	return nil
}

func contentLanguage(locale string) (string, error) {
	primary := locale
	if cut := strings.IndexAny(primary, "-_"); cut >= 0 {
		primary = primary[:cut]
	}
	primary = strings.ToLower(strings.TrimSpace(primary))
	if len(primary) != 2 || primary[0] < 'a' || primary[0] > 'z' || primary[1] < 'a' || primary[1] > 'z' {
		return "", fmt.Errorf("%w: the locale %q has no primary language tag of two letters, and the "+
			"Content API takes only a two-letter language code", sms.ErrInvalidConfig, locale)
	}
	return primary, nil
}

func doubleBrace(body string, params []string) (string, error) {
	slots := make(map[string]int, len(params))
	for i, name := range params {
		if _, seen := slots[name]; seen {
			return "", fmt.Errorf("%w: the submission declares the parameter %q twice", sms.ErrRejected, name)
		}
		slots[name] = i + 1
	}
	var out strings.Builder
	out.Grow(len(body))
	rest := body
	for {
		start := strings.Index(rest, "${")
		if start < 0 {
			out.WriteString(rest)
			return out.String(), nil
		}
		out.WriteString(rest[:start])
		after := rest[start+len("${"):]
		end := strings.Index(after, "}")
		if end < 0 {
			return "", fmt.Errorf("%w: the submission body has an unclosed placeholder %q",
				sms.ErrRejected, rest[start:])
		}
		name := after[:end]
		position, ok := slots[name]
		if !ok {
			return "", fmt.Errorf("%w: the submission body names the parameter %q, which it does not declare",
				sms.ErrRejected, name)
		}
		out.WriteString("{{")
		out.WriteString(strconv.Itoa(position))
		out.WriteString("}}")
		rest = after[end+len("}"):]
	}
}
