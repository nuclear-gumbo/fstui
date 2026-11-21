// SPDX-License-Identifier: GPL-3.0-or-later

package check

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nuclear-gumbo/fstui/internal/problem"
	"github.com/nuclear-gumbo/fstui/internal/zfs/health"

	"github.com/nuclear-gumbo/fstui/internal/config"
	"github.com/nuclear-gumbo/fstui/internal/events"
	"github.com/nuclear-gumbo/fstui/internal/notify"
	"github.com/nuclear-gumbo/fstui/internal/termtext"
	"github.com/nuclear-gumbo/fstui/internal/zfs"
	"github.com/nuclear-gumbo/fstui/internal/zfs/name"
)

type fakeSource struct {
	pools []zfs.Overview
	evs   []events.Event
	err   error
}

func (f *fakeSource) Measures(_ context.Context, now time.Time, _ map[string]bool) ([]problem.Measure, error) {
	if f.err != nil {
		return nil, f.err
	}
	return health.Measure(f.pools, now, zfs.Version{}), nil
}

func (f *fakeSource) Events(context.Context) ([]events.Event, int, error) {
	return f.evs, 0, nil
}

// outbox records what was sent, by channel; fail makes a channel fail.
type outbox struct {
	mu   sync.Mutex
	got  map[string][]notify.Message
	fail map[string]bool
}

func (o *outbox) send(_ context.Context, ch notify.Channel, m notify.Message) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.fail[ch.Name] {
		return errors.New("unreachable")
	}
	o.got[ch.Name] = append(o.got[ch.Name], m)
	return nil
}

func (o *outbox) take(ch string) []notify.Message {
	o.mu.Lock()
	defer o.mu.Unlock()
	m := o.got[ch]
	delete(o.got, ch)
	return m
}

func overview(t *testing.T, n, state string, used uint64) zfs.Overview {
	t.Helper()
	p, err := name.ParsePool(n)
	if err != nil {
		t.Fatal(err)
	}
	disk := zfs.Vdev{Type: "disk", State: "ONLINE"}
	return zfs.Overview{Pool: zfs.Pool{Name: p}, Status: zfs.PoolStatus{Name: p, State: state, Data: zfs.Vdev{Type: "root", Children: []zfs.Vdev{disk}}}, Used: used, Available: 100 - used}
}

type harness struct {
	t   *testing.T
	src *fakeSource
	out *outbox
	run Run
	now time.Time
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	h := &harness{t: t, src: &fakeSource{}, out: &outbox{got: map[string][]notify.Message{}, fail: map[string]bool{}}, now: time.Date(2026, 11, 17, 8, 0, 0, 0, time.UTC)}
	cfg := config.Default()
	cfg.Notifications.Channels = []config.Channel{
		{Name: "phone", Type: "ntfy", URL: "https://n", Topic: "z", MinSeverity: "warning"},
		{Name: "pager", Type: "webhook", URL: "https://w", MinSeverity: "error"},
	}
	h.run = Run{
		Source: h.src, Config: cfg, Secrets: config.Secrets{}, StateDir: filepath.Join(t.TempDir(), "state"),
		Host: "nas", Boot: "boot-1", Now: func() time.Time { return h.now }, Send: h.out.send,
	}
	return h
}

func (h *harness) once() Result {
	h.t.Helper()
	r := h.run.Once(h.t.Context())
	if r.StateErr != nil {
		h.t.Fatal(r.StateErr)
	}
	return r
}

func titles(ms []notify.Message) string {
	var s []string
	for _, m := range ms {
		s = append(s, m.Title)
	}
	return strings.Join(s, " | ")
}

// A problem is sent once, again after the repeat interval, and as
// resolved when it clears; each channel takes what it asked for.
func TestRules(t *testing.T) {
	h := newHarness(t)
	h.src.pools = []zfs.Overview{overview(t, "tank", "DEGRADED", 10), overview(t, "media", "ONLINE", 85)}
	h.once()
	if got := titles(h.out.take("phone")); got != "nas: 2 new" {
		t.Errorf("phone, first run: %q", got)
	}
	if got := titles(h.out.take("pager")); got != "nas: tank is DEGRADED" {
		t.Errorf("pager takes errors only: %q", got)
	}

	h.now = h.now.Add(time.Hour)
	h.once()
	if len(h.out.take("phone"))+len(h.out.take("pager")) != 0 {
		t.Error("sent again within the repeat interval")
	}

	h.now = h.now.Add(24 * time.Hour)
	h.once()
	if got := titles(h.out.take("phone")); got != "nas: 2 still open" {
		t.Errorf("after a day: %q", got)
	}
	h.out.take("pager")

	h.src.pools = []zfs.Overview{overview(t, "tank", "ONLINE", 10), overview(t, "media", "ONLINE", 85)}
	h.now = h.now.Add(time.Hour)
	h.once()
	if got := titles(h.out.take("pager")); got != "nas: resolved: tank is ONLINE again" {
		t.Errorf("resolved: %q", got)
	}
	if got := titles(h.out.take("phone")); got != "nas: resolved: tank is ONLINE again" {
		t.Errorf("phone resolved: %q", got)
	}
}

// A channel that fails is told next time; the others are not told twice.
func TestFailedChannelIsToldNextRun(t *testing.T) {
	h := newHarness(t)
	h.src.pools = []zfs.Overview{overview(t, "tank", "FAULTED", 10)}
	h.out.fail["pager"] = true
	r := h.once()
	if r.Failed["pager"] == nil || len(h.out.take("phone")) != 1 {
		t.Fatalf("first run: %+v", r)
	}
	h.out.fail["pager"] = false
	h.now = h.now.Add(10 * time.Minute)
	h.once()
	if len(h.out.take("pager")) != 1 || len(h.out.take("phone")) != 0 {
		t.Error("the failed channel was not told, or the other told twice")
	}

	h.src.pools = []zfs.Overview{overview(t, "tank", "ONLINE", 10)}
	h.out.fail["phone"] = true
	h.once()
	h.out.take("pager")
	h.out.fail["phone"] = false
	h.once()
	if got := titles(h.out.take("phone")); got != "nas: resolved: tank is ONLINE again" {
		t.Errorf("resolution after a failure: %q", got)
	}
	if len(h.out.take("pager")) != 0 {
		t.Error("pager told twice that tank is ONLINE")
	}
}

func event(id uint64, sev events.Severity, summary string) events.Event {
	return events.Event{ID: id, Severity: sev, Source: termtext.New("tank"), Summary: termtext.New(summary), Class: termtext.New("ereport.fs.zfs.checksum")}
}

// Events before the first run are not sent; later ones are, once, at or
// above events_min; ZED keeps them under auto when it sends them itself.
func TestEvents(t *testing.T) {
	h := newHarness(t)
	h.run.Config.Notifications.EventsMin = "warn"
	h.src.evs = []events.Event{event(1, events.Error, "old")}
	r := h.once()
	if r.Events != 0 || !strings.Contains(r.EventsNote, "first run") {
		t.Fatalf("first run: %+v", r)
	}
	h.src.evs = append(h.src.evs, event(2, events.Info, "boring"), event(3, events.Error, "Checksum error"))
	r = h.once()
	if got := titles(h.out.take("phone")); r.Events != 1 || got != "nas: tank: Checksum error; ereport.fs.zfs.checksum" {
		t.Errorf("events: %d %q", r.Events, got)
	}
	h.once()
	if len(h.out.take("phone")) != 0 {
		t.Error("an event was sent twice")
	}
	h.run.ZED = ZED{Running: true, Channels: []string{"ntfy"}}
	h.src.evs = append(h.src.evs, event(4, events.Error, "more"))
	if r := h.once(); r.Events != 0 || !strings.Contains(r.EventsNote, "left to ZED") {
		t.Errorf("auto with ZED: %+v", r)
	}
}

// Pools that cannot be read are a problem to report, and a channel
// without the secret it needs is skipped with the reason.
func TestReadFailureAndMissingSecret(t *testing.T) {
	h := newHarness(t)
	h.src.err = errors.New("zfs list: context deadline exceeded\nmore")
	h.run.Config.Notifications.Channels = append(h.run.Config.Notifications.Channels, config.Channel{Name: "chat", Type: "slack", MinSeverity: "warning"})
	r := h.once()
	if got := titles(h.out.take("pager")); got != "nas: fstui check could not read the pools: zfs list: context deadline exceeded" {
		t.Errorf("read failure: %q", got)
	}
	if !strings.Contains(r.Skipped["chat"], "no secret") {
		t.Errorf("skipped %v", r.Skipped)
	}
}

func TestZEDDetection(t *testing.T) {
	proc := t.TempDir()
	for pid, comm := range map[string]string{"12": "bash\n", "99": "zed\n"} {
		if err := os.MkdirAll(filepath.Join(proc, pid), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(proc, pid, "comm"), []byte(comm), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	rc := filepath.Join(t.TempDir(), "zed.rc")
	if err := os.WriteFile(rc, []byte("ZED_EMAIL_ADDR=\"root\"\n#ZED_SLACK_WEBHOOK_URL=\"x\"\nZED_NTFY_TOPIC=\"zfs\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	z := DetectZED(proc, rc)
	if !z.Running || strings.Join(z.Channels, ",") != "ntfy" || !z.SendsEvents() {
		t.Errorf("%+v", z)
	}
	if z := DetectZED(t.TempDir(), rc); z.Running || z.SendsEvents() {
		t.Errorf("no zed process: %+v", z)
	}
}

// The heartbeat goes out after every run that read the pools, and not
// after one that could not: its silence is the warning.
func TestHeartbeat(t *testing.T) {
	h := newHarness(t)
	h.run.Config.Notifications.HeartbeatURL = "https://hc.example.org/ping/abc"
	h.src.pools = []zfs.Overview{overview(t, "tank", "ONLINE", 10)}
	if r := h.once(); r.Heartbeat != "sent" || len(h.out.take("heartbeat")) != 1 {
		t.Errorf("after a good run: %q", r.Heartbeat)
	}
	h.src.err = errors.New("zpool: no such command")
	if r := h.once(); r.Heartbeat != "not sent: the pools could not be read" || len(h.out.take("heartbeat")) != 0 {
		t.Errorf("after a failed read: %q", r.Heartbeat)
	}
	h.run.Config.Notifications.HeartbeatURL = ""
	if r := h.once(); r.Heartbeat != "" {
		t.Errorf("without a URL: %q", r.Heartbeat)
	}
}
