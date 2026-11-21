// SPDX-License-Identifier: GPL-3.0-or-later

package notify

import (
	"bytes"
	"cmp"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// Defaults of one send, for a channel that sets none: each attempt has
// defaultAttempt, the whole send defaultSend, and failed attempts that may
// succeed later are tried defaultRetries more times, waiting 1, 2, 4
// seconds and so on, or what the server asks for with Retry-After.
const (
	defaultAttempt = 10 * time.Second
	defaultSend    = 30 * time.Second
	defaultRetries = 3
	maxErrorBody   = 4 << 10 // of a server's error answer, read for the message
)

// attempt, total and tries are ch's timing, with the defaults for what it
// leaves at zero (a Channel built without FromConfig).
func (ch Channel) attempt() time.Duration { return cmp.Or(ch.AttemptTimeout, defaultAttempt) }
func (ch Channel) total() time.Duration   { return cmp.Or(ch.SendTimeout, defaultSend) }
func (ch Channel) tries() int {
	if ch.AttemptTimeout == 0 && ch.SendTimeout == 0 && ch.Retries == 0 {
		return defaultRetries + 1
	}
	return ch.Retries + 1
}

var (
	// ErrInsecureURL is returned for a URL that is not https:// on a
	// channel that does not allow plain HTTP.
	ErrInsecureURL = errors.New("not an https:// URL (insecure_http allows http://)")
	// ErrRedirect is returned when the server answers with a redirect:
	// fstui does not follow one, so a request with credentials goes only
	// where the configuration says.
	ErrRedirect = errors.New("the server redirected the request")
)

// client is an HTTP client for ch: TLS 1.2 or newer, the system's
// certificate authorities plus ch.CAFile, no proxy (the environment's
// proxy variables are not read), no redirects.
func client(ch Channel) (*http.Client, error) {
	tc, err := tlsConfig(ch, "")
	if err != nil {
		return nil, err
	}
	return &http.Client{
		Timeout: ch.attempt(),
		Transport: &http.Transport{
			Proxy:               nil,
			TLSClientConfig:     tc,
			TLSHandshakeTimeout: ch.attempt(),
			ForceAttemptHTTP2:   true,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return ErrRedirect },
	}, nil
}

// tlsConfig is TLS 1.2 or newer, verifying the server against the
// system's certificate authorities plus ch.CAFile; serverName is the name
// to verify where the connection does not set it (SMTP).
func tlsConfig(ch Channel, serverName string) (*tls.Config, error) {
	roots, err := x509.SystemCertPool()
	if err != nil {
		roots = x509.NewCertPool()
	}
	if ch.CAFile != "" {
		pem, err := os.ReadFile(ch.CAFile)
		if err != nil {
			return nil, fmt.Errorf("reading the CA file: %w", err)
		}
		if !roots.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("%s holds no PEM certificate", ch.CAFile)
		}
	}
	return &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots, ServerName: serverName}, nil
}

// checkURL parses raw and refuses plain HTTP unless ch allows it.
func checkURL(ch Channel, raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		// url.Parse's error quotes the whole URL, which for Discord and
		// Slack is the secret: keep only what went wrong.
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return nil, fmt.Errorf("URL: %w", err)
	}
	switch {
	case u.Scheme == "https" && u.Host != "":
	case u.Scheme == "http" && u.Host != "" && ch.InsecureHTTP:
	default:
		return nil, ErrInsecureURL
	}
	return u, nil
}

// request is one HTTP request a sender makes; post sends it again on a
// retry, so the body is kept as bytes.
type request struct {
	method string
	url    *url.URL
	header http.Header
	body   []byte
}

// post sends r for ch. Network errors, 5xx and 429 are tried again, up to
// ch.tries() times within ch.total(); any other answer outside 2xx fails at
// once with the first line of what the server said.
func post(ctx context.Context, ch Channel, r request) error {
	c, err := client(ch)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, ch.total())
	defer cancel()
	wait := time.Second
	var last error
	for attempt := 1; attempt <= ch.tries(); attempt++ {
		retry, after, err := attemptOnce(ctx, c, r)
		if err == nil {
			return nil
		}
		last = err
		if !retry || attempt == ch.tries() {
			break
		}
		if after > 0 {
			wait = after
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("%w (gave up: %w)", last, ctx.Err())
		case <-time.After(wait):
		}
		wait *= 2
	}
	return last
}

// attemptOnce makes one request. retry says whether trying again can
// help; after is the server's Retry-After, if it gave one.
func attemptOnce(ctx context.Context, c *http.Client, r request) (retry bool, after time.Duration, err error) {
	req, err := http.NewRequestWithContext(ctx, r.method, r.url.String(), bytes.NewReader(r.body))
	if err != nil {
		return false, 0, fmt.Errorf("request: %w", err)
	}
	addHeaders(req.Header, r.header)
	resp, err := c.Do(req)
	if err != nil {
		hideURL(err)
		var cert *tls.CertificateVerificationError
		switch {
		case errors.Is(err, ErrRedirect):
			return false, 0, ErrRedirect
		case errors.As(err, &cert): // a certificate does not become valid by asking again
			return false, 0, fmt.Errorf("sending: %w", err)
		}
		return true, 0, fmt.Errorf("sending: %w", err)
	}
	defer func() { _ = resp.Body.Close() }() // the answer was read; nothing to report
	if resp.StatusCode/100 == 2 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxErrorBody))
		return false, 0, nil
	}
	b, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
	err = fmt.Errorf("the server answered %s%s", resp.Status, firstLine(b))
	retry = resp.StatusCode/100 == 5 || resp.StatusCode == http.StatusTooManyRequests
	if s, perr := strconv.Atoi(resp.Header.Get("Retry-After")); perr == nil && s > 0 {
		after = time.Duration(s) * time.Second
	}
	return retry, after, err
}

func addHeaders(dst, src http.Header) {
	for k, vs := range src {
		for _, v := range vs {
			dst.Add(k, v)
		}
	}
}

// firstLine is ": " and the first line of a server's answer, made safe to
// show, or "" for an empty answer.
func firstLine(b []byte) string {
	line, _, _ := strings.Cut(strings.TrimSpace(string(b)), "\n")
	line = headerSafe(line)
	if len(line) > 200 {
		line = line[:200] + "…"
	}
	if line == "" {
		return ""
	}
	return ": " + line
}

// hideURL cuts the URL in a failed request's error down to its scheme
// and host: the path or query may hold the secret (Discord and Slack
// webhooks, Telegram's bot token), and the error is printed and logged.
func hideURL(err error) {
	var ue *url.Error
	if !errors.As(err, &ue) {
		return
	}
	if u, perr := url.Parse(ue.URL); perr == nil {
		ue.URL = u.Scheme + "://" + u.Host + "/…"
	} else {
		ue.URL = "…"
	}
}

// sendHeartbeat requests ch.URL with GET, which Healthchecks and Uptime
// Kuma's push monitors take as "still running". The URL is the secret
// that identifies the monitor, so errors do not show it.
func sendHeartbeat(ctx context.Context, ch Channel) error {
	u, err := checkURL(ch, ch.URL)
	if err != nil {
		return err
	}
	err = post(ctx, ch, request{method: http.MethodGet, url: u, header: http.Header{}})
	hideURL(err)
	return err
}
