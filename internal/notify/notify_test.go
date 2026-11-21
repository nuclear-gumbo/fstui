// SPDX-License-Identifier: GPL-3.0-or-later

package notify

import (
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// server is an HTTPS test server and a channel that trusts it through a
// CA file, as a self-hosted server with a private certificate would be.
func server(t *testing.T, typ string, h http.HandlerFunc) Channel {
	t.Helper()
	s := httptest.NewTLSServer(h)
	t.Cleanup(s.Close)
	ca := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: s.Certificate().Raw}), 0o600); err != nil {
		t.Fatal(err)
	}
	return Channel{Name: "test", Type: typ, URL: s.URL, CAFile: ca}
}

type seen struct {
	method, path, title, priority, auth, ctype string
	body                                       []byte
}

func recording(t *testing.T, got *seen, status int) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		*got = seen{r.Method, r.URL.Path, r.Header.Get("X-Title"), r.Header.Get("X-Priority"), r.Header.Get("Authorization"), r.Header.Get("Content-Type"), b}
		w.WriteHeader(status)
	}
}

// A title carrying CR and LF from a hostile pool name cannot add headers.
func TestNtfy(t *testing.T) {
	var got seen
	ch := server(t, "ntfy", recording(t, &got, http.StatusOK))
	ch.Topic, ch.User, ch.Secret = "zfs alerts", "alice", "s3cret"
	m := Message{Title: "host: tank\r\nX-Evil: 1 is DEGRADED", Body: "tank is DEGRADED: 1 device OFFLINE", Level: Error}
	if err := Send(t.Context(), ch, m); err != nil {
		t.Fatal(err)
	}
	if got.method != "POST" || got.path != "/zfs alerts" || got.priority != "5" || string(got.body) != m.Body {
		t.Errorf("request %+v", got)
	}
	if strings.ContainsAny(got.title, "\r\n") || got.title != "host: tank  X-Evil: 1 is DEGRADED" {
		t.Errorf("title %q", got.title)
	}
	if !strings.HasPrefix(got.auth, "Basic ") {
		t.Errorf("auth %q", got.auth)
	}
	ch.User = ""
	if err := Send(t.Context(), ch, m); err != nil || got.auth != "Bearer s3cret" {
		t.Errorf("token: %q, %v", got.auth, err)
	}
}

func TestWebhook(t *testing.T) {
	var got seen
	ch := server(t, "webhook", recording(t, &got, http.StatusNoContent))
	m := Message{Title: `tank "quoted"`, Body: "line\nnext", Level: Warning}
	if err := Send(t.Context(), ch, m); err != nil {
		t.Fatal(err)
	}
	var b webhookBody
	if err := json.Unmarshal(got.body, &b); err != nil || b != (webhookBody{Title: m.Title, Message: m.Body, Level: "warning"}) || got.ctype != "application/json" {
		t.Errorf("body %s, %v", got.body, err)
	}
}

// 5xx is tried again, honouring Retry-After; 4xx fails at once with the
// server's message.
func TestRetries(t *testing.T) {
	var calls atomic.Int32
	ch := server(t, "webhook", func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) < 3 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	start := time.Now()
	if err := Send(t.Context(), ch, Message{Title: "t"}); err != nil || calls.Load() != 3 {
		t.Fatalf("%d calls, %v", calls.Load(), err)
	}
	if d := time.Since(start); d < 2*time.Second {
		t.Errorf("retried after %v, sooner than Retry-After", d)
	}

	calls.Store(0)
	ch = server(t, "webhook", func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		http.Error(w, "unauthorized: bad token\nmore", http.StatusUnauthorized)
	})
	err := Send(t.Context(), ch, Message{Title: "t"})
	if err == nil || calls.Load() != 1 || !strings.Contains(err.Error(), "401 Unauthorized: unauthorized: bad token") || strings.Contains(err.Error(), "more") {
		t.Errorf("%d calls, %v", calls.Load(), err)
	}
}

// Plain HTTP needs insecure_http; a redirect is not followed; an
// untrusted certificate fails.
func TestRefusals(t *testing.T) {
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	defer plain.Close()
	ch := Channel{Name: "p", Type: "webhook", URL: plain.URL}
	if err := Send(t.Context(), ch, Message{}); !errors.Is(err, ErrInsecureURL) {
		t.Errorf("http: %v", err)
	}
	ch.InsecureHTTP = true
	if err := Send(t.Context(), ch, Message{}); err != nil {
		t.Errorf("http allowed: %v", err)
	}

	moved := server(t, "webhook", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://example.org/", http.StatusFound)
	})
	if err := Send(t.Context(), moved, Message{}); !errors.Is(err, ErrRedirect) {
		t.Errorf("redirect: %v", err)
	}

	untrusted := server(t, "webhook", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	untrusted.CAFile = ""
	start := time.Now()
	if err := Send(t.Context(), untrusted, Message{}); err == nil || !strings.Contains(err.Error(), "certificate") || time.Since(start) > time.Second {
		t.Errorf("untrusted, after %v: %v", time.Since(start), err)
	}
	if err := Send(t.Context(), Channel{Name: "x", Type: "carrier-pigeon"}, Message{}); !errors.Is(err, ErrUnknownType) {
		t.Errorf("type: %v", err)
	}
}

// A secret carried in the URL (Discord and Slack webhooks, Telegram's bot
// token) never appears in an error, which fstui check prints and the
// journal keeps.
func TestErrorsHideTheSecret(t *testing.T) {
	const token = "s3cr3t-T0KEN"
	msg := Message{Title: "t", Body: "b"}
	for _, ch := range []Channel{
		{Name: "d", Type: "discord", Secret: "http://127.0.0.1:1/api/webhooks/1/" + token, InsecureHTTP: true, Retries: 0, AttemptTimeout: time.Second, SendTimeout: time.Second},
		{Name: "t", Type: "telegram", URL: "http://127.0.0.1:1", Secret: token, ChatID: "1", InsecureHTTP: true, AttemptTimeout: time.Second, SendTimeout: time.Second},
		{Name: "s", Type: "slack", Secret: "https://hooks.example/%zz" + token},
	} {
		err := Send(t.Context(), ch, msg)
		if err == nil || strings.Contains(err.Error(), token) {
			t.Errorf("%s: %v", ch.Type, err)
		}
	}
}

// A heartbeat is a GET of its URL; a failure does not show the URL,
// which identifies the monitor.
func TestHeartbeat(t *testing.T) {
	var got seen
	ch := server(t, "heartbeat", recording(t, &got, http.StatusOK))
	ch.URL += "/ping/5f2c-secret"
	if err := Send(t.Context(), ch, Message{Title: "fstui check ran"}); err != nil {
		t.Fatal(err)
	}
	if got.method != http.MethodGet || got.path != "/ping/5f2c-secret" || len(got.body) != 0 {
		t.Errorf("request %+v", got)
	}
	bad := server(t, "heartbeat", recording(t, &got, http.StatusNotFound))
	bad.URL += "/ping/5f2c-secret"
	err := Send(t.Context(), bad, Message{})
	if err == nil || strings.Contains(err.Error(), "5f2c-secret") {
		t.Errorf("error %v", err)
	}
	if err := Send(t.Context(), Channel{Name: "h", Type: "heartbeat", URL: "http://example.org/x"}, Message{}); !errors.Is(err, ErrInsecureURL) {
		t.Errorf("http:// sent: %v", err)
	}
}
