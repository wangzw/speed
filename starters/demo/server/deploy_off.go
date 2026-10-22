//go:build !deploy

package main

// deployModules is empty in a default build. The SMTP carrier and the email
// verification code sender are compiled only under the deploy build tag, so
// the baseline binary names neither and keeps registration closed unless a
// host opens it with its own carrier. See deploy.go.
var deployModules []string
