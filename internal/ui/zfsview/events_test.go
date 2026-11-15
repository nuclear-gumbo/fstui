// SPDX-License-Identifier: GPL-3.0-or-later

package zfsview

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/nuclear-gumbo/fstui/internal/config"
	"github.com/nuclear-gumbo/fstui/internal/events"
	"github.com/nuclear-gumbo/fstui/internal/zfs"
)

// fakeEvents hands out the captured events, plus any added later.
type fakeEvents struct{ evs []events.Event }

func (f *fakeEvents) Events(context.Context) ([]events.Event, int, error) { return f.evs, 0, nil }

func capturedEvents(t *testing.T) []events.Event {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "testdata", "zfs", "2.3.9", "events", "induced-v.txt"))
	if err != nil {
		t.Fatal(err)
	}
	evs, _, err := zfs.ParseEvents(b)
	if err != nil {
		t.Fatal(err)
	}
	return evs
}

func loadedEvents(t *testing.T, filters ...config.EventFilter) (*Events, *fakeEvents) {
	t.Helper()
	src := &fakeEvents{evs: capturedEvents(t)}
	v := NewEvents(src, EventsOptions{Root: true, Retention: 5000, Filters: filters, Now: func() time.Time { return now }})
	v.Update(v.Init()())
	return v, src
}

func press(v *Events, keys ...string) {
	for _, k := range keys {
		msg := tea.KeyPressMsg{Code: rune(k[0]), Text: k}
		switch k {
		case "enter":
			msg = tea.KeyPressMsg{Code: tea.KeyEnter}
		case "esc":
			msg = tea.KeyPressMsg{Code: tea.KeyEscape}
		}
		v.Update(msg)
	}
}

func renderEvents(v *Events, w, h int) string { return ansi.Strip(v.Render(display(), w, h)) }

func TestEventsNeedRoot(t *testing.T) {
	v := NewEvents(&fakeEvents{}, EventsOptions{Retention: 100, Now: func() time.Time { return now }})
	if v.Init() != nil || v.Activate() != nil {
		t.Error("read events without root")
	}
	if out := renderEvents(v, 100, 20); !strings.Contains(out, "ZFS events need root") {
		t.Errorf("unprivileged:\n%s", out)
	}
}

// The capture holds 93 events; the newest is selected while following.
func TestEventsFollowAndPause(t *testing.T) {
	v, src := loadedEvents(t)
	out := renderEvents(v, 110, 30)
	if !strings.Contains(out, "93 kept, 93 shown") || !strings.Contains(out, "following") || v.shown()[v.cursor].ID != src.evs[92].ID {
		t.Fatalf("after loading:\n%s", out)
	}
	press(v, "j") // one older: following stops
	selected := v.shown()[v.cursor].ID
	newer := src.evs[92]
	newer.ID++
	src.evs = append(src.evs, newer)
	v.Update(v.poll.refresh()())
	if v.follow || v.unseen != 1 || v.shown()[v.cursor].ID != selected {
		t.Fatalf("paused: follow %v, unseen %d, selection moved", v.follow, v.unseen)
	}
	if out := renderEvents(v, 110, 30); !strings.Contains(out, "paused, 1 new") {
		t.Errorf("header:\n%s", strings.Split(out, "\n")[1])
	}
	press(v, "F")
	if !v.follow || v.cursor != 0 || v.unseen != 0 {
		t.Errorf("F: follow %v cursor %d unseen %d", v.follow, v.cursor, v.unseen)
	}
}

// Counts from the capture: 20 checksum errors and one OFFLINE state
// change are the only events above INFO.
func TestEventsFilters(t *testing.T) {
	v, _ := loadedEvents(t, config.EventFilter{Name: "errors", Severities: []string{"error"}})
	count := func() int { return len(v.shown()) }
	for _, want := range []int{21, 21, 20, 93} { // NOTICE+, WARN+, ERROR, all
		press(v, "v")
		if count() != want {
			t.Errorf("after v: %d shown, want %d (%s)", count(), want, v.filter.describe())
		}
	}
	press(v, "c") // the first class in name order: checksum
	if count() != 20 || v.filter.class != "checksum" {
		t.Errorf("class %q: %d shown", v.filter.class, count())
	}
	press(v, "c", "c", "c", "c", "c", "c", "c", "c", "c", "c", "c", "c") // back to all classes
	press(v, "P")
	if v.filter.pool != "fstuitest_ev" {
		t.Errorf("pool %q", v.filter.pool)
	}
	press(v, "f")
	if count() != 20 || !strings.Contains(v.filter.describe(), "saved: errors") {
		t.Errorf("saved filter: %d shown, %s", count(), v.filter.describe())
	}
	press(v, "f")
	if count() != 93 {
		t.Errorf("no saved filter: %d shown", count())
	}
}

func TestEventsTextFilter(t *testing.T) {
	v, _ := loadedEvents(t)
	press(v, "/", "O", "F", "F", "L", "I", "N", "E")
	if !v.Typing() || !strings.Contains(renderEvents(v, 110, 30), "/ OFFLINE_") {
		t.Fatal("not typing the filter")
	}
	matched := len(v.shown())
	press(v, "enter")
	if v.Typing() || matched == 0 || matched >= 93 || len(v.shown()) != matched {
		t.Errorf("text filter: %d of 93 shown", matched)
	}
	press(v, "/", "x", "esc") // cancelling keeps the previous text
	if v.filter.text != "OFFLINE" || len(v.shown()) != matched {
		t.Errorf("after cancel: %q", v.filter.text)
	}
}

func TestEventsDetail(t *testing.T) {
	v, _ := loadedEvents(t)
	press(v, "c", "enter") // the newest checksum error
	if !v.focus || !v.Nested() {
		t.Fatal("enter did not open the detail")
	}
	out := renderEvents(v, 140, 40)
	if !strings.Contains(out, "Checksum error") || !strings.Contains(out, "vdev_path") || !strings.Contains(out, "zio_offset") {
		t.Errorf("detail:\n%s", out)
	}
	press(v, "esc")
	if v.focus {
		t.Error("esc did not leave the detail")
	}
}
