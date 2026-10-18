//go:build review

package main

// This file is the review assembly of the demo program: a build made with
// -tags review that names the identity providers the baseline leaves out and
// gives the SMS login method a sender that writes the verification code to the
// log instead of calling a carrier.
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

import (
	"context"
	"strings"

	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/sms"

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

// reviewCarrierName is what the sender answers Name with: the carrier this
// sender is, in the sense the real carriers use.
const reviewCarrierName = "review"

// reviewModules is what the review build adds to the serve roster. The four
// social methods deliver a LoginMethod each and read their credentials from the
// review configuration; the SMS three are the code state machine, its template
// registry and the login method over them, with the review sender supplying the
// carrier.
var reviewModules = []string{
	googlelogin.ModuleName,
	githublogin.ModuleName,
	wechatmp.ModuleName,
	wechatweb.ModuleName,
	authnsms.ModuleName,
	smsregistry.ModuleName,
	smslogin.ModuleName,
	reviewCarrierModuleName,
}

// reviewSenderConfig is the review sender's configuration. It declares no item:
// the sender needs no credential and no endpoint, and every message goes to the
// process log.
type reviewSenderConfig struct{}

// init registers the review sender as a carrier module. Importing this file is
// what puts it in the available set, and the serve roster below names it for a
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
}

// reviewSender is the carrier the review build sends through. It reaches no
// vendor: Send writes the message it was handed to the process log and answers
// success, and the template-management calls answer as a vendor whose templates
// are all approved would.
type reviewSender struct{}

// reviewSender must deliver both seams the carrier module provides.
var _ sms.Product = reviewSender{}

// Name is the carrier's own name.
func (reviewSender) Name() string { return reviewCarrierName }

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
// process holds as already approved, so nothing calls this in the assembly; it
// exists because a carrier delivers the whole Provider seam.
func (reviewSender) Offer(_ context.Context, s sms.Submission) (string, error) {
	return reviewCarrierName + "-" + strings.ReplaceAll(s.Name, " ", "-"), nil
}

// Approval answers every template as approved, the state a locally held set
// would be in.
func (reviewSender) Approval(context.Context, string) (sms.Approval, error) {
	return sms.Approval{State: sms.ApprovalApproved}, nil
}

// Retire accepts the removal of a template, which this process never holds at a
// vendor.
func (reviewSender) Retire(context.Context, string) error { return nil }
