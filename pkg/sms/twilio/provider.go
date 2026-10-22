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
// the vendor filed it under.
//
// The submission body is written with the seam's named placeholders and the
// Content API takes positional ones, so the body is rewritten into double-brace
// placeholders numbered by the order of the declared parameter names. The vendor
// reviews no per-template content resource for this account, so the created
// template is sendable at once and Approval answers ApprovalNotRequired. Nothing
// is retried: a call that fell over may still have created the content, so a
// failed call is uncertain and never sent again.
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

// Approval answers for one template code. This carrier files every template
// without a per-template review, so there is nothing to wait on and no vendor
// call to make: the state is ApprovalNotRequired, which is not ApprovalUnknown —
// that one says a review exists and this process does not track it.
func (s *sender) Approval(_ context.Context, _ string) (sms.Approval, error) {
	return sms.Approval{State: sms.ApprovalNotRequired}, nil
}

// Retire removes one content template at the vendor.
//
// A content the vendor no longer holds is a retire that already happened, so a
// 404 is success. Any other failure is classified like a send: a call that fell
// over may still have removed the content, and it is not retried.
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

// contentLanguage derives the language a content resource is filed under: the
// primary tag of the locale, lower-cased, which must be two lowercase ASCII
// letters.
//
// The Content API accepts only a two-letter ISO 639-1 code, while this package's
// locale is opaque: zh-CN becomes zh and en-US becomes en. A locale whose primary
// tag is not two letters — "fil", or nothing at all — cannot be expressed, and a
// submission that carries one is refused before the vendor is reached.
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

// doubleBrace rewrites a submission body from the seam's named placeholders into
// the positional ones the Content API's twilio/text template takes: each ${name}
// becomes {{1}}, {{2}}, … numbered by the order of the declared parameter names.
//
// A parameter declared twice is refused, because one ${name} would then stand for
// two slots and there is no way to tell which the author meant. A placeholder
// naming a parameter the submission does not declare is refused, because the
// vendor would be handed a slot whose meaning was never stated, and an opening
// ${ with no closing brace is refused because the body cannot be read. A declared
// parameter the body never uses is allowed: the reviewed template then has fewer
// slots than the submission lists, and nothing here can tell whether that is the
// author's intent.
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
