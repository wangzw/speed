package smtp

import (
	"fmt"
	netmail "net/mail"
	"strings"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/mail"
)

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

const (
	tlsStartTLS = "starttls"
	tlsImplicit = "implicit"
	tlsNone     = "none"
)

const defaultPort = 587

var configDefaults = moduleConfig{
	Port: defaultPort,
	TLS:  tlsStartTLS,
}

type moduleConfig struct {
	Host          string   `config:"host"`
	Port          int      `config:"port"`
	TLS           string   `config:"tls"`
	TLSSkipVerify bool     `config:"tls-skip-verify"`
	HELOName      string   `config:"helo-name"`
	Username      string   `config:"username"`
	Password      string   `config:"password"`
	From          string   `config:"from"`
	FromAllowed   []string `config:"from-allowed"`
}

type resolved struct {
	host          string
	port          int
	tls           string
	tlsSkipVerify bool
	heloName      string
	username      string
	password      string
	from          string
	allowed       map[string]bool
}

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
			Origins:     config.OriginPrimary,
			Description: "additional senders a message may name beyond the default, in RFC 5322 form",
		},
	}
}
