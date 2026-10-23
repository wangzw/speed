//go:build deploy

package main

import (
	"github.com/vislake/speed/pkg/authn/emailcode"
	githublogin "github.com/vislake/speed/pkg/authn/login/github"
	googlelogin "github.com/vislake/speed/pkg/authn/login/google"
	"github.com/vislake/speed/pkg/mail/smtp"
)

// deployModules is what the deployment assembly adds to the serve roster: the
// SMTP mail carrier and the email verification code sender over it, and the two
// social login methods the deployment holds OAuth applications for. The Fly
// image is built with the deploy tag because these need a relay and provider
// credentials the baseline does not have; the template renders each client
// identifier and secret from a Fly secret. A default build names none of them;
// see deploy_off.go.
var deployModules = []string{
	smtp.ModuleName,
	emailcode.ModuleName,
	googlelogin.ModuleName,
	githublogin.ModuleName,
}
