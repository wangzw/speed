//go:build deploy

package main

import (
	"github.com/vislake/speed/pkg/authn/emailcode"
	"github.com/vislake/speed/pkg/mail/smtp"
)

// deployModules is what the deployment assembly adds to the serve roster: the
// SMTP mail carrier and the email verification code sender over it. The Fly
// image is built with the deploy tag because these two need a relay the
// baseline does not have. A default build names none of them; see
// deploy_off.go.
var deployModules = []string{
	smtp.ModuleName,
	emailcode.ModuleName,
}
