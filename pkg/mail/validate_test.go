package mail

import (
	"errors"
	"strings"
	"testing"
)

// TestValidateJudgesEachRuleBothDirections walks the rules one by one: a
// well-formed message and each variation of it that is legal are accepted, and
// each variation that breaks exactly one rule is refused with ErrRejected naming
// the field at fault.
func TestValidateJudgesEachRuleBothDirections(t *testing.T) {
	// edit starts from a message valid on every rule and applies one change, so a
	// case isolates the rule it is about.
	edit := func(mut func(*Message)) Message {
		m := Message{
			To:      []string{"Alice Example <alice@example.com>"},
			Subject: "Your code",
			Text:    "hello",
		}
		mut(&m)
		return m
	}

	cases := []struct {
		name     string
		m        Message
		accepted bool
		mentions string
	}{
		{name: "a display-name recipient", m: edit(func(*Message) {}), accepted: true},

		{name: "no recipients at all", m: edit(func(m *Message) { m.To = nil }), mentions: "To"},
		{
			name: "To empty but Cc set", m: edit(func(m *Message) { m.To = nil; m.Cc = []string{"cc@example.com"} }),
			accepted: true,
		},
		{
			name: "To empty but Bcc set", m: edit(func(m *Message) { m.To = nil; m.Bcc = []string{"bcc@example.com"} }),
			accepted: true,
		},
		{name: "a bare addr-spec", m: edit(func(m *Message) { m.To = []string{"a@b.com"} }), accepted: true},
		{name: "a non-ASCII display name", m: edit(func(m *Message) {
			m.To = []string{"张三 <zhangsan@example.com>"}
		}), accepted: true},
		{
			name: "a malformed recipient", m: edit(func(m *Message) { m.To = []string{"not-an-address"} }),
			mentions: "To",
		},
		{name: "a malformed From", m: edit(func(m *Message) { m.From = "oops" }), mentions: "From"},
		{
			name: "a malformed ReplyTo", m: edit(func(m *Message) { m.ReplyTo = []string{"bad"} }),
			mentions: "ReplyTo",
		},
		{name: "an empty From is legal", m: edit(func(m *Message) { m.From = "" }), accepted: true},

		{name: "an empty subject is legal", m: edit(func(m *Message) { m.Subject = "" }), accepted: true},
		{name: "a subject with a carriage return", m: edit(func(m *Message) {
			m.Subject = "hi\rBcc: x@y.com"
		}), mentions: "Subject"},
		{name: "a subject with a line feed", m: edit(func(m *Message) {
			m.Subject = "hi\nBcc: x@y.com"
		}), mentions: "Subject"},

		{name: "Text set and HTML empty", m: edit(func(*Message) {}), accepted: true},
		{
			name: "HTML set and Text empty", m: edit(func(m *Message) { m.Text = ""; m.HTML = "<p>hi</p>" }),
			accepted: true,
		},
		{name: "both bodies empty", m: edit(func(m *Message) { m.Text = "" }), mentions: "Text"},

		{name: "a well-formed attachment", m: edit(func(m *Message) {
			m.Attachments = []Attachment{{Name: "f.txt", Data: strings.NewReader("x")}}
		}), accepted: true},
		{name: "an attachment with an empty Name", m: edit(func(m *Message) {
			m.Attachments = []Attachment{{Data: strings.NewReader("x")}}
		}), mentions: "Name"},
		{name: "an attachment name with a line feed", m: edit(func(m *Message) {
			m.Attachments = []Attachment{{Name: "f\nBcc: x@y.com", Data: strings.NewReader("x")}}
		}), mentions: "Name"},
		{name: "an attachment name with a carriage return", m: edit(func(m *Message) {
			m.Attachments = []Attachment{{Name: "f\rBcc: x@y.com", Data: strings.NewReader("x")}}
		}), mentions: "Name"},
		{name: "an attachment ContentType with a line feed", m: edit(func(m *Message) {
			m.Attachments = []Attachment{{
				Name: "f.txt", ContentType: "text/plain\nX-Injected: x", Data: strings.NewReader("x"),
			}}
		}), mentions: "ContentType"},
		{name: "an attachment ContentType with a carriage return", m: edit(func(m *Message) {
			m.Attachments = []Attachment{{
				Name: "f.txt", ContentType: "text/plain\rX-Injected: x", Data: strings.NewReader("x"),
			}}
		}), mentions: "ContentType"},
		{name: "an attachment with nil Data", m: edit(func(m *Message) {
			m.Attachments = []Attachment{{Name: "f.txt"}}
		}), mentions: "Data"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.m.Validate()
			if c.accepted {
				if err != nil {
					t.Fatalf("Validate = %v, want the message accepted", err)
				}
				return
			}
			if !errors.Is(err, ErrRejected) {
				t.Fatalf("Validate = %v, want one wrapping ErrRejected", err)
			}
			if !strings.Contains(err.Error(), c.mentions) {
				t.Errorf("the refusal %q does not name the field %q", err.Error(), c.mentions)
			}
		})
	}
}

// TestValidateNamesTheFieldNotTheValue is the security half of the refusals: the
// address and the subject that caused a rejection must not survive into the
// error text, because a value that can reach a header must not be able to write
// one through a log line either.
func TestValidateNamesTheFieldNotTheValue(t *testing.T) {
	const (
		addr    = "no-such-address-sentinel"
		subject = "subject-sentinel"
	)
	for name, m := range map[string]Message{
		"a bad address": {To: []string{addr}, Subject: "ok", Text: "body"},
		"a bad subject": {To: []string{"a@b.com"}, Subject: subject + "\r\n", Text: "body"},
		"a bad attach": {
			To: []string{"a@b.com"}, Subject: "ok", Text: "body",
			Attachments: []Attachment{{Name: subject + "\n", Data: strings.NewReader("x")}},
		},
	} {
		t.Run(name, func(t *testing.T) {
			err := m.Validate()
			if err == nil {
				t.Fatal("Validate accepted the message")
			}
			for _, value := range []string{addr, subject} {
				if strings.Contains(err.Error(), value) {
					t.Errorf("the refusal %q repeats the value %q", err.Error(), value)
				}
			}
		})
	}
}
