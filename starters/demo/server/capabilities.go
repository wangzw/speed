package main

// This file is the compile-time ceiling of the demo program's capability
// set. A platform capability is imported in the default build, with no build
// tag: the import is what puts a module in the available set. Naming a module
// in capabilityModules is what lets a run consider it directly; a module this
// list does not name can still arrive through the dependency closure of one
// that it does. sms.budget is that case: the delivery pipeline requires its
// store, and the import chain registers it.
//
// Compiling a capability in is not starting it. A module in this list is in the
// participating set of every serve run; whether it actually runs is the
// module's own judgement in Prepare, read from its configuration (see
// docs/adr/adr-module-enablement.md). That split is the whole point: the same
// binary assembles different feature sets on different deployments, and a
// capability that is absent from a run is a run-time fact reported by the
// startup diagnostics, not a different binary.
//
// The two things that stay out of this file are the review-only test controls
// and the loopback guard. They are not product capabilities and must not exist
// in a deployment or baseline binary; they live behind the review tag in
// review.go, review_sms_control.go and notification_control_review.go. See
// docs/adr/adr-starters-skeleton.md, "The review build's control routes".
//
// Several carriers of one capability may be compiled in at once: the SMTP mail
// carrier and the log mail carrier, for instance. They are exclusive at run
// time, and which one runs is a configuration decision made in Prepare; the
// build itself no longer chooses between them.

import (
	"github.com/vislake/speed/pkg/authn/emailcode"
	"github.com/vislake/speed/pkg/authn/invitemail"
	githublogin "github.com/vislake/speed/pkg/authn/login/github"
	googlelogin "github.com/vislake/speed/pkg/authn/login/google"
	smslogin "github.com/vislake/speed/pkg/authn/login/sms"
	wechatmp "github.com/vislake/speed/pkg/authn/login/wechatmp"
	wechatweb "github.com/vislake/speed/pkg/authn/login/wechatweb"
	mfasmssms "github.com/vislake/speed/pkg/authn/mfa/sms"
	authnsms "github.com/vislake/speed/pkg/authn/sms"
	ssooidc "github.com/vislake/speed/pkg/authn/sso/oidc"
	maillog "github.com/vislake/speed/pkg/mail/log"
	"github.com/vislake/speed/pkg/mail/smtp"
	smsadmin "github.com/vislake/speed/pkg/sms/admin"
	smslog "github.com/vislake/speed/pkg/sms/log"
	smsregistry "github.com/vislake/speed/pkg/sms/registry"
)

// capabilityModules is the compile-time ceiling of the capability set: every
// module a serve run may consider, compiled into every build of this program.
//
// The list holds the capabilities the platform baseline leaves out because they
// need a credential, an external account or a service outside the deployment
// stack — the social login methods, the enterprise OIDC method, the SMS family
// with its template management page and second factor, the mail carriers and
// the email verification code sender — together with the log carriers the
// local walkthrough uses and the notification fixture. It is the same list in
// the default build, the Fly image and the review build; the difference between
// those runs is configuration and the review-only controls, never this roster.
//
// A module here is considered by a run, not necessarily started by it. Each
// module states in Prepare whether its configuration lets it run; a module whose
// optional configuration is absent stands itself down with a reason, and one
// whose required configuration is absent fails the startup. The import alone
// puts a module in the available set, which is also the scope of the merged
// contract document — an import for the contract is no longer the same question
// as naming a module for a run, because everything imported here is named.
var capabilityModules = []string{
	// Social login methods: each delivers a LoginMethod and reads its OAuth
	// application's identifier and secret from its own configuration.
	googlelogin.ModuleName,
	githublogin.ModuleName,
	wechatmp.ModuleName,
	wechatweb.ModuleName,

	// The enterprise OIDC login method: an enterprise provider's
	// authorization-code flow as a first factor. The providers themselves are
	// data in the authn.sso register; this method is the code that consumes
	// them.
	ssooidc.ModuleName,

	// The SMS family: the code state machine, its template registry, the login
	// method over them, the second factor, and the template management page.
	authnsms.ModuleName,
	smsregistry.ModuleName,
	smslogin.ModuleName,
	mfasmssms.ModuleName,
	smsadmin.ModuleName,

	// The mail capability's carriers and the notices over them. mail.smtp
	// reaches a relay; mail.log writes each message to the process log and is
	// the fallback when no other carrier is configured. Both are compiled in,
	// and the run selects one through mail.carrier. authn.emailcode renders a
	// verification code, and authn.invitemail an invitation notice, into the
	// carrier's message.
	smtp.ModuleName,
	maillog.ModuleName,
	emailcode.ModuleName,
	invitemail.ModuleName,

	// The SMS log carrier. It is built through the same sms.NewModule a vendor
	// carrier uses, writes each resolved message to the process log, and is the
	// fallback when no vendor carrier is configured.
	smslog.ModuleName,

	// The notification fixture: one notification type, the worker that
	// dispatches it to a tenant's members, and the preference-recipient wiring.
	// The fixture's review-only dispatch control is compiled separately under
	// the review tag; see notification_control_review.go.
	reviewNotificationModuleName,
}
