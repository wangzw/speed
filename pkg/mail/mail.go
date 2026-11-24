// Package mail sends one already-rendered message through a carrier. It holds
// no template, rendering, queue or retry; a carrier is a module that hands a
// Spec to NewModule.
package mail

import (
	"context"
	"errors"
	"io"
)

// Message is one rendered message handed to a carrier. The body and any carrier
// credential reach no log, error text or metric.
type Message struct {
	// From is the sender. An empty From means the carrier's configured default
	// sender.
	From string
	// To is the recipient list, each entry in RFC 5322 form.
	To []string
	// Cc is the carbon-copy list, in the same form as To.
	Cc []string
	// Bcc is the blind-copy list, in the same form as To. Its entries are envelope
	// recipients only: a carrier never writes them into a header.
	Bcc []string
	// ReplyTo is the reply-to list, in the same form as To.
	ReplyTo []string
	// Subject is the header subject. It may be empty and carries no control
	// character beyond a tab.
	Subject string
	// Text is the plain-text body. At least one of Text and HTML has to be
	// non-empty.
	Text string
	// HTML is the HTML body. At least one of Text and HTML has to be non-empty.
	HTML string
	// Attachments are the files the message carries.
	Attachments []Attachment
	// DedupeID is a stable identifier; a carrier sends at most one message per
	// identifier. An empty identifier delivers the message directly.
	DedupeID string
}

// Attachment is one file a message carries. Data is read to the end during Send,
// so one Message value can be sent only once.
type Attachment struct {
	// Name is the file name the recipient sees. It has to be non-empty and carries
	// no control character beyond a tab.
	Name string
	// ContentType is the media type; empty means application/octet-stream. It
	// carries no control character beyond a tab.
	ContentType string
	// Data is the file content. It has to be non-nil, and Send reads it to the
	// end.
	Data io.Reader
}

// Receipt is what a carrier answers with once it has taken a message.
type Receipt struct {
	// ProviderID is the carrier's own identifier for the send, when it gave one.
	ProviderID string
}

// Sender is the sending seam a carrier module delivers exclusively. A carrier
// answers its own name, calls one vendor and never retries inside the call.
type Sender interface {
	// Name reports the carrier this sender is.
	Name() string
	// Send hands one message to the carrier, at most once per DedupeID. An error
	// is one of ErrThrottled, ErrRejected or ErrUncertain, wrapped so the detail
	// is readable and errors.Is still finds the sentinel.
	Send(ctx context.Context, m Message) (Receipt, error)
}

var (
	// ErrThrottled reports a carrier that limited the call.
	ErrThrottled = errors.New("mail: the carrier limited the call")
	// ErrRejected reports a message a carrier refused, including a caller defect
	// this package catches before the carrier is reached.
	ErrRejected = errors.New("mail: the carrier rejected the message")
	// ErrUncertain reports a send whose delivery is not known.
	ErrUncertain = errors.New("mail: whether the message was delivered is not known")
	// ErrInvalidConfig reports a configuration a carrier cannot run on.
	ErrInvalidConfig = errors.New("mail: the configuration holds a value it cannot run on")
)
