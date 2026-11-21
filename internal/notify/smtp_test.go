// SPDX-License-Identifier: GPL-3.0-or-later

package notify

import (
	"bufio"
	"crypto/tls"
	"encoding/pem"
	"errors"
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// fakeSMTP is just enough of a mail server: it offers STARTTLS if asked,
// takes AUTH PLAIN and one mail, and records what it saw.
type fakeSMTP struct {
	implicit bool // TLS from the first byte, as on port 465
	startTLS bool
	tls      *tls.Config
	mu       sync.Mutex
	log      []string // commands, in order
	data     string
}

func (f *fakeSMTP) record(s string) { f.mu.Lock(); f.log = append(f.log, s); f.mu.Unlock() }

// session is one connection to the fake server.
type session struct {
	f      *fakeSMTP
	conn   net.Conn
	r      *bufio.Reader
	secure bool
}

func (s *session) say(line string) { _, _ = s.conn.Write([]byte(line + "\r\n")) }

// upgrade switches the connection to TLS.
func (s *session) upgrade() bool {
	tc := tls.Server(s.conn, s.f.tls)
	if tc.Handshake() != nil {
		return false
	}
	s.conn, s.r, s.secure = tc, bufio.NewReader(tc), true
	return true
}

func (f *fakeSMTP) serve(conn net.Conn) {
	s := &session{f: f, conn: conn, r: bufio.NewReader(conn)}
	defer func() { _ = s.conn.Close() }()
	if f.implicit && !s.upgrade() {
		return
	}
	s.say("220 fake ESMTP")
	for {
		line, err := s.r.ReadString('\n')
		if err != nil {
			return
		}
		verb := strings.ToUpper(strings.Fields(line + " x")[0])
		f.record(map[bool]string{true: "tls ", false: ""}[s.secure] + verb)
		if !s.handle(verb) {
			return
		}
	}
}

// handle answers one command; false ends the session.
func (s *session) handle(verb string) bool {
	switch verb {
	case "EHLO":
		if s.f.startTLS && !s.secure {
			s.say("250-fake\r\n250-STARTTLS\r\n250 AUTH PLAIN")
		} else {
			s.say("250-fake\r\n250 AUTH PLAIN")
		}
	case "STARTTLS":
		s.say("220 go ahead")
		return s.upgrade()
	case "AUTH":
		s.say("235 ok")
	case "MAIL", "RCPT":
		s.say("250 ok")
	case "DATA":
		s.say("354 go")
		s.readData()
		s.say("250 queued")
	case "QUIT":
		s.say("221 bye")
		return false
	default:
		s.say("502 no")
	}
	return true
}

// readData keeps the mail up to the line with a single dot.
func (s *session) readData() {
	var b strings.Builder
	for {
		l, err := s.r.ReadString('\n')
		if err != nil || l == ".\r\n" {
			break
		}
		b.WriteString(l)
	}
	s.f.mu.Lock()
	s.f.data = b.String()
	s.f.mu.Unlock()
}

// smtpServer starts a fake server and a channel for it, trusting the
// certificate it uses for STARTTLS.
func smtpServer(t *testing.T, startTLS, implicit bool) (*fakeSMTP, Channel) {
	t.Helper()
	certSrc := httptest.NewTLSServer(nil) // only for its certificate, valid for 127.0.0.1
	t.Cleanup(certSrc.Close)
	ca := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certSrc.Certificate().Raw}), 0o600); err != nil {
		t.Fatal(err)
	}
	f := &fakeSMTP{implicit: implicit, startTLS: startTLS, tls: &tls.Config{Certificates: certSrc.TLS.Certificates, MinVersion: tls.VersionTLS12}}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go f.serve(c)
		}
	}()
	port, _ := strconv.Atoi(strings.TrimPrefix(l.Addr().String(), "127.0.0.1:"))
	return f, Channel{Name: "mail", Type: "smtp", Host: "127.0.0.1", Port: port, CAFile: ca, From: "fstui <fstui@example.org>", To: []string{"ops@example.org"}, User: "u", Secret: "p"}
}

// The session goes to TLS before authenticating; the subject cannot
// carry a header of its own; the body arrives with CRLF line ends.
func TestSMTP(t *testing.T) {
	f, ch := smtpServer(t, true, false)
	m := Message{Title: "host: tank\r\nBcc: evil@example.org is DEGRADED", Body: "line one\nline two", Level: Error}
	if err := Send(t.Context(), ch, m); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	got := strings.Join(f.log, ",")
	if !strings.HasPrefix(got, "EHLO,STARTTLS,tls EHLO,tls AUTH,tls MAIL,tls RCPT,tls DATA") {
		t.Errorf("session %s", got)
	}
	if strings.Contains(f.data, "\r\nBcc:") || !strings.Contains(f.data, "Subject: host: tank  Bcc: evil@example.org is DEGRADED\r\n") {
		t.Errorf("headers:\n%s", f.data)
	}
	if !strings.Contains(f.data, "\r\n\r\nline one\r\nline two\r\n") {
		t.Errorf("body:\n%q", f.data)
	}
}

// A server without STARTTLS gets nothing, not even the password.
func TestSMTPRefusesClearText(t *testing.T) {
	f, ch := smtpServer(t, false, false)
	if err := Send(t.Context(), ch, Message{Title: "t"}); !errors.Is(err, ErrNoStartTLS) {
		t.Fatalf("%v", err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.log {
		if c == "AUTH" || c == "MAIL" {
			t.Errorf("sent %s in clear: %v", c, f.log)
		}
	}
}

// On the SMTPS port the whole session is TLS, with no STARTTLS.
func TestSMTPImplicitTLS(t *testing.T) {
	f, ch := smtpServer(t, false, true)
	old := smtpsPort
	smtpsPort = ch.Port
	t.Cleanup(func() { smtpsPort = old })
	if err := Send(t.Context(), ch, Message{Title: "t", Body: "b"}); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if got := strings.Join(f.log, ","); !strings.HasPrefix(got, "tls EHLO,tls AUTH,tls MAIL") {
		t.Errorf("session %s", got)
	}
}
