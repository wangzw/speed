package mail

import (
	"fmt"
	netmail "net/mail"
)

// Validate reports the first reason this message cannot be sent, or nil.
//
// It is stateless: it judges the message alone, never the carrier that would send
// it. Whether the recipient is one the carrier accepts is the carrier's own
// check, made against its configured allowed set at Send time.
//
// Every refusal wraps ErrRejected and names the field at fault, never the value
// in it: an address, a subject or a body the caller wrote must not reach an error
// text. The refusal of a control character in a field that becomes a header line
// is the reason the rule exists; headerControl states it.
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

// validateAddrs refuses the named field when any of its addresses is not one
// net/mail reads as a single RFC 5322 address.
//
// The parse failure is dropped rather than wrapped: its text repeats the address
// it refused, which is the one thing a rejection here may not carry.
func validateAddrs(field string, addrs []string) error {
	for _, addr := range addrs {
		if _, err := netmail.ParseAddress(addr); err != nil {
			return fmt.Errorf("%w: %s holds an address that is not a single RFC 5322 address",
				ErrRejected, field)
		}
	}
	return nil
}

// headerControl reports the first character in s that a header-bound value may
// not carry, and whether it found one. It judges every field whose value becomes
// a header line: the subject, and an attachment's Name and ContentType.
//
// A tab is not one of them. It is ordinary whitespace inside a header value.
//
// What the rest would do is not one thing, and that is the reason for refusing
// them here rather than leaving it to the carrier. A library that writes the
// value as it stands lets a carriage return or a line feed write the header
// block; one that rewrites it delivers a value the caller did not write; one
// that encodes it turns the value into something else again. Validate judges the
// message and not the carrier, so it refuses the character instead of modelling
// which library sits behind the seam.
func headerControl(s string) (byte, bool) {
	for i := 0; i < len(s); i++ {
		if b := s[i]; (b < 0x20 && b != '\t') || b == 0x7f {
			return b, true
		}
	}
	return 0, false
}
