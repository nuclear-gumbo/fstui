// SPDX-License-Identifier: GPL-3.0-or-later

package notify

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"mime"
	"net"
	"net/mail"
	"net/smtp"
	"strconv"
	"strings"
	"time"
)

// smtpsPort is the port of SMTP over TLS from the start (RFC 8314); a
// variable so the tests can use a port they can listen on.
var smtpsPort = 465

// ErrNoStartTLS is returned by a server on a port other than 465 that does
// not offer STARTTLS: fstui does not send mail, or a password, in clear.
var ErrNoStartTLS = errors.New("the server does not offer STARTTLS, and fstui sends mail only encrypted")

// sendSMTP sends m as a plain text mail: over TLS from the start on port
// 465, else after STARTTLS, which the server must offer. A connection that
// cannot be made is tried again like an HTTP request; once the server has
// answered, its answer is final.
func sendSMTP(ctx context.Context, ch Channel, m Message) error {
	from, to, err := addresses(ch)
	if err != nil {
		return err
	}
	tc, err := tlsConfig(ch, ch.Host)
	if err != nil {
		return err
	}
	msg := mailText(from, to, m, time.Now())
	ctx, cancel := context.WithTimeout(ctx, ch.total())
	defer cancel()
	wait := time.Second
	for attempt := 1; ; attempt++ {
		conn, err := dialSMTP(ctx, ch, tc)
		if err == nil {
			return deliver(ctx, conn, ch, tc, from, to, msg)
		}
		var cert *tls.CertificateVerificationError
		if attempt >= ch.tries() || errors.As(err, &cert) {
			return err
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("%w (gave up: %w)", err, ctx.Err())
		case <-time.After(wait):
		}
		wait *= 2
	}
}

// addresses checks the server, the sender and the recipients.
func addresses(ch Channel) (*mail.Address, []*mail.Address, error) {
	if ch.Host == "" || ch.Port == 0 {
		return nil, nil, fmt.Errorf("host or port %w", errMissing)
	}
	from, err := mail.ParseAddress(ch.From)
	if err != nil {
		return nil, nil, fmt.Errorf("from: %w", err)
	}
	if len(ch.To) == 0 {
		return nil, nil, fmt.Errorf("recipient %w", errMissing)
	}
	to := make([]*mail.Address, len(ch.To))
	for i, a := range ch.To {
		if to[i], err = mail.ParseAddress(a); err != nil {
			return nil, nil, fmt.Errorf("to: %w", err)
		}
	}
	return from, to, nil
}

// dialSMTP connects to the server, over TLS on port 465.
func dialSMTP(ctx context.Context, ch Channel, tc *tls.Config) (net.Conn, error) {
	addr := net.JoinHostPort(ch.Host, strconv.Itoa(ch.Port))
	d := &net.Dialer{Timeout: ch.attempt()}
	if ch.Port == smtpsPort {
		conn, err := (&tls.Dialer{NetDialer: d, Config: tc}).DialContext(ctx, "tcp", addr)
		if err != nil {
			return nil, fmt.Errorf("connecting: %w", err)
		}
		return conn, nil
	}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("connecting: %w", err)
	}
	return conn, nil
}

// deliver runs the SMTP session on conn: STARTTLS unless already on TLS,
// authentication if a user is set, then the mail.
func deliver(ctx context.Context, conn net.Conn, ch Channel, tc *tls.Config, from *mail.Address, to []*mail.Address, msg []byte) error {
	deadline, _ := ctx.Deadline()
	if err := conn.SetDeadline(deadline); err != nil {
		return fmt.Errorf("connection: %w", err)
	}
	c, err := smtp.NewClient(conn, ch.Host)
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("greeting: %w", err)
	}
	defer func() { _ = c.Close() }() // after Quit, or on an error already reported
	if _, isTLS := conn.(*tls.Conn); !isTLS {
		if ok, _ := c.Extension("STARTTLS"); !ok {
			return ErrNoStartTLS
		}
		if err := c.StartTLS(tc); err != nil {
			return fmt.Errorf("STARTTLS: %w", err)
		}
	}
	if ch.User != "" {
		if err := c.Auth(smtp.PlainAuth("", ch.User, ch.Secret, ch.Host)); err != nil {
			return fmt.Errorf("authentication: %w", err)
		}
	}
	if err := c.Mail(from.Address); err != nil {
		return fmt.Errorf("sender: %w", err)
	}
	for _, a := range to {
		if err := c.Rcpt(a.Address); err != nil {
			return fmt.Errorf("recipient %s: %w", a.Address, err)
		}
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("data: %w", err)
	}
	if _, err := w.Write(msg); err != nil {
		return fmt.Errorf("data: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("data: %w", err)
	}
	if err := c.Quit(); err != nil {
		return fmt.Errorf("quit: %w", err)
	}
	return nil
}

// mailText is the message with its headers. The subject is the title
// with control characters removed and, if not plain ASCII, encoded as RFC
// 2047 says; the body is UTF-8 text, line breaks as CRLF.
func mailText(from *mail.Address, to []*mail.Address, m Message, now time.Time) []byte {
	var id [12]byte
	_, _ = rand.Read(id[:]) // crypto/rand does not fail on Linux
	rcpt := make([]string, len(to))
	for i, a := range to {
		rcpt[i] = a.String()
	}
	_, domain, _ := strings.Cut(from.Address, "@")
	var b strings.Builder
	for _, h := range [][2]string{
		{"From", from.String()},
		{"To", strings.Join(rcpt, ", ")},
		{"Subject", mime.QEncoding.Encode("utf-8", headerSafe(m.Title))},
		{"Date", now.Format(time.RFC1123Z)},
		{"Message-ID", "<fstui-" + hex.EncodeToString(id[:]) + "@" + domain + ">"},
		{"MIME-Version", "1.0"},
		{"Content-Type", "text/plain; charset=utf-8"},
		{"Content-Transfer-Encoding", "8bit"},
	} {
		b.WriteString(h[0] + ": " + h[1] + "\r\n")
	}
	b.WriteString("\r\n")
	b.WriteString(strings.ReplaceAll(strings.ReplaceAll(m.Body, "\r\n", "\n"), "\n", "\r\n"))
	b.WriteString("\r\n")
	return []byte(b.String())
}
