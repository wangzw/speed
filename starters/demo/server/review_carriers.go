package main

// The log carriers: the SMS and mail carriers the local walkthrough reads its
// verification codes from.
//
// They are compiled into every build, the Fly image included, and named in
// capabilityModules beside the carriers that reach a vendor. A log carrier
// reaches no network: it writes the message it was handed to the process log
// and answers success. Which carrier a run uses is a configuration decision
// made in Prepare, not a build-tag decision; the two carriers of one capability
// are exclusive at run time and a run that enables two fails to start.
//
// The SMS carrier is built through the same sms.NewModule a vendor carrier uses:
// it delivers the Sender and Provider seams exclusively, so the SMS module and
// its template registry resolve it like any other carrier. The verification code
// never reaches it through a hardcoded value — the SMS module generates it and
// hands it over as the message's "code" parameter — and the carrier's whole work
// is writing that line to the process log and answering success. Every number
// still goes through the real budgets, the real challenge row and the real
// verification, so the flow exercises the product's own state machine end to
// end.
//
// Unlike a carrier that reaches no vendor, this one answers a real per-template
// review: its Model declares Review.PerTemplate, every language it does not know
// about answers pending, and Review moves one code's verdict. A runtime template
// therefore starts pending in each language it was registered in and settles
// language by language. Review is an input the review assembly drives — see
// review_sms_control.go — and that control exists only in the review build; the
// carrier itself is present in every build.
//
// The mail side is the same shape on the other capability: mail.review is a
// carrier built through mail.NewModule, it reaches no relay, and it writes each
// message authn.emailcode rendered to the process log. That is what makes
// name-and-password registration walkable locally: the account adds its address
// after signing in and the reviewer reads the code out of the log.

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/mail"
	"github.com/vislake/speed/pkg/sms"
)

// reviewCarrierModuleName is the module name of the log SMS carrier. It is a
// carrier's own name: it appears in the startup diagnostics and in the
// enablement lookup, and it is the namespace the carrier reads, which declares
// no item and therefore appears in no configuration file.
const reviewCarrierModuleName = "sms.review"

// reviewMailModuleName is the module name of the log mail carrier. It is a
// carrier's own name under the mail capability's namespace, and the sender it
// builds reaches no relay: every message goes to the process log.
const reviewMailModuleName = "mail.review"

// reviewMailCarrierName is what the mail sender answers Name with.
const reviewMailCarrierName = "review"

// reviewCarrierName is what the SMS sender answers Name with: the carrier this
// sender is, in the sense the real carriers use.
const reviewCarrierName = "review"

// reviewSenderConfig is the log SMS carrier's configuration. It declares no
// item: the carrier needs no credential and no endpoint, and every message goes
// to the process log.
type reviewSenderConfig struct{}

// reviewMailConfig is the log mail carrier's configuration. It declares no item
// for the same reason: the carrier reaches no relay.
type reviewMailConfig struct{}

// init registers the two log carriers. Importing this file is what puts them in
// the available set, and capabilityModules names them for a run. The review
// build's controls are registered separately; see review.go.
func init() {
	core.ProcessRegistry.Register(reviewSMSModule(newReviewCarrier()))
	core.ProcessRegistry.Register(mail.NewModule(mail.Spec[reviewMailConfig]{
		ModuleName:      reviewMailModuleName,
		ConfigNamespace: reviewMailModuleName,
		Config:          &reviewMailConfig{},
		Stance: func(*reviewMailConfig) (core.Enablement, error) {
			return core.Enablement{State: core.StateEnabled}, nil
		},
		Open: func(context.Context, *reviewMailConfig) (mail.Sender, error) {
			return reviewMailSender{}, nil
		},
	}))
}

// reviewSMSModule builds the log SMS carrier's descriptor around one product.
// The product is passed in so a case can hold the same value the module opens
// and drive its review control; a run passes the carrier this file constructs.
func reviewSMSModule(product sms.Product) core.Module {
	return sms.NewModule(sms.Spec[reviewSenderConfig]{
		ModuleName:      reviewCarrierModuleName,
		ConfigNamespace: reviewCarrierModuleName,
		Config:          &reviewSenderConfig{},
		Stance: func(*reviewSenderConfig) (core.Enablement, error) {
			return core.Enablement{State: core.StateEnabled}, nil
		},
		Open: func(context.Context, *reviewSenderConfig) (sms.Product, error) {
			return product, nil
		},
	})
}

// reviewSender is the carrier a run uses when it selects the log SMS carrier.
// It reaches no vendor: Send writes the message it was handed to the process log
// and answers success.
//
// It does review every template and every language. The carrier keeps a verdict
// per vendor code in memory, answers pending for a code it has not been told
// about, and Review sets one code's verdict. A code is the vendor's per-language
// identifier, so moving one code's verdict leaves every other language where it
// was. The store is an input the review assembly drives, while the carrier
// itself is present in every build; a real carrier reads the same seam from its
// vendor's own review.
type reviewSender struct {
	mu       sync.Mutex
	verdicts map[string]sms.Approval
}

// newReviewCarrier builds the carrier with an empty verdict store.
func newReviewCarrier() *reviewSender {
	return &reviewSender{verdicts: make(map[string]sms.Approval)}
}

// reviewSender must deliver both seams the carrier module provides.
var _ sms.Product = (*reviewSender)(nil)

// Name is the carrier's own name.
func (*reviewSender) Name() string { return reviewCarrierName }

// Model declares what this carrier can carry. It writes whatever the registry
// resolved to the process log, so it accepts both a vendor code and a rendered
// body. It reviews every template: a code starts pending and Review moves it,
// which is the state the template registry reads back per language. It names no
// identity: every message goes to the process log, so there is nothing to choose
// between.
func (*reviewSender) Model() sms.CarrierModel {
	return sms.CarrierModel{
		Content: sms.ContentSupport{TemplateCode: true, TextBody: true},
		Review:  sms.ReviewSupport{PerTemplate: true},
	}
}

// Send writes the resolved message to the process log. The code is the SMS
// module's own, handed over as the message's declared parameter; this line is
// where a reviewer reads it, and no value here is chosen by this file. The
// attribute is spelled verification_code rather than code because the gateway
// registers "code" as a sensitive key name for the process — an authorization
// code travels under it — and the logging layer would mask this value.
//
// The line names the message's kind. A verification message carries the code
// the recipient proves and keeps the verification_code field; a notice carries
// no code, so its line says a message was sent and omits that field rather than
// claiming a verification that never happened.
func (*reviewSender) Send(_ context.Context, r sms.Resolved) (sms.Receipt, error) {
	msg, attrs := reviewSMSRecord(r)
	log.Default().Info(msg, attrs...)
	return sms.Receipt{ProviderID: reviewCarrierName + ":" + r.Template + ":" + r.DedupeID}, nil
}

// reviewSMSRecord builds the log record one resolved message produces: the
// message text and its attributes. The kind is the template's, set by the
// registry from the declaration, so a configured authn code template and a
// runtime notice template are told apart even when both arrive through this
// same carrier.
func reviewSMSRecord(r sms.Resolved) (string, []any) {
	attrs := []any{
		"sender", reviewCarrierName,
		"to", r.To,
		"template", r.Template,
		"locale", r.Locale,
	}
	if r.Kind != sms.KindVerification {
		// The kind is what the vendor would be told; a carrier that writes to
		// the log instead must not report a code a non-verification message
		// never carried.
		return "review sms: a message was sent", attrs
	}
	return "review sms: a verification code was sent",
		append(attrs, "verification_code", r.Params["code"])
}

// Offer answers with a template code derived from the submission's name and
// records that code as pending: a template just offered to the vendor has not
// been reviewed yet.
func (s *reviewSender) Offer(_ context.Context, sub sms.Submission) (string, error) {
	code := reviewCarrierName + "-" + strings.ReplaceAll(sub.Name, " ", "-")
	s.mu.Lock()
	if s.verdicts == nil {
		s.verdicts = make(map[string]sms.Approval)
	}
	s.verdicts[code] = sms.Approval{State: sms.ApprovalPending}
	s.mu.Unlock()
	return code, nil
}

// Approval answers one code's review. A code the carrier has not been told about
// is pending: the vendor has taken the template and has not answered yet, which
// is the state a language starts in when a runtime template is registered.
func (s *reviewSender) Approval(_ context.Context, code string) (sms.Approval, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if approval, held := s.verdicts[code]; held {
		return approval, nil
	}
	return sms.Approval{State: sms.ApprovalPending}, nil
}

// Review moves one vendor code's verdict, leaving every other code — and
// therefore every other language — untouched. It is the review assembly's own
// control over the carrier; the control route that calls it exists only in the
// review build. The states it accepts are the per-template review states;
// Unknown and NotRequired describe carriers whose review this process does not
// track or that have none.
func (s *reviewSender) Review(code string, state sms.ApprovalState, reason string) error {
	if code == "" {
		return fmt.Errorf("log carrier: a review names no vendor code")
	}
	switch state {
	case sms.ApprovalPending, sms.ApprovalApproved, sms.ApprovalRejected, sms.ApprovalWithdrawn:
	default:
		return fmt.Errorf("log carrier: %q is not a per-template review state", state)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.verdicts == nil {
		s.verdicts = make(map[string]sms.Approval)
	}
	s.verdicts[code] = sms.Approval{State: state, Reason: reason}
	return nil
}

// Retire forgets one code's verdict, so a code offered again starts pending.
func (s *reviewSender) Retire(_ context.Context, code string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.verdicts, code)
	return nil
}

// reviewMailSender is the mail carrier a run uses when it selects the log mail
// carrier. It reaches no relay: Send writes the message it was handed to the
// process log and answers success.
//
// Writing the body is this carrier's whole purpose and the one place the mail
// capability's "the body reaches no log" promise is deliberately suspended: the
// log carrier exists so a person can read the verification code out of the
// process output. The code is also reported under verification_code, because the
// gateway registers "code" as a sensitive key name for the process and the
// logging layer would mask a value logged under it. A run that selects this
// carrier emits its own warning at startup; the log can still leave the machine
// through collection or export.
type reviewMailSender struct{}

var _ mail.Sender = reviewMailSender{}

// Name is the carrier's own name.
func (reviewMailSender) Name() string { return reviewMailCarrierName }

// Send writes the rendered message to the process log. No value here is chosen
// by this file: the code was drawn by the login module and rendered by
// authn.emailcode.
func (reviewMailSender) Send(_ context.Context, m mail.Message) (mail.Receipt, error) {
	log.Default().Info("log mail: a verification message was sent",
		"sender", reviewMailCarrierName,
		"to", strings.Join(m.To, ", "),
		"subject", m.Subject,
		"text", m.Text,
		"html", m.HTML,
		"verification_code", verificationCode(m.Text, m.HTML))
	return mail.Receipt{ProviderID: reviewMailCarrierName}, nil
}

// verificationCode reads the six-digit code out of the rendered bodies, the
// plain text first. It reports an empty string when no body carries one; a
// longer run of digits is not a code and is not reported.
func verificationCode(bodies ...string) string {
	for _, body := range bodies {
		if code := sixDigitRun(body); code != "" {
			return code
		}
	}
	return ""
}

// sixDigitRun returns the first run of exactly six digits in text, or "".
func sixDigitRun(text string) string {
	run, start := 0, 0
	for i := 0; i < len(text); i++ {
		if text[i] >= '0' && text[i] <= '9' {
			if run == 0 {
				start = i
			}
			run++
			continue
		}
		if run == 6 {
			return text[start : start+6]
		}
		run = 0
	}
	if run == 6 {
		return text[start : start+6]
	}
	return ""
}
