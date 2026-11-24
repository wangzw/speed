package main

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
	ssosaml "github.com/vislake/speed/pkg/authn/sso/saml"
	maillog "github.com/vislake/speed/pkg/mail/log"
	"github.com/vislake/speed/pkg/mail/smtp"
	smsadmin "github.com/vislake/speed/pkg/sms/admin"
	smslog "github.com/vislake/speed/pkg/sms/log"
	smsregistry "github.com/vislake/speed/pkg/sms/registry"
)

var capabilityModules = []string{
	googlelogin.ModuleName,
	githublogin.ModuleName,
	wechatmp.ModuleName,
	wechatweb.ModuleName,

	ssooidc.ModuleName,

	ssosaml.ModuleName,

	authnsms.ModuleName,
	smsregistry.ModuleName,
	smslogin.ModuleName,
	mfasmssms.ModuleName,
	smsadmin.ModuleName,

	smtp.ModuleName,
	maillog.ModuleName,
	emailcode.ModuleName,
	invitemail.ModuleName,

	smslog.ModuleName,

	demoNotificationModuleName,
}
