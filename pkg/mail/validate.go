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
// text. The control-byte refusals are the reason the rule exists: a carriage
// return or a line feed in a value that becomes a header line lets the rest of
// the value write the header block, and any other control character leaves a
// line no parser reads.
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
		if bad, control := controlByte(att.Name); control {
			return fmt.Errorf("%w: attachment %d holds a Name with the control byte %#02x",
				ErrRejected, i, bad)
		}
		if bad, control := controlByte(att.ContentType); control {
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

// controlByte reports the first control character in s, and whether it found
// one. It judges the fields whose value becomes a header line as the caller
// wrote it — an attachment's Name and ContentType. A carriage return or a line
// feed there ends the line and lets the rest of the value write the header
// block; any other control character, NUL and DEL included, leaves a line a
// parser refuses to read, which truncates the message at that part. A subject
// and an address are not judged by this: the carrier's library encodes the first
// into an RFC 2047 word and net/mail reads the second, so a control character in
// either produces a message that still parses.
func controlByte(s string) (byte, bool) {
	for i := 0; i < len(s); i++ {
		if b := s[i]; b < 0x20 || b == 0x7f {
			return b, true
		}
	}
	return 0, false
}
