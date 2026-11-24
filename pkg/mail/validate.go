package mail

import (
	"fmt"
	netmail "net/mail"
)

// Validate reports the first reason this message cannot be sent, or nil. Every
// refusal wraps ErrRejected and names the field at fault, never the value in it.
func (m Message) Validate() error {
	if len(m.To)+len(m.Cc)+len(m.Bcc) == 0 {
		return fmt.Errorf("%w: no recipient: To, Cc and Bcc are all empty", ErrRejected)
	}
	var from []string
	if m.From != "" {
		from = []string{m.From}
	}
	for _, list := range []struct {
		field string
		addrs []string
	}{
		{"From", from},
		{"To", m.To},
		{"Cc", m.Cc},
		{"Bcc", m.Bcc},
		{"ReplyTo", m.ReplyTo},
	} {
		if err := validateAddrs(list.field, list.addrs); err != nil {
			return err
		}
	}
	if bad, control := headerControl(m.Subject); control {
		return fmt.Errorf("%w: Subject holds the control byte %#02x", ErrRejected, bad)
	}
	if m.Text == "" && m.HTML == "" {
		return fmt.Errorf("%w: Text and HTML are both empty, and a message needs a body", ErrRejected)
	}
	for i, att := range m.Attachments {
		if att.Name == "" {
			return fmt.Errorf("%w: attachment %d has an empty Name", ErrRejected, i)
		}
		if bad, control := headerControl(att.Name); control {
			return fmt.Errorf("%w: attachment %d holds a Name with the control byte %#02x",
				ErrRejected, i, bad)
		}
		if bad, control := headerControl(att.ContentType); control {
			return fmt.Errorf("%w: attachment %d holds a ContentType with the control byte %#02x",
				ErrRejected, i, bad)
		}
		if att.Data == nil {
			return fmt.Errorf("%w: attachment %d has nil Data", ErrRejected, i)
		}
	}
	return nil
}

func validateAddrs(field string, addrs []string) error {
	for _, addr := range addrs {
		if _, err := netmail.ParseAddress(addr); err != nil {
			return fmt.Errorf("%w: %s holds an address that is not a single RFC 5322 address",
				ErrRejected, field)
		}
	}
	return nil
}

// headerControl reports the first control character a header-bound value may not
// carry; a tab is allowed. It refuses here rather than leaving the carrier's
// library to write, rewrite or encode the value.
func headerControl(s string) (byte, bool) {
	for i := 0; i < len(s); i++ {
		if b := s[i]; (b < 0x20 && b != '\t') || b == 0x7f {
			return b, true
		}
	}
	return 0, false
}
