//go:build review

package main

// This file is the review assembly of the demo program: a build made with
// -tags review that names the identity providers the baseline leaves out and
// gives the SMS login method a sender that writes the verification code to the
// log instead of calling a carrier.
//
// The assembly also carries a guard: a run whose HTTP listener is not loopback
// fails at startup, before a socket is bound. The guard limits the listening
// address only — a log line can still leave the machine through log collection
// or export — and what it keeps off the network is the seeded administrator
// password the repository publishes and the open signup.
//
// The baseline admits only capabilities that start without a credential, an
// external account or a service outside the deployment stack (see main.go). A
// review of the login surface needs the other kinds on screen, so this build
// adds them with values that start and a sender that reaches no network. None
// of it is part of the baseline: the four social methods and the SMS family are
// imported here and nowhere else, the review modules are a second roster the
// serve command names beside the baseline, and a default build compiles neither.
// The configuration the run reads is a copy outside testdata, too —
// server/review/sqlite.yaml — so the six baselines under testdata stay exactly
// as they are.
//
// The sender is a carrier module built through the same sms.NewModule a real
// carrier uses: it delivers the Sender and Provider seams exclusively, so the
// SMS module and its template registry resolve it like any other carrier. The
// verification code never reaches it through a hardcoded value — the SMS module
// generates it and hands it over as the message's "code" parameter — and the
// sender's whole work is writing that line to the process log and answering
// success. Every number still goes through the real budgets, the real challenge
// row and the real verification, so the flow exercises the product's own state
// machine end to end.
//
// The mail side is the same shape on the other capability: mail.review is a
// carrier built through mail.NewModule, it reaches no relay, and it writes each
// message authn.emailcode rendered to the process log. That is what makes
// name-and-password registration walkable here: the password-signup switch is
// open in review/sqlite.yaml, the account adds its address after signing in, and
// the reviewer reads the code out of the log. The baseline has neither module
// and keeps the switch closed.

import (
	"context"
	"fmt"
	"net"
	"slices"
	"strings"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/mail"
	"github.com/vislake/speed/pkg/sms"

	"github.com/vislake/speed/pkg/authn/emailcode"
	githublogin "github.com/vislake/speed/pkg/authn/login/github"
	googlelogin "github.com/vislake/speed/pkg/authn/login/google"
	smslogin "github.com/vislake/speed/pkg/authn/login/sms"
	wechatmp "github.com/vislake/speed/pkg/authn/login/wechatmp"
	wechatweb "github.com/vislake/speed/pkg/authn/login/wechatweb"
	authnsms "github.com/vislake/speed/pkg/authn/sms"
	smsregistry "github.com/vislake/speed/pkg/sms/registry"
)

// reviewCarrierModuleName is the module name of the review sender. It is a
// carrier's own name: it appears in the startup diagnostics and in the
// enablement lookup, and it is the namespace the sender reads, which declares
// no item and therefore appears in no configuration file.
const reviewCarrierModuleName = "sms.review"

// reviewMailModuleName is the module name of the review mail carrier. It is a
// carrier's own name under the mail capability's namespace, and the sender it
// builds reaches no relay: every message goes to the process log.
const reviewMailModuleName = "mail.review"

// reviewMailCarrierName is what the mail sender answers Name with.
const reviewMailCarrierName = "review"

// reviewGuardModuleName is the module name of the startup guard. It mounts
// nothing: its one work is refusing a run whose listening addresses are not
// loopback.
const reviewGuardModuleName = "review.guard"

// reviewHTTPConfigNamespace is where the HTTP module's input items are mounted:
// its own fixed namespace, which the guard reads the listener addresses from.
const reviewHTTPConfigNamespace = "http"

// reviewCarrierName is what the sender answers Name with: the carrier this
// sender is, in the sense the real carriers use.
const reviewCarrierName = "review"

// reviewModules is what the review build adds to the serve roster. The four
// social methods deliver a LoginMethod each and read their credentials from the
// review configuration; the SMS three are the code state machine, its template
// registry and the login method over them, with the review sender supplying the
// carrier; the guard refuses a listening address reachable beyond the machine,
// where the seeded administrator password and the open signup would be public.
var reviewModules = []string{
	googlelogin.ModuleName,
	githublogin.ModuleName,
	wechatmp.ModuleName,
	wechatweb.ModuleName,
	authnsms.ModuleName,
	smsregistry.ModuleName,
	smslogin.ModuleName,
	reviewCarrierModuleName,
	// Name-and-password registration needs an email channel: authn.emailcode
	// renders the code into the authn.email.verify messages and hands them to a
	// mail carrier, and mail.review is that carrier, writing each message to the
	// process log instead of reaching a relay.
	emailcode.ModuleName,
	reviewMailModuleName,
	reviewGuardModuleName,
}

// reviewSenderConfig is the review sender's configuration. It declares no item:
// the sender needs no credential and no endpoint, and every message goes to the
// process log.
type reviewSenderConfig struct{}

// reviewMailConfig is the review mail carrier's configuration. It declares no
// item for the same reason: the carrier reaches no relay.
type reviewMailConfig struct{}

// init registers the two review carriers — sms.review for SMS and mail.review
// for mail — and the review guard beside them. Importing this file is what puts
// all three in the available set, and the serve roster above names them for a
// run.
func init() {
	core.ProcessRegistry.Register(sms.NewModule(sms.Spec[reviewSenderConfig]{
		ModuleName:      reviewCarrierModuleName,
		ConfigNamespace: reviewCarrierModuleName,
		Config:          &reviewSenderConfig{},
		Stance: func(*reviewSenderConfig) (core.Enablement, error) {
			return core.Enablement{State: core.StateEnabled}, nil
		},
		Open: func(context.Context, *reviewSenderConfig) (sms.Product, error) {
			return reviewSender{}, nil
		},
	}))
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
	core.ProcessRegistry.Register(reviewGuardModule())
}

// reviewGuardModule refuses a run whose listening addresses are not loopback.
//
// The review build seeds the administrator credential the repository publishes
// and opens signup, so a process anyone on the network can reach hands both to
// anyone. The guard reads the HTTP module's listener addresses in Prepare,
// before any socket is bound: a non-loopback address fails the startup, and the
// run never serves. It limits the listener only: a verification code written to
// the process log can still leave the machine through log collection or export.
func reviewGuardModule() core.Module {
	return core.Module{
		Name:     reviewGuardModuleName,
		Requires: []core.Requirement{{Token: (*config.Reader)(nil)}},
		Prepare: func(_ context.Context, reg *core.Registry) (core.Enablement, error) {
			reader, err := core.Resolve[config.Reader](reg)
			if err != nil {
				return core.Enablement{}, fmt.Errorf("%s: taking up the configuration reader: %w",
					reviewGuardModuleName, err)
			}
			if err := checkReviewListeners(reader); err != nil {
				return core.Enablement{}, err
			}
			return core.Enablement{State: core.StateEnabled}, nil
		},
	}
}

// reviewHTTPConfig is the part of the HTTP module's configuration the guard
// reads: the address of each configured listener. The listener entries carry
// more, which is the HTTP module's business.
type reviewHTTPConfig struct {
	Listeners map[string]reviewListenerConfig `config:"listeners"`
}

// reviewListenerConfig is one listener entry's address.
type reviewListenerConfig struct {
	Address string `config:"address"`
}

// checkReviewListeners refuses any listener the review build configured on a
// non-loopback address.
func checkReviewListeners(reader config.Reader) error {
	var cfg reviewHTTPConfig
	if err := reader.Decode(reviewHTTPConfigNamespace, &cfg); err != nil {
		return fmt.Errorf("%s: reading the listener addresses: %w", reviewGuardModuleName, err)
	}
	// The names are visited in order so that a run reporting a bad address
	// reports the same one every time, and so the verdict does not depend on
	// map iteration order.
	names := make([]string, 0, len(cfg.Listeners))
	for name := range cfg.Listeners {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		listener := cfg.Listeners[name]
		if loopbackAddress(listener.Address) {
			continue
		}
		return fmt.Errorf("%s: listener %q is bound to %q, which is not a loopback address. "+
			"The review build seeds a published administrator credential and opens signup, "+
			"so it serves loopback addresses only",
			reviewGuardModuleName, name, listener.Address)
	}
	return nil
}

// loopbackAddress reports whether address, in net.Listen's form, names a
// loopback host. An empty host is every interface and is refused; "localhost"
// is accepted as the conventional loopback name.
func loopbackAddress(address string) bool {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// reviewSender is the carrier the review build sends through. It reaches no
// vendor: Send writes the message it was handed to the process log and answers
// success, and the template-management calls answer as a vendor with no
// per-template review would.
type reviewSender struct{}

// reviewSender must deliver both seams the carrier module provides.
var _ sms.Product = reviewSender{}

// Name is the carrier's own name.
func (reviewSender) Name() string { return reviewCarrierName }

// Model declares what this carrier can carry. It writes whatever the registry
// resolved to the process log, so it accepts both a vendor code and a rendered
// body; it holds no template at a vendor and therefore reviews none per
// template, which makes its Approval answer NotRequired. It names no identity:
// every message goes to the process log, so there is nothing to choose between.
func (reviewSender) Model() sms.CarrierModel {
	return sms.CarrierModel{
		Content: sms.ContentSupport{TemplateCode: true, TextBody: true},
	}
}

// Send writes the resolved message to the process log. The code is the SMS
// module's own, handed over as the message's declared parameter; this line is
// where a reviewer reads it, and no value here is chosen by this file. The
// attribute is spelled verification_code rather than code because the gateway
// registers "code" as a sensitive key name for the process — an authorization
// code travels under it — and the logging layer would mask this value.
func (reviewSender) Send(_ context.Context, r sms.Resolved) (sms.Receipt, error) {
	log.Default().Info("review sms: a verification code was sent",
		"sender", reviewCarrierName,
		"to", r.To,
		"template", r.Template,
		"locale", r.Locale,
		"verification_code", r.Params["code"])
	return sms.Receipt{ProviderID: reviewCarrierName + ":" + r.Template + ":" + r.DedupeID}, nil
}

// Offer answers with a template code derived from the submission's name. The
// review configuration declares its templates in configuration, which this
// process holds as admitted without a vendor review, so nothing calls this in the
// assembly; it exists because a carrier delivers the whole Provider seam.
func (reviewSender) Offer(_ context.Context, s sms.Submission) (string, error) {
	return reviewCarrierName + "-" + strings.ReplaceAll(s.Name, " ", "-"), nil
}

// Approval answers every template as not required: this carrier holds nothing at
// a vendor, so there is no review to wait on — which is the state a carrier with
// Review.PerTemplate false reports, and the one a message may be sent under.
func (reviewSender) Approval(context.Context, string) (sms.Approval, error) {
	return sms.Approval{State: sms.ApprovalNotRequired}, nil
}

// Retire accepts the removal of a template, which this process never holds at a
// vendor.
func (reviewSender) Retire(context.Context, string) error { return nil }

// reviewMailSender is the mail carrier the review build sends through. It
// reaches no relay: Send writes the message it was handed to the process log and
// answers success.
//
// Writing the body is this carrier's whole purpose and the one place the mail
// capability's "the body reaches no log" promise is deliberately suspended: the
// review build exists so a person can read the verification code out of the
// server output. The code is also reported under verification_code, because the
// gateway registers "code" as a sensitive key name for the process and the
// logging layer would mask a value logged under it. The carrier listens on
// loopback only, through the review guard, and the log can still leave the
// machine through collection or export.
type reviewMailSender struct{}

var _ mail.Sender = reviewMailSender{}

// Name is the carrier's own name.
func (reviewMailSender) Name() string { return reviewMailCarrierName }

// Send writes the rendered message to the process log. No value here is chosen
// by this file: the code was drawn by the login module and rendered by
// authn.emailcode.
func (reviewMailSender) Send(_ context.Context, m mail.Message) (mail.Receipt, error) {
	log.Default().Info("review mail: a verification message was sent",
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
