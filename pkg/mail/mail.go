// Package mail sends one already-rendered message through a carrier.
//
// It is the sending capability on its own: a Message carries the addresses, the
// subject and the two alternative bodies the caller has already rendered, and a
// Sender hands that message to one carrier. It holds no template and no
// rendering — the caller renders — and no queue, no budget and no retry; a
// consumer that needs those builds them over this package.
//
// A carrier is a module: an implementation subpackage hands a Spec to NewModule,
// and a host enables at most one of them for this package's Sender.
package mail

import (
	"context"
	"errors"
	"io"
)

// Message is one rendered message handed to a carrier.
//
// The body it carries and any carrier credential reach no log, no error text and
// no metric. This package names the fields; keeping that promise is each
// carrier's.
type Message struct {
	// From is the sender. It is optional: an empty From means the carrier's own
	// configured default sender.
	From string
	// To is the recipient list, each entry in RFC 5322 form: a bare addr-spec, or
	// "Display Name <addr>".
	To []string
	// Cc is the carbon-copy list, in the same form as To.
	Cc []string
	// Bcc is the blind-copy list, in the same form as To. Its entries are envelope
	// recipients only: a carrier never writes them into a header.
	Bcc []string
	// ReplyTo is the reply-to list, in the same form as To.
	ReplyTo []string
	// Subject is the header subject. It may be empty.
	Subject string
	// Text is the plain-text body. At least one of Text and HTML has to be
	// non-empty.
	Text string
	// HTML is the HTML body. At least one of Text and HTML has to be non-empty.
	HTML string
	// Attachments are the files the message carries.
	Attachments []Attachment
	// DedupeID is a stable identifier; a carrier sends at most one message per
	// identifier. An empty identifier has nothing to guard by and the message is
	// delivered directly.
	DedupeID string
}

// Attachment is one file a message carries.
//
// Data is read to the end during Send, and one Message value can be sent only
// once: a dedupe hit does not re-read the reader, so a caller that retries with a
// fresh DedupeID has to rebuild the reader, or it sends an empty attachment with
// no error to show for it.
type Attachment struct {
	// Name is the file name the recipient sees. It has to be non-empty and hold no
	// control character: it is written into the attachment's own header block, so
	// a carriage return or a line feed there would write the block and any other
	// control character would leave a line no parser reads.
	Name string
	// ContentType is the media type. An empty ContentType means
	// application/octet-stream. It is written into the attachment's own header
	// block as it stands, so it too has to hold no control character.
	ContentType string
	// Data is the file content. It has to be non-nil, and Send reads it to the
	// end.
	Data io.Reader
}

// Receipt is what a carrier answers with once it has taken a message.
type Receipt struct {
	// ProviderID is the carrier's own identifier for the send, when the carrier
	// gave one.
	ProviderID string
}

// Sender is the sending seam a carrier module delivers.
//
// A carrier module delivers it exclusively, so a command names one carrier and
// two at once fail in resolution. A carrier answers its own name, calls one
// vendor and never retries inside the call, and classifies what comes back into
// ErrThrottled, ErrRejected or ErrUncertain: an error that is neither of the
// first two, a timeout and a network error are all ErrUncertain.
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
	// ErrRejected reports a message a carrier refused. It is also the sentinel for
	// a caller defect this package catches before the carrier is reached — no
	// recipient, both bodies empty, a subject or an attachment name that can write
	// a header — because neither reached the carrier and neither is uncertain.
	ErrRejected = errors.New("mail: the carrier rejected the message")
	// ErrUncertain reports a send whose delivery is not known, because the call
	// failed in a way that leaves it open whether the message went out.
	ErrUncertain = errors.New("mail: whether the message was delivered is not known")
	// ErrInvalidConfig reports a configuration a carrier cannot run on.
	ErrInvalidConfig = errors.New("mail: the configuration holds a value it cannot run on")
)
