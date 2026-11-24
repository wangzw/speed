package smtp

import (
	"bufio"
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"io"
	"math/big"
	"net"
	"strings"
	"sync"
	"testing"
	"testing/iotest"
	"time"
)

type sessionMode int

const (
	modePlain sessionMode = iota
	modeStartTLS
	modeImplicit
)

type session struct {
	mailFrom string
	rcptTo   []string
	data     []byte
	wire     []byte
	tls      bool
	authUser string
	authPass string
}

type fakeServer struct {
	host string
	port int
	mode sessionMode

	tlsConfig *tls.Config

	silent         bool
	rejectRCPTCode string
	rejectDataCode string
	dropMidData    bool

	raw net.Listener
	ln  net.Listener

	mu          sync.Mutex
	connections int
	sessions    []session
	auths       []session
}

func newFakeServer(t *testing.T, mode sessionMode, configure ...func(*fakeServer)) *fakeServer {
	t.Helper()
	raw, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listening: %v", err)
	}
	s := &fakeServer{raw: raw, ln: raw, mode: mode}
	addr := raw.Addr().(*net.TCPAddr)
	s.host = "127.0.0.1"
	s.port = addr.Port
	if mode != modePlain {
		cert := selfSignedCert(t)
		s.tlsConfig = &tls.Config{Certificates: []tls.Certificate{*cert}}
		if mode == modeImplicit {
			s.ln = tls.NewListener(raw, s.tlsConfig)
		}
	}
	for _, apply := range configure {
		apply(s)
	}
	go s.serve()
	t.Cleanup(func() { _ = raw.Close() })
	return s
}

func (s *fakeServer) serve() {
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			return
		}
		s.mu.Lock()
		s.connections++
		s.mu.Unlock()
		go s.handle(conn)
	}
}

func (s *fakeServer) handle(conn net.Conn) {
	defer func() { _ = conn.Close() }()

	if s.silent {
		_, _ = io.Copy(io.Discard, conn)
		return
	}

	r := bufio.NewReader(conn)
	w := bufio.NewWriter(conn)
	if !s.reply(w, "220 fake ESMTP ready\r\n") {
		return
	}

	sess := session{tls: s.mode == modeImplicit}
	tlsActive := sess.tls
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		verb := strings.ToUpper(line)
		switch {
		case strings.HasPrefix(verb, "EHLO"):
			s.greet(w, s.mode == modeStartTLS && !tlsActive)
		case strings.HasPrefix(verb, "HELO"):
			s.reply(w, "250 fake\r\n")
		case strings.HasPrefix(verb, "STARTTLS"):
			if s.mode != modeStartTLS || tlsActive {
				s.reply(w, "503 bad sequence\r\n")
				continue
			}
			s.reply(w, "220 go ahead\r\n")
			tlsConn := tls.Server(conn, s.tlsConfig)
			if err := tlsConn.Handshake(); err != nil {
				return
			}
			conn = tlsConn
			r = bufio.NewReader(conn)
			w = bufio.NewWriter(conn)
			tlsActive = true
			sess.tls = true
		case strings.HasPrefix(verb, "AUTH"):
			if !s.auth(r, w, line, &sess) {
				return
			}
		case strings.HasPrefix(verb, "MAIL FROM:"):
			sess.mailFrom = envelopeAddr(line[len("MAIL FROM:"):])
			s.reply(w, "250 sender ok\r\n")
		case strings.HasPrefix(verb, "RCPT TO:"):
			if s.rejectRCPTCode != "" {
				s.reply(w, s.rejectRCPTCode+" recipient refused\r\n")
				continue
			}
			sess.rcptTo = append(sess.rcptTo, envelopeAddr(line[len("RCPT TO:"):]))
			s.reply(w, "250 recipient ok\r\n")
		case strings.HasPrefix(verb, "DATA"):
			if !s.reply(w, "354 end with a line holding one dot\r\n") {
				return
			}
			if s.dropMidData {
				_, _ = io.CopyN(io.Discard, r, 8)
				return
			}
			data, wire, err := readData(r)
			if err != nil {
				return
			}
			sess.data, sess.wire = data, wire
			s.record(sess)
			reply := "250 message accepted\r\n"
			if s.rejectDataCode != "" {
				reply = s.rejectDataCode + " refused after DATA\r\n"
			}
			if !s.reply(w, reply) {
				return
			}
		case strings.HasPrefix(verb, "RSET"):
			s.reply(w, "250 ok\r\n")
		case strings.HasPrefix(verb, "NOOP"):
			s.reply(w, "250 ok\r\n")
		case strings.HasPrefix(verb, "QUIT"):
			s.reply(w, "221 bye\r\n")
			return
		default:
			s.reply(w, "502 command not implemented\r\n")
		}
	}
}

func (s *fakeServer) greet(w *bufio.Writer, advertiseStartTLS bool) {
	w.WriteString("250-fake greets you\r\n")
	w.WriteString("250-PIPELINING\r\n")
	if advertiseStartTLS {
		w.WriteString("250-STARTTLS\r\n")
	}
	w.WriteString("250-AUTH PLAIN\r\n")
	w.WriteString("250-8BITMIME\r\n")
	w.WriteString("250 SMTPUTF8\r\n")
	_ = w.Flush()
}

func (s *fakeServer) auth(r *bufio.Reader, w *bufio.Writer, line string, sess *session) bool {
	fields := strings.Fields(line)
	var encoded string
	if len(fields) >= 3 {
		encoded = fields[2]
	} else {
		if !s.reply(w, "334 \r\n") {
			return false
		}
		continuation, err := r.ReadString('\n')
		if err != nil {
			return false
		}
		encoded = strings.TrimSpace(continuation)
	}
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err == nil {
		if parts := strings.Split(string(raw), "\x00"); len(parts) == 3 {
			sess.authUser, sess.authPass = parts[1], parts[2]
			s.mu.Lock()
			s.auths = append(s.auths, session{authUser: parts[1], authPass: parts[2]})
			s.mu.Unlock()
		}
	}
	return s.reply(w, "235 authentication successful\r\n")
}

func (s *fakeServer) reply(w *bufio.Writer, line string) bool {
	if _, err := w.WriteString(line); err != nil {
		return false
	}
	return w.Flush() == nil
}

func (s *fakeServer) record(sess session) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions = append(s.sessions, sess)
}

func (s *fakeServer) connectionCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.connections
}

func (s *fakeServer) lastSession(t *testing.T) session {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.sessions) == 0 {
		t.Fatal("the fake server recorded no completed transaction")
	}
	return s.sessions[len(s.sessions)-1]
}

func (s *fakeServer) lastAuth(t *testing.T) session {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.auths) == 0 {
		t.Fatal("the fake server accepted no authentication")
	}
	return s.auths[len(s.auths)-1]
}

func readData(r *bufio.Reader) (message, wire []byte, err error) {
	var msg, raw bytes.Buffer
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return nil, nil, err
		}
		if line == ".\r\n" || line == ".\n" {
			return msg.Bytes(), raw.Bytes(), nil
		}
		raw.WriteString(line)
		msg.WriteString(strings.TrimPrefix(line, "."))
	}
}

func envelopeAddr(arg string) string {
	arg = strings.TrimSpace(arg)
	if open := strings.Index(arg, "<"); open >= 0 {
		if close := strings.Index(arg, ">"); close > open {
			return arg[open+1 : close]
		}
	}
	if fields := strings.Fields(arg); len(fields) > 0 {
		return strings.Trim(fields[0], "<>")
	}
	return ""
}

func selfSignedCert(t *testing.T) *tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generating a key: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "127.0.0.1"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
		DNSNames:              []string{"localhost"},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("creating a certificate: %v", err)
	}
	return &tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

// TestReadDataUndoesDotStuffing checks that reading the body undoes dot-stuffing.
func TestReadDataUndoesDotStuffing(t *testing.T) {
	const transmitted = "..leading dot\r\nnormal\r\n.\r\n"
	message, wire, err := readData(bufio.NewReader(strings.NewReader(transmitted)))
	if err != nil {
		t.Fatalf("reading the body: %v", err)
	}
	if got, want := string(message), ".leading dot\r\nnormal\r\n"; got != want {
		t.Errorf("the message is %q, want %q", got, want)
	}
	if got, want := string(wire), "..leading dot\r\nnormal\r\n"; got != want {
		t.Errorf("the wire form is %q, want %q", got, want)
	}
}

// TestReadDataFindsTheTerminatorAcrossReads checks that the terminator is found
// even when the bytes arrive one at a time.
func TestReadDataFindsTheTerminatorAcrossReads(t *testing.T) {
	r := bufio.NewReader(iotest.OneByteReader(strings.NewReader("a line\r\n.\r\n")))
	message, _, err := readData(r)
	if err != nil {
		t.Fatalf("reading a byte-at-a-time body: %v", err)
	}
	if got, want := string(message), "a line\r\n"; got != want {
		t.Errorf("the message is %q, want %q", got, want)
	}
}

func unusedPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listening: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	if err := ln.Close(); err != nil {
		t.Fatalf("closing the probe listener: %v", err)
	}
	return port
}
