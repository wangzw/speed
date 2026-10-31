package smtp

import (
	"fmt"
	netmail "net/mail"
	"strings"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/mail"
)

// The keys this module accepts, relative to its namespace. All of them are
// configuration, read once at startup: the relay, the TLS posture, the
// credential and the senders a message may name are fixed for the life of the
// process.
const (
	hostItem          = "host"
	portItem          = "port"
	tlsItem           = "tls"
	tlsSkipVerifyItem = "tls-skip-verify"
	heloNameItem      = "helo-name"
	usernameItem      = "username"
	passwordItem      = "password"
	fromItem          = "from"
	fromAllowedItem   = "from-allowed"
)

// The three connection postures. starttls is the default; a value outside the
// set is a startup failure.
const (
	// tlsStartTLS connects in the clear and requires the server to advertise
	// STARTTLS. It is not an opportunistic upgrade: a server that does not
	// advertise it fails the send.
	tlsStartTLS = "starttls"
	// tlsImplicit encrypts from the first byte, the SMTPS posture.
	tlsImplicit = "implicit"
	// tlsNone runs the whole session in the clear.
	tlsNone = "none"
)

// defaultPort is the submission port a relay listens on when the host names no
// port. An implicit-TLS relay usually listens on 465 instead, which the host
// sets explicitly.
const defaultPort = 587

// configDefaults is what this module is worth without a configuration file.
// Only the port and the TLS posture have one: there is no default relay, no
// default sender and no default credential, because a message sent through a
// guessed relay or from a guessed sender is a message nobody configured.
var configDefaults = moduleConfig{
	Port: defaultPort,
	TLS:  tlsStartTLS,
}

// moduleConfig is the set of input items this module accepts.
type moduleConfig struct {
	// Host is the relay the messages are handed to.
	Host string `config:"host"`
	// Port is the relay's submission port.
	Port int `config:"port"`
	// TLS is the connection posture: starttls, implicit or none.
	TLS string `config:"tls"`
	// TLSSkipVerify turns certificate verification off, for a private relay
	// presented with a self-signed certificate. It is off by default.
	TLSSkipVerify bool `config:"tls-skip-verify"`
	// HELOName is the name reported in EHLO/HELO. An empty name means the
	// library's own default.
	HELOName string `config:"helo-name"`
	// Username is the AUTH identity. An empty username means no AUTH is
	// attempted.
	Username string `config:"username"`
	// Password is the AUTH secret. It is registered as a literal redaction rule.
	Password string `config:"password"`
	// From is the default sender, in RFC 5322 form. A message that names no
	// sender is sent from it.
	From string `config:"from"`
	// FromAllowed is the set of additional senders a message may name beyond
	// From. Each entry is in RFC 5322 form.
	FromAllowed []string `config:"from-allowed"`
}

// resolved is the configuration this module actually runs on: the connection
// settings, the credential and the frozen set of accepted senders.
type resolved struct {
	host          string
	port          int
	tls           string
	tlsSkipVerify bool
	heloName      string
	username      string
	password      string
	// from is the default sender as it was configured, kept in its RFC 5322
	// form so a display name survives into the header.
	from string
	// allowed holds every accepted sender as a lower-cased addr-spec.
	allowed map[string]bool
}

// resolve turns decoded configuration into what the module runs on, and states
// every value it cannot run with.
//
// Each failure wraps ErrInvalidConfig and names the item at fault rather than
// the value in it: the address parser's own text repeats the address it
// refused, and a configuration failure a startup prints may not carry it.
func (c moduleConfig) resolve() (resolved, error) {
	switch c.TLS {
	case tlsStartTLS, tlsImplicit, tlsNone:
	default:
		return resolved{}, fmt.Errorf("%w: %s.%s: not one of %s, %s or %s",
			mail.ErrInvalidConfig, ConfigNamespace, tlsItem, tlsStartTLS, tlsImplicit, tlsNone)
	}
	if c.Username != "" && c.Password == "" {
		return resolved{}, fmt.Errorf("%w: %s.%s: a username is set with no password",
			mail.ErrInvalidConfig, ConfigNamespace, passwordItem)
	}
	from, err := netmail.ParseAddress(c.From)
	if err != nil {
		return resolved{}, fmt.Errorf("%w: %s.%s: not a single RFC 5322 address",
			mail.ErrInvalidConfig, ConfigNamespace, fromItem)
	}
	allowed := map[string]bool{strings.ToLower(from.Address): true}
	for _, raw := range c.FromAllowed {
		addr, err := netmail.ParseAddress(raw)
		if err != nil {
			return resolved{}, fmt.Errorf("%w: %s.%s: an entry that is not a single RFC 5322 address",
				mail.ErrInvalidConfig, ConfigNamespace, fromAllowedItem)
		}
		allowed[strings.ToLower(addr.Address)] = true
	}
	return resolved{
		host:          c.Host,
		port:          c.Port,
		tls:           c.TLS,
		tlsSkipVerify: c.TLSSkipVerify,
		heloName:      c.HELOName,
		username:      c.Username,
		password:      c.Password,
		from:          c.From,
		allowed:       allowed,
	}, nil
}

// items declares this module's input items. The module descriptor mounts them
// under this module's namespace.
func items() map[string]config.Item {
	return map[string]config.Item{
		hostItem: {
			Description: "the SMTP relay the messages are handed to",
		},
		portItem: {
			Description: "the relay's submission port",
		},
		tlsItem: {
			Description: "the connection posture: starttls (default), implicit or none",
		},
		tlsSkipVerifyItem: {
			Description: "skip TLS certificate verification, for a private relay with a self-signed certificate",
		},
		heloNameItem: {
			Description: "the name reported in EHLO/HELO; empty means the library default",
		},
		usernameItem: {
			Description: "the AUTH identity; empty means no AUTH is attempted",
		},
		passwordItem: {
			Sensitive:   true,
			Description: "the AUTH secret",
		},
		fromItem: {
			Description: "the default sender, in RFC 5322 form",
		},
		fromAllowedItem: {
			// A container leaf: a list takes its origin set from the primary
			// source alone.
			Origins:     config.OriginPrimary,
			Description: "additional senders a message may name beyond the default, in RFC 5322 form",
		},
	}
}
