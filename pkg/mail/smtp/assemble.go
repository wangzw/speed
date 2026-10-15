package smtp

import (
	"fmt"

	gomail "github.com/wneessen/go-mail"

	"github.com/vislake/speed/pkg/mail"
)

// assemble turns a rendered message into the carrier's own message: the header
// block, the one or two alternative bodies and the attachments.
//
// A failure here is a caller defect — an address the carrier's parser refuses
// where the package's own accepted it, a reader that fell over mid-attachment —
// and reaches no carrier, so it is a rejection and not an uncertain case. The
// underlying text is dropped rather than wrapped: it can repeat an address or a
// slice of the message, which is the one thing a rejection may not carry.
func assemble(m mail.Message, from string) (*gomail.Msg, error) {
	msg := gomail.NewMsg()

	if err := msg.From(from); err != nil {
		return nil, rejected("the sender address")
	}
	if len(m.To) > 0 {
		if err := msg.To(m.To...); err != nil {
			return nil, rejected("a recipient address")
		}
	}
	if len(m.Cc) > 0 {
		if err := msg.Cc(m.Cc...); err != nil {
			return nil, rejected("a carbon-copy address")
		}
	}
	if len(m.Bcc) > 0 {
		// Bcc is set as recipients alone: the carrier never writes them into a
		// header, only into RCPT TO.
		if err := msg.Bcc(m.Bcc...); err != nil {
			return nil, rejected("a blind-copy address")
		}
	}
	if len(m.ReplyTo) > 0 {
		if err := msg.SetAddrHeader(gomail.HeaderReplyTo, m.ReplyTo...); err != nil {
			return nil, rejected("a reply-to address")
		}
	}

	msg.Subject(m.Subject)
	setBodies(msg, m)
	if err := attach(msg, m.Attachments); err != nil {
		return nil, err
	}
	return msg, nil
}

// setBodies writes the one or two bodies. With both present the message is a
// multipart/alternative laid out plain-text first, then HTML, which is the order
// a reader that understands only one of them renders: the alternative order is
// most-fallback first.
func setBodies(msg *gomail.Msg, m mail.Message) {
	switch {
	case m.Text != "" && m.HTML != "":
		msg.SetBodyString(gomail.TypeTextPlain, m.Text)
		msg.AddAlternativeString(gomail.TypeTextHTML, m.HTML)
	case m.HTML != "":
		msg.SetBodyString(gomail.TypeTextHTML, m.HTML)
	default:
		msg.SetBodyString(gomail.TypeTextPlain, m.Text)
	}
}

// attach reads each attachment to the end into the message. An empty
// ContentType is left for the library to default to application/octet-stream.
func attach(msg *gomail.Msg, files []mail.Attachment) error {
	for _, file := range files {
		var opts []gomail.FileOption
		if file.ContentType != "" {
			opts = append(opts, gomail.WithFileContentType(gomail.ContentType(file.ContentType)))
		}
		if err := msg.AttachReader(file.Name, file.Data, opts...); err != nil {
			return rejected("an attachment that could not be read")
		}
	}
	return nil
}

// rejected is the caller-defect shape every assembly failure takes.
func rejected(what string) error {
	return fmt.Errorf("%w: %s: %s", mail.ErrRejected, carrierName, what)
}
