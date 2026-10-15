package mail

import (
	"fmt"
	netmail "net/mail"
	"strings"
)

// Validate reports the first reason this message cannot be sent, or nil.
//
// It is stateless: it judges the message alone, never the carrier that would send
// it. Whether the recipient is one the carrier accepts is the carrier's own
// check, made against its configured allowed set at Send time.
//
// Every refusal wraps ErrRejected and names the field at fault, never the value
// in it: an address, a subject or a body the caller wrote must not reach an error
// text. The carriage-return and line-feed refusals are the reason the rule
// exists: a value that can write a line is a value that can write the header
// block.
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
	if strings.ContainsAny(m.Subject, "\r\n") {
		return fmt.Errorf("%w: Subject holds a carriage return or a line feed", ErrRejected)
	}
	if m.Text == "" && m.HTML == "" {
		return fmt.Errorf("%w: Text and HTML are both empty, and a message needs a body", ErrRejected)
	}
	for i, att := range m.Attachments {
		if att.Name == "" {
			return fmt.Errorf("%w: attachment %d has an empty Name", ErrRejected, i)
		}
		if strings.ContainsAny(att.Name, "\r\n") {
			return fmt.Errorf("%w: attachment %d holds a Name with a carriage return or a line feed",
				ErrRejected, i)
		}
		if strings.ContainsAny(att.ContentType, "\r\n") {
			return fmt.Errorf("%w: attachment %d holds a ContentType with a carriage return or a line feed",
				ErrRejected, i)
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
