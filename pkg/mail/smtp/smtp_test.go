package smtp

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	netmail "net/mail"
	"net/textproto"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/iotest"
	"time"

	gomail "github.com/wneessen/go-mail"

	"github.com/vislake/speed/pkg/mail"
)

// The senders and recipients the cases run with.
const (
	defaultSender    = "sender@example.com"
	otherSender      = "other@example.com"
	testRecipient    = "recipient@example.com"
	testCC           = "cc@example.com"
	testBCC          = "bcc@example.com"
	testReplyTo      = "reply@example.com"
	testBinaryHeader = "binary.bin"
)

// tlsForMode is the connection posture that reaches one session shape.
func tlsForMode(mode sessionMode) string {
	switch mode {
	case modeStartTLS:
		return tlsStartTLS
	case modeImplicit:
		return tlsImplicit
	default:
		return tlsNone
	}
}

// newTestSender builds the module's product against a fake server, running the
// real client construction.
func newTestSender(t *testing.T, srv *fakeServer, mutate func(*moduleConfig)) *sender {
	t.Helper()
	cfg := moduleConfig{
		Host:          srv.host,
		Port:          srv.port,
		TLS:           tlsForMode(srv.mode),
		TLSSkipVerify: srv.mode != modePlain,
		From:          defaultSender,
	}
	if mutate != nil {
		mutate(&cfg)
	}
	delivered, err := open(t.Context(), &cfg)
	if err != nil {
		t.Fatalf("opening the sender: %v", err)
	}
	s, ok := delivered.(*sender)
	if !ok {
		t.Fatalf("open delivered %T, want *sender", delivered)
	}
	return s
}

// textOnly is a message with a plain body alone.
func textOnly() mail.Message {
	return mail.Message{
		To:      []string{testRecipient},
		Subject: "Hello",
		Text:    "hello there",
	}
}

// parseMessage parses a landed message with the standard library, so an
// independent parser and not this package's writer is the judge.
func parseMessage(t *testing.T, raw []byte) *netmail.Message {
	t.Helper()
	m, err := netmail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("parsing the landed message: %v\n%s", err, raw)
	}
	return m
}

// headerish is what both net/mail's header and a MIME part's header offer.
type headerish interface{ Get(key string) string }

// decodedHeader reads a header value and undoes its RFC 2047 encoding.
func decodedHeader(t *testing.T, h headerish, name string) string {
	t.Helper()
	value := h.Get(name)
	if value == "" {
		return ""
	}
	decoded, err := new(mime.WordDecoder).DecodeHeader(value)
	if err != nil {
		t.Fatalf("decoding the %s header %q: %v", name, value, err)
	}
	return decoded
}

// mediaType splits a Content-Type header into its media type and parameters,
// defaulting an absent one to text/plain.
func mediaType(t *testing.T, h headerish) (string, map[string]string) {
	t.Helper()
	raw := h.Get("Content-Type")
	if raw == "" {
		return "text/plain", nil
	}
	mt, params, err := mime.ParseMediaType(raw)
	if err != nil {
		t.Fatalf("parsing the Content-Type %q: %v", raw, err)
	}
	return mt, params
}

// decodeBody reads a part body and undoes its transfer encoding.
func decodeBody(t *testing.T, h headerish, r io.Reader) []byte {
	t.Helper()
	switch strings.ToLower(h.Get("Content-Transfer-Encoding")) {
	case "base64":
		body, err := io.ReadAll(base64.NewDecoder(base64.StdEncoding, r))
		if err != nil {
			t.Fatalf("decoding a base64 body: %v", err)
		}
		return body
	case "quoted-printable":
		body, err := io.ReadAll(quotedprintable.NewReader(r))
		if err != nil {
			t.Fatalf("decoding a quoted-printable body: %v", err)
		}
		return body
	default:
		body, err := io.ReadAll(r)
		if err != nil {
			t.Fatalf("reading a body: %v", err)
		}
		return body
	}
}

// lf turns CRLF into LF, so a body compares the same however the transport
// spelled its line endings.
func lf(b []byte) string { return strings.ReplaceAll(string(b), "\r\n", "\n") }

// sameText compares a landed body with what the caller wrote. The trailing
// newline a body part terminates with is not part of the caller's text, so it is
// trimmed from both sides.
func sameText(got []byte, want string) bool {
	return strings.TrimRight(lf(got), "\n") == strings.TrimRight(want, "\n")
}

// part is one MIME part read into memory.
type part struct {
	header textproto.MIMEHeader
	body   []byte
}

// readParts reads every part of a multipart body into memory, so cases can walk
// them without the reader losing an unread body under them.
func readParts(t *testing.T, mr *multipart.Reader) []part {
	t.Helper()
	var parts []part
	for {
		p, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			return parts
		}
		if err != nil {
			t.Fatalf("reading a part: %v", err)
		}
		body, err := io.ReadAll(p)
		if err != nil {
			t.Fatalf("reading a part body: %v", err)
		}
		parts = append(parts, part{header: p.Header, body: body})
	}
}

// partsOf opens a multipart body into its parts.
func partsOf(t *testing.T, h headerish, body io.Reader) []part {
	t.Helper()
	mt, params := mediaType(t, h)
	if !strings.HasPrefix(mt, "multipart/") {
		t.Fatalf("the message is %q, want a multipart body", mt)
	}
	return readParts(t, multipart.NewReader(body, params["boundary"]))
}

// decodedPart is a part's body with its transfer encoding undone.
func decodedPart(t *testing.T, p part) []byte {
	t.Helper()
	return decodeBody(t, p.header, bytes.NewReader(p.body))
}

// findPart returns the first part whose media type is want, or a fatal.
func findPart(t *testing.T, parts []part, want string) part {
	t.Helper()
	for _, p := range parts {
		if mt, _ := mediaType(t, p.header); mt == want {
			return p
		}
	}
	t.Fatalf("no %s part among the %d parts", want, len(parts))
	return part{}
}

// TestSendDeliversATextOnlyMessage holds the simplest shape: one plain body, the
// envelope the message named, and the body the caller wrote.
func TestSendDeliversATextOnlyMessage(t *testing.T) {
	srv := newFakeServer(t, modePlain)
	s := newTestSender(t, srv, nil)

	receipt, err := s.Send(t.Context(), textOnly())
	if err != nil {
		t.Fatalf("sending: %v", err)
	}
	if receipt.ProviderID == "" {
		t.Error("the receipt carries no provider identifier")
	}

	sess := srv.lastSession(t)
	if sess.mailFrom != defaultSender {
		t.Errorf("MAIL FROM is %q, want %q", sess.mailFrom, defaultSender)
	}
	if !slices.Equal(sess.rcptTo, []string{testRecipient}) {
		t.Errorf("RCPT TO is %v, want the recipient", sess.rcptTo)
	}

	parsed := parseMessage(t, sess.data)
	if got := decodedHeader(t, parsed.Header, "Subject"); got != "Hello" {
		t.Errorf("Subject is %q, want Hello", got)
	}
	if mt, _ := mediaType(t, parsed.Header); mt != "text/plain" {
		t.Errorf("the body is %q, want text/plain", mt)
	}
	if got := decodeBody(t, parsed.Header, parsed.Body); !sameText(got, "hello there") {
		t.Errorf("the body is %q, want the plain text", got)
	}
}

// TestSendDeliversBothBodiesAsAlternative holds the two-body shape: a
// multipart/alternative with the plain text part first and the HTML second.
func TestSendDeliversBothBodiesAsAlternative(t *testing.T) {
	srv := newFakeServer(t, modePlain)
	s := newTestSender(t, srv, nil)

	m := mail.Message{
		To:      []string{testRecipient},
		Subject: "Both",
		Text:    "plain body",
		HTML:    "<p>html body</p>",
	}
	if _, err := s.Send(t.Context(), m); err != nil {
		t.Fatalf("sending: %v", err)
	}

	parsed := parseMessage(t, srv.lastSession(t).data)
	if mt, _ := mediaType(t, parsed.Header); mt != "multipart/alternative" {
		t.Fatalf("the body is %q, want multipart/alternative", mt)
	}
	parts := partsOf(t, parsed.Header, parsed.Body)
	if len(parts) != 2 {
		t.Fatalf("the alternative holds %d parts, want 2", len(parts))
	}
	if mt, _ := mediaType(t, parts[0].header); mt != "text/plain" {
		t.Errorf("the first part is %q, want the plain text first", mt)
	}
	if got := decodedPart(t, parts[0]); !sameText(got, "plain body") {
		t.Errorf("the plain part is %q, want the plain body", got)
	}
	if mt, _ := mediaType(t, parts[1].header); mt != "text/html" {
		t.Errorf("the second part is %q, want the HTML", mt)
	}
	if got := decodedPart(t, parts[1]); !sameText(got, "<p>html body</p>") {
		t.Errorf("the HTML part is %q, want the HTML body", got)
	}
}

// TestSendDeliversAnHTMLOnlyMessage holds the single-HTML shape: a message with
// no plain body goes out as text/html, not as a plain part carrying markup.
func TestSendDeliversAnHTMLOnlyMessage(t *testing.T) {
	srv := newFakeServer(t, modePlain)
	s := newTestSender(t, srv, nil)

	m := mail.Message{
		To:      []string{testRecipient},
		Subject: "HTML only",
		HTML:    "<p>only html</p>",
	}
	if _, err := s.Send(t.Context(), m); err != nil {
		t.Fatalf("sending: %v", err)
	}

	parsed := parseMessage(t, srv.lastSession(t).data)
	if mt, _ := mediaType(t, parsed.Header); mt != "text/html" {
		t.Errorf("the body is %q, want text/html", mt)
	}
	if got := decodeBody(t, parsed.Header, parsed.Body); !sameText(got, "<p>only html</p>") {
		t.Errorf("the body is %q, want the HTML body", got)
	}
}

// TestSendDeliversAnAttachmentHoldsTheAttachedShape: a multipart/mixed whose
// first part is the multipart/alternative and whose second is the attachment.
func TestSendDeliversAnAttachment(t *testing.T) {
	srv := newFakeServer(t, modePlain)
	s := newTestSender(t, srv, nil)

	m := mail.Message{
		To:      []string{testRecipient},
		Subject: "With a file",
		Text:    "see attached",
		HTML:    "<p>see attached</p>",
		Attachments: []mail.Attachment{{
			Name:        "report.txt",
			ContentType: "text/plain",
			Data:        strings.NewReader("the file body"),
		}},
	}
	if _, err := s.Send(t.Context(), m); err != nil {
		t.Fatalf("sending: %v", err)
	}

	parsed := parseMessage(t, srv.lastSession(t).data)
	if mt, _ := mediaType(t, parsed.Header); mt != "multipart/mixed" {
		t.Fatalf("the body is %q, want multipart/mixed", mt)
	}
	mixed := partsOf(t, parsed.Header, parsed.Body)

	alternative := findPart(t, mixed, "multipart/alternative")
	alternativeParts := partsOf(t, alternative.header, bytes.NewReader(alternative.body))
	if mt, _ := mediaType(t, alternativeParts[0].header); mt != "text/plain" {
		t.Errorf("the nested alternative's first part is %q, want the plain text first", mt)
	}
	if mt, _ := mediaType(t, alternativeParts[1].header); mt != "text/html" {
		t.Errorf("the nested alternative's second part is %q, want the HTML", mt)
	}

	attachment := attachmentPart(t, mixed, "report.txt")
	if got := decodedPart(t, attachment); !sameText(got, "the file body") {
		t.Errorf("the attachment body is %q, want the file content", got)
	}
}

// attachmentPart finds the part whose Content-Disposition names fileName. The
// disposition is decoded first, because a file name outside ASCII travels as an
// RFC 2047 encoded word that the media-type parser alone does not undo.
func attachmentPart(t *testing.T, parts []part, fileName string) part {
	t.Helper()
	for _, p := range parts {
		_, params, err := mime.ParseMediaType(decodedHeader(t, p.header, "Content-Disposition"))
		if err != nil {
			continue
		}
		if params["filename"] == fileName {
			return p
		}
	}
	t.Fatalf("no attachment named %q among the parts", fileName)
	return part{}
}

// TestANonASCIISubjectRoundTrips holds that a subject outside ASCII is written
// as an RFC 2047 encoded word and reads back unchanged.
func TestANonASCIISubjectRoundTrips(t *testing.T) {
	srv := newFakeServer(t, modePlain)
	s := newTestSender(t, srv, nil)

	const subject = "验证码 · Verifizierung"
	m := textOnly()
	m.Subject = subject
	if _, err := s.Send(t.Context(), m); err != nil {
		t.Fatalf("sending: %v", err)
	}

	parsed := parseMessage(t, srv.lastSession(t).data)
	raw := parsed.Header.Get("Subject")
	if !strings.Contains(raw, "=?") {
		t.Errorf("the subject %q carries no RFC 2047 encoded word", raw)
	}
	if got := decodedHeader(t, parsed.Header, "Subject"); got != subject {
		t.Errorf("the subject reads back %q, want %q", got, subject)
	}
}

// TestANonASCIIAttachmentFilenameRoundTrips holds that an attachment file name
// outside ASCII survives into the part's Content-Disposition.
func TestANonASCIIAttachmentFilenameRoundTrips(t *testing.T) {
	srv := newFakeServer(t, modePlain)
	s := newTestSender(t, srv, nil)

	const fileName = "报告.txt"
	m := textOnly()
	m.Attachments = []mail.Attachment{{Name: fileName, Data: strings.NewReader("body")}}
	if _, err := s.Send(t.Context(), m); err != nil {
		t.Fatalf("sending: %v", err)
	}

	parsed := parseMessage(t, srv.lastSession(t).data)
	parts := partsOf(t, parsed.Header, parsed.Body)
	attachment := attachmentPart(t, parts, fileName)
	if got := decodedPart(t, attachment); !sameText(got, "body") {
		t.Errorf("the attachment body is %q, want the attached content", got)
	}
}

// TestABodyLineBeginningWithADotSurvivesDotStuffing holds the one line the DATA
// framing is most likely to corrupt: the wire carries it with an extra leading
// dot, and the message a reader parses carries the original line.
func TestABodyLineBeginningWithADotSurvivesDotStuffing(t *testing.T) {
	srv := newFakeServer(t, modePlain)
	s := newTestSender(t, srv, nil)

	const body = "first line\n.dotted line\nlast line\n"
	m := textOnly()
	m.Text = body
	if _, err := s.Send(t.Context(), m); err != nil {
		t.Fatalf("sending: %v", err)
	}

	sess := srv.lastSession(t)
	if !strings.Contains(string(sess.wire), "..dotted line") {
		t.Errorf("the wire body does not carry the dot-stuffed line:\n%s", sess.wire)
	}
	parsed := parseMessage(t, sess.data)
	if got := decodeBody(t, parsed.Header, parsed.Body); !sameText(got, body) {
		t.Errorf("the body reads back %q, want %q", got, body)
	}
}

// TestABinaryAttachmentRoundTripsByteForByte holds that an attachment holding a
// NUL and a CRLF pair comes back out of the message with the same bytes.
func TestABinaryAttachmentRoundTripsByteForByte(t *testing.T) {
	srv := newFakeServer(t, modePlain)
	s := newTestSender(t, srv, nil)

	content := []byte{0x00, 0x01, 0x02, '\r', '\n', 0xff, 0x00, 'A', 'B', '\n'}
	m := textOnly()
	m.Attachments = []mail.Attachment{{
		Name:        testBinaryHeader,
		ContentType: "application/octet-stream",
		Data:        bytes.NewReader(content),
	}}
	if _, err := s.Send(t.Context(), m); err != nil {
		t.Fatalf("sending: %v", err)
	}

	parsed := parseMessage(t, srv.lastSession(t).data)
	parts := partsOf(t, parsed.Header, parsed.Body)
	attachment := attachmentPart(t, parts, testBinaryHeader)
	got := decodedPart(t, attachment)
	if !bytes.Equal(got, content) {
		t.Errorf("the attachment is % x, want % x", got, content)
	}
	if mt, _ := mediaType(t, attachment.header); mt != "application/octet-stream" {
		t.Errorf("the attachment media type is %q, want application/octet-stream", mt)
	}
}

// TestCCApearsInHeadersWhileBCCReachesOnlyTheEnvelope holds the blind-copy rule:
// Cc is a header, Bcc is an envelope recipient and never a header.
func TestCCApearsInHeadersWhileBCCReachesOnlyTheEnvelope(t *testing.T) {
	srv := newFakeServer(t, modePlain)
	s := newTestSender(t, srv, nil)

	m := textOnly()
	m.Cc = []string{testCC}
	m.Bcc = []string{testBCC}
	if _, err := s.Send(t.Context(), m); err != nil {
		t.Fatalf("sending: %v", err)
	}

	sess := srv.lastSession(t)
	for _, want := range []string{testRecipient, testCC, testBCC} {
		if !slices.Contains(sess.rcptTo, want) {
			t.Errorf("RCPT TO %v does not hold %q", sess.rcptTo, want)
		}
	}

	parsed := parseMessage(t, sess.data)
	if got := parsed.Header.Get("Cc"); !strings.Contains(got, testCC) {
		t.Errorf("the Cc header is %q, want it to hold %q", got, testCC)
	}
	if got := parsed.Header.Get("Bcc"); got != "" {
		t.Errorf("the message carries a Bcc header %q; a blind copy is envelope-only", got)
	}
	if strings.Contains(string(sess.data), testBCC) {
		t.Errorf("the blind copy reached the message body:\n%s", sess.data)
	}
}

// TestReplyToIsWritten holds that a reply-to list becomes a Reply-To header.
func TestReplyToIsWritten(t *testing.T) {
	srv := newFakeServer(t, modePlain)
	s := newTestSender(t, srv, nil)

	m := textOnly()
	m.ReplyTo = []string{testReplyTo}
	if _, err := s.Send(t.Context(), m); err != nil {
		t.Fatalf("sending: %v", err)
	}

	parsed := parseMessage(t, srv.lastSession(t).data)
	if got := parsed.Header.Get("Reply-To"); !strings.Contains(got, testReplyTo) {
		t.Errorf("the Reply-To header is %q, want it to hold %q", got, testReplyTo)
	}
}

// TestEachSendOpensOneConnection holds that a send is one connection: the client
// never pools or reuses one across calls.
func TestEachSendOpensOneConnection(t *testing.T) {
	srv := newFakeServer(t, modePlain)
	s := newTestSender(t, srv, nil)

	for range 3 {
		if _, err := s.Send(t.Context(), textOnly()); err != nil {
			t.Fatalf("sending: %v", err)
		}
	}
	if got := srv.connectionCount(); got != 3 {
		t.Errorf("three sends opened %d connections, want 3", got)
	}
}

// TestConcurrentSendsShareTheSenderSafely holds the carrier's concurrency
// contract under the race detector: several goroutines may send at once over one
// sender, and racing calls carrying one identifier still reach the relay once.
func TestConcurrentSendsShareTheSenderSafely(t *testing.T) {
	t.Run("distinct identifiers each reach the relay", func(t *testing.T) {
		srv := newFakeServer(t, modePlain)
		s := newTestSender(t, srv, nil)

		ids := []string{"c1", "c2", "c3", "c4", "c5", "c6", "c7", "c8"}
		var wg sync.WaitGroup
		for _, id := range ids {
			wg.Add(1)
			go func() {
				defer wg.Done()
				m := textOnly()
				m.DedupeID = id
				if _, err := s.Send(t.Context(), m); err != nil {
					t.Errorf("sending %s: %v", id, err)
				}
			}()
		}
		wg.Wait()

		if got := srv.connectionCount(); got != len(ids) {
			t.Errorf("%d sends opened %d connections, want %d", len(ids), got, len(ids))
		}
	})

	t.Run("one identifier reaches the relay once", func(t *testing.T) {
		srv := newFakeServer(t, modePlain)
		s := newTestSender(t, srv, nil)

		const racers = 8
		var wg sync.WaitGroup
		for range racers {
			wg.Add(1)
			go func() {
				defer wg.Done()
				m := textOnly()
				m.DedupeID = "one-identifier"
				if _, err := s.Send(t.Context(), m); err != nil {
					t.Errorf("sending: %v", err)
				}
			}()
		}
		wg.Wait()

		if got := srv.connectionCount(); got != 1 {
			t.Errorf("%d racing sends opened %d connections, want 1", racers, got)
		}
	})
}

// TestTheMessageSenderMustBeAmongTheConfiguredOnes holds the allowed-set check:
// a sender the configuration does not hold is refused before the relay is
// reached, and one it holds — the default or an additional allowed sender — is
// not.
func TestTheMessageSenderMustBeAmongTheConfiguredOnes(t *testing.T) {
	srv := newFakeServer(t, modePlain)
	s := newTestSender(t, srv, func(c *moduleConfig) {
		c.FromAllowed = []string{"Alias <" + otherSender + ">"}
	})

	allowed := textOnly()
	allowed.From = otherSender
	if _, err := s.Send(t.Context(), allowed); err != nil {
		t.Fatalf("sending from an allowed sender: %v", err)
	}

	refused := textOnly()
	refused.From = "stranger@example.com"
	if _, err := s.Send(t.Context(), refused); !errors.Is(err, mail.ErrRejected) {
		t.Fatalf("a sender outside the set gave %v, want ErrRejected", err)
	}
	if got := srv.connectionCount(); got != 1 {
		t.Errorf("the refused sender opened a connection; %d were opened, want 1", got)
	}
}

// TestAMessageWithNoRecipientIsRefused holds that a message the package refuses
// never reaches the relay.
func TestAMessageWithNoRecipientIsRefused(t *testing.T) {
	srv := newFakeServer(t, modePlain)
	s := newTestSender(t, srv, nil)

	_, err := s.Send(t.Context(), mail.Message{Subject: "no one", Text: "body"})
	if !errors.Is(err, mail.ErrRejected) {
		t.Fatalf("a message with no recipient gave %v, want ErrRejected", err)
	}
	if got := srv.connectionCount(); got != 0 {
		t.Errorf("a refused message opened %d connections, want 0", got)
	}
}

// TestOneIdentifierReachesTheRelayOnce holds the dedupe guard: a second call for
// one identifier is answered with the first call's outcome and opens no second
// connection.
func TestOneIdentifierReachesTheRelayOnce(t *testing.T) {
	srv := newFakeServer(t, modePlain)
	s := newTestSender(t, srv, nil)

	m := textOnly()
	m.DedupeID = "one-and-only"
	first, firstErr := s.Send(t.Context(), m)
	second, secondErr := s.Send(t.Context(), m)

	if firstErr != nil || secondErr != nil {
		t.Fatalf("the sends failed: %v and %v", firstErr, secondErr)
	}
	if first != second {
		t.Errorf("the second call was answered %+v, want the first call's %+v", second, first)
	}
	if got := srv.connectionCount(); got != 1 {
		t.Errorf("one identifier opened %d connections, want 1", got)
	}
}

// TestARepeatedIdentifierIsAnsweredWithTheFirstFailure holds that the dedupe
// guard carries the outcome and not only the receipt: a second call for an
// identifier whose first call was refused is refused the same way, rather than
// being answered with an empty success.
func TestARepeatedIdentifierIsAnsweredWithTheFirstFailure(t *testing.T) {
	srv := newFakeServer(t, modePlain, func(s *fakeServer) { s.rejectRCPTCode = "550" })
	s := newTestSender(t, srv, nil)

	m := textOnly()
	m.DedupeID = "refused-once"
	_, firstErr := s.Send(t.Context(), m)
	_, secondErr := s.Send(t.Context(), m)

	if !errors.Is(firstErr, mail.ErrRejected) {
		t.Fatalf("the first call gave %v, want ErrRejected", firstErr)
	}
	if !errors.Is(secondErr, mail.ErrRejected) {
		t.Fatalf("the second call gave %v, want the first call's ErrRejected", secondErr)
	}
	if got := srv.connectionCount(); got != 1 {
		t.Errorf("one identifier opened %d connections, want 1", got)
	}
}

// TestAnAttachmentThatCannotBeReadIsRefused holds that a reader falling over
// part way is a caller defect: it is a rejection and reaches no relay.
func TestAnAttachmentThatCannotBeReadIsRefused(t *testing.T) {
	srv := newFakeServer(t, modePlain)
	s := newTestSender(t, srv, nil)

	m := textOnly()
	m.Attachments = []mail.Attachment{{
		Name: "broken.bin",
		Data: iotest.ErrReader(errors.New("the stream fell over")),
	}}
	if _, err := s.Send(t.Context(), m); !errors.Is(err, mail.ErrRejected) {
		t.Fatalf("an unreadable attachment gave %v, want ErrRejected", err)
	}
	if got := srv.connectionCount(); got != 0 {
		t.Errorf("a refused message opened %d connections, want 0", got)
	}
}

// TestTheThreeSessionShapesDeliver holds that each connection posture reaches
// the relay: plaintext with AUTH, STARTTLS with an in-session upgrade, and
// implicit TLS from the first byte.
func TestTheThreeSessionShapesDeliver(t *testing.T) {
	cases := []struct {
		name string
		mode sessionMode
		auth bool
	}{
		{"plaintext with AUTH", modePlain, true},
		{"STARTTLS", modeStartTLS, false},
		{"implicit TLS", modeImplicit, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := newFakeServer(t, c.mode)
			s := newTestSender(t, srv, func(cfg *moduleConfig) {
				if c.auth {
					cfg.Username = "the-user"
					cfg.Password = "the-password"
				}
			})
			if _, err := s.Send(t.Context(), textOnly()); err != nil {
				t.Fatalf("sending: %v", err)
			}
			sess := srv.lastSession(t)
			if c.mode != modePlain && !sess.tls {
				t.Errorf("the session was not encrypted under %s", c.name)
			}
			if c.auth && (sess.authUser != "the-user" || sess.authPass != "the-password") {
				t.Errorf("the relay read the identity %q/%q, want the configured one",
					sess.authUser, sess.authPass)
			}
		})
	}
}

// TestTheFailurePathsAreClassified holds the classification of a landed failure:
// a permanent refusal is a rejection, and a connection lost mid-message, a
// refused dial and a timeout are all uncertain.
func TestTheFailurePathsAreClassified(t *testing.T) {
	t.Run("a 4xx at RCPT is a throttle", func(t *testing.T) {
		srv := newFakeServer(t, modePlain, func(s *fakeServer) { s.rejectRCPTCode = "450" })
		s := newTestSender(t, srv, nil)
		_, err := s.Send(t.Context(), textOnly())
		if !errors.Is(err, mail.ErrThrottled) {
			t.Fatalf("a 450 at RCPT gave %v, want ErrThrottled", err)
		}
	})

	t.Run("a 5xx at RCPT is a rejection", func(t *testing.T) {
		srv := newFakeServer(t, modePlain, func(s *fakeServer) { s.rejectRCPTCode = "550" })
		s := newTestSender(t, srv, nil)
		_, err := s.Send(t.Context(), textOnly())
		if !errors.Is(err, mail.ErrRejected) {
			t.Fatalf("a 550 at RCPT gave %v, want ErrRejected", err)
		}
	})

	t.Run("a 5xx answered after DATA is a rejection", func(t *testing.T) {
		srv := newFakeServer(t, modePlain, func(s *fakeServer) { s.rejectDataCode = "554" })
		s := newTestSender(t, srv, nil)
		_, err := s.Send(t.Context(), textOnly())
		if !errors.Is(err, mail.ErrRejected) {
			t.Fatalf("a 554 after DATA gave %v, want ErrRejected", err)
		}
	})

	t.Run("a 4xx answered after DATA is a throttle", func(t *testing.T) {
		srv := newFakeServer(t, modePlain, func(s *fakeServer) { s.rejectDataCode = "451" })
		s := newTestSender(t, srv, nil)
		_, err := s.Send(t.Context(), textOnly())
		if !errors.Is(err, mail.ErrThrottled) {
			t.Fatalf("a 451 after DATA gave %v, want ErrThrottled", err)
		}
	})

	t.Run("a connection dropped mid-message is uncertain", func(t *testing.T) {
		srv := newFakeServer(t, modePlain, func(s *fakeServer) { s.dropMidData = true })
		s := newTestSender(t, srv, nil)
		_, err := s.Send(t.Context(), textOnly())
		if !errors.Is(err, mail.ErrUncertain) {
			t.Fatalf("a dropped connection gave %v, want ErrUncertain", err)
		}
	})

	t.Run("a refused dial is uncertain", func(t *testing.T) {
		s := newTestSender(t, &fakeServer{host: "127.0.0.1", port: unusedPort(t), mode: modePlain}, nil)
		_, err := s.Send(t.Context(), textOnly())
		if !errors.Is(err, mail.ErrUncertain) {
			t.Fatalf("a refused dial gave %v, want ErrUncertain", err)
		}
	})

	t.Run("an ended context is uncertain", func(t *testing.T) {
		srv := newFakeServer(t, modePlain)
		s := newTestSender(t, srv, nil)
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		_, err := s.Send(ctx, textOnly())
		if !errors.Is(err, mail.ErrUncertain) {
			t.Fatalf("an ended context gave %v, want ErrUncertain", err)
		}
	})

	t.Run("a server that never answers is a timeout", func(t *testing.T) {
		srv := newFakeServer(t, modePlain, func(s *fakeServer) { s.silent = true })
		client, err := gomail.NewClient(srv.host, gomail.WithPort(srv.port),
			gomail.WithTimeout(50*time.Millisecond))
		if err != nil {
			t.Fatalf("building a client with a short timeout: %v", err)
		}
		s := newSender(resolved{from: defaultSender, allowed: map[string]bool{
			defaultSender: true,
		}}, client)
		_, err = s.Send(t.Context(), textOnly())
		if !errors.Is(err, mail.ErrUncertain) {
			t.Fatalf("a timed-out send gave %v, want ErrUncertain", err)
		}
	})
}

// TestTheExportedSurfaceNamesNoCarrierType holds that the vendor library stays
// behind the seam: a host that imports this package to name the carrier sees the
// mail.Sender interface and nothing of the SDK behind it.
func TestTheExportedSurfaceNamesNoCarrierType(t *testing.T) {
	doc := goDoc(t)
	if strings.Contains(doc, "wneessen") {
		t.Errorf("the exported surface names the carrier library's module path:\n%s", doc)
	}
	for _, field := range []string{"*gomail.", "gomail."} {
		if strings.Contains(doc, field) {
			t.Errorf("the exported surface carries a %q-typed field:\n%s", field, doc)
		}
	}
}

// goDoc is this package's exported surface, as go doc renders it.
func goDoc(t *testing.T) string {
	t.Helper()
	cmd := exec.Command("go", "doc", "-all", ".")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go doc: %v\n%s", err, stderr.String())
	}
	if len(out) == 0 {
		t.Fatal("go doc printed nothing")
	}
	return string(out)
}
