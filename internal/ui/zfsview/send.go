// SPDX-License-Identifier: GPL-3.0-or-later

package zfsview

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/nuclear-gumbo/fstui/internal/termtext"
	"github.com/nuclear-gumbo/fstui/internal/theme"
	"github.com/nuclear-gumbo/fstui/internal/ui"
	"github.com/nuclear-gumbo/fstui/internal/ui/keys"
	"github.com/nuclear-gumbo/fstui/internal/zfs"
)

// Sender estimates send streams and reads the encryption state they
// depend on; *zfs.Reader is one.
type Sender interface {
	EstimateSend(ctx context.Context, s zfs.SendStep, o zfs.SendOptions) (zfs.SendEstimate, error)
	Get(ctx context.Context, names, props []string) (map[string]map[string]string, error)
}

// sendWindow estimates how large a zfs send of the marked snapshots
// would be, against a capacity such as one tape, and shows the commands.
// It only reads: nothing is sent.
type sendWindow struct {
	src      Sender
	marked   []zfs.SnapshotRow
	opts     zfs.SendOptions
	capacity uint64
	typing   *string // the capacity being typed
	message  string
	gen      int
	loading  bool
	steps    []zfs.SendStep
	sizes    []uint64
	errs     []termtext.Text // per step; empty when it estimated
	warnings []string
	err      termtext.Text // the whole estimate failed
	scroll   int
}

type sendMsg struct {
	w        *sendWindow
	gen      int
	sizes    []uint64
	errs     []termtext.Text
	warnings []string
	err      error
}

// openSend opens the window for the marked snapshots, then estimates.
func openSend(src Sender, marked []zfs.SnapshotRow) tea.Cmd {
	w := &sendWindow{src: src, marked: marked}
	open := func() tea.Msg { return ui.OpenModalMsg{Modal: w} }
	return tea.Sequence(open, w.estimate())
}

// estimate plans the streams for the current options and asks zfs for
// each one's size: about 10 ms a stream on 2.3.9.
func (w *sendWindow) estimate() tea.Cmd {
	w.gen++
	w.loading, w.err = true, termtext.Text{}
	w.steps = zfs.PlanSends(w.marked, w.opts)
	gen, steps, opts, src := w.gen, w.steps, w.opts, w.src
	return func() tea.Msg {
		ctx := context.Background()
		msg := sendMsg{w: w, gen: gen, sizes: make([]uint64, len(steps)), errs: make([]termtext.Text, len(steps))}
		enc, err := sendEncryption(ctx, src, steps)
		if err != nil {
			msg.err = err
			return msg
		}
		for i, s := range steps {
			ds, _, _ := strings.Cut(s.To, "@")
			e := enc[ds]
			switch {
			case e.encrypted && !opts.Raw && e.keyMissing:
				msg.errs[i] = termtext.New("the key of " + ds + " is not loaded; only a raw send (w) works")
				continue
			case e.encrypted && !opts.Raw && s.From == "":
				msg.warnings = append(msg.warnings, ds+" is encrypted: without w (raw) its stream carries the data decrypted")
			}
			est, err := src.EstimateSend(ctx, s, opts)
			if err != nil {
				msg.errs[i] = termtext.New(firstLine(err))
				continue
			}
			msg.sizes[i] = est.Total
		}
		return msg
	}
}

type encState struct{ encrypted, keyMissing bool }

// sendEncryption reads whether each dataset in steps is encrypted and has
// its key loaded: a dry run succeeds without the key, the send does not.
func sendEncryption(ctx context.Context, src Sender, steps []zfs.SendStep) (map[string]encState, error) {
	var names []string
	seen := map[string]bool{}
	for _, s := range steps {
		ds, _, _ := strings.Cut(s.To, "@")
		if !seen[ds] {
			seen[ds] = true
			names = append(names, ds)
		}
	}
	props, err := src.Get(ctx, names, []string{"encryption", "keystatus"})
	if err != nil {
		return nil, err
	}
	out := map[string]encState{}
	for ds, p := range props {
		out[ds] = encState{encrypted: p["encryption"] != "" && p["encryption"] != "off", keyMissing: p["keystatus"] == "unavailable"}
	}
	return out, nil
}

func firstLine(err error) string {
	l, _, _ := strings.Cut(strings.TrimSpace(err.Error()), "\n")
	return l
}

func (w *sendWindow) Busy() bool { return false }

var (
	keySendCompressed = key.NewBinding(key.WithKeys("c"), key.WithHelp("c", "compressed"))
	keySendRaw        = key.NewBinding(key.WithKeys("w"), key.WithHelp("w", "raw"))
	keySendBetween    = key.NewBinding(key.WithKeys("I"), key.WithHelp("I", "every snapshot between"))
	keySendCapacity   = key.NewBinding(key.WithKeys("t"), key.WithHelp("t", "capacity"))
)

func (w *sendWindow) Keys() []key.Binding {
	if w.typing != nil {
		return []key.Binding{
			key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "set capacity")),
			key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "cancel")),
		}
	}
	return []key.Binding{keySendCompressed, keySendRaw, keySendBetween, keySendCapacity, keys.As(keys.Down, "scroll"), keys.As(keys.Back, "close")}
}

func (w *sendWindow) Update(msg tea.Msg) (ui.Modal, tea.Cmd) {
	switch msg := msg.(type) {
	case sendMsg:
		if msg.w == w && msg.gen == w.gen {
			w.loading, w.sizes, w.errs, w.warnings = false, msg.sizes, msg.errs, msg.warnings
			if msg.err != nil {
				w.err = termtext.New(firstLine(msg.err))
			}
		}
	case tea.KeyPressMsg:
		if w.typing != nil {
			w.typeCapacity(msg)
			return w, nil
		}
		return w.key(msg)
	}
	return w, nil
}

func (w *sendWindow) key(msg tea.KeyPressMsg) (ui.Modal, tea.Cmd) {
	w.message = ""
	switch {
	case key.Matches(msg, keys.Get(keys.Back)):
		return nil, nil
	case key.Matches(msg, keySendCompressed):
		w.opts.Compressed = !w.opts.Compressed
		return w, w.estimate()
	case key.Matches(msg, keySendRaw):
		w.opts.Raw = !w.opts.Raw
		return w, w.estimate()
	case key.Matches(msg, keySendBetween):
		w.opts.Intermediates = !w.opts.Intermediates
		return w, w.estimate()
	case key.Matches(msg, keySendCapacity):
		s := ""
		w.typing = &s
	case key.Matches(msg, keys.Get(keys.Down)):
		w.scroll++
	case key.Matches(msg, keys.Get(keys.Up)):
		w.scroll = max(w.scroll-1, 0)
	}
	return w, nil
}

// maxCapacityText bounds the typed capacity.
const maxCapacityText = 16

func (w *sendWindow) typeCapacity(msg tea.KeyPressMsg) {
	switch k := msg.String(); {
	case k == "esc":
		w.typing = nil
	case k == "enter":
		if strings.TrimSpace(*w.typing) == "" {
			w.capacity, w.typing = 0, nil
			return
		}
		n, err := parseCapacity(*w.typing)
		if err != nil {
			w.message = err.Error()
			return
		}
		w.capacity, w.typing = n, nil
	case k == "backspace" && *w.typing != "":
		*w.typing = (*w.typing)[:len(*w.typing)-1]
	case len(msg.Text) == 1 && len(*w.typing) < maxCapacityText && strings.ContainsAny(msg.Text, "0123456789.kKmMgGtTpPiIbB"):
		*w.typing += msg.Text
	}
}

var errCapacity = errors.New("capacity: a number and a unit, such as 18TB (decimal, as tapes are sold) or 16T (binary, as ZFS counts)")

// parseCapacity reads a capacity: KB, MB, GB, TB and PB are decimal, as
// drive and tape makers count; K, M, G, T and P (or KiB to PiB) are
// binary, as ZFS counts. A plain number is bytes.
func parseCapacity(s string) (uint64, error) {
	s = strings.TrimSpace(s)
	num := strings.TrimRight(s, "kKmMgGtTpPiIbB")
	unit := strings.ToUpper(s[len(num):])
	v, err := strconv.ParseFloat(num, 64)
	if err != nil || v <= 0 {
		return 0, errCapacity
	}
	mult := map[string]float64{
		"": 1, "B": 1,
		"K": 1 << 10, "KIB": 1 << 10, "M": 1 << 20, "MIB": 1 << 20, "G": 1 << 30, "GIB": 1 << 30,
		"T": 1 << 40, "TIB": 1 << 40, "P": 1 << 50, "PIB": 1 << 50,
		"KB": 1e3, "MB": 1e6, "GB": 1e9, "TB": 1e12, "PB": 1e15,
	}[unit]
	if mult == 0 || v*mult >= 1<<63 {
		return 0, errCapacity
	}
	return uint64(v * mult), nil
}

func (w *sendWindow) total() (uint64, int) {
	var sum uint64
	failed := 0
	for i := range w.steps {
		if i < len(w.errs) && !w.errs[i].IsEmpty() {
			failed++
			continue
		}
		if i < len(w.sizes) {
			sum += w.sizes[i]
		}
	}
	return sum, failed
}

func (w *sendWindow) Render(d ui.Display, width, height int) string {
	iw, ih := ui.Inner(width, height)
	muted, warn := d.Palette.Style(theme.Muted), d.Palette.Style(theme.Warn)
	lines := []string{muted.Render(ansi.Truncate("Estimated by zfs send -n; nothing is sent.", iw, "…")), ""}
	switch {
	case !w.err.IsEmpty():
		lines = append(lines, d.Palette.Style(theme.Error).Render(ansi.Truncate(w.err.Display(), iw, "…")),
			muted.Render("Without root, this needs the delegated send permission."))
	case w.loading && w.sizes == nil:
		lines = append(lines, muted.Render("Asking zfs…"))
	default:
		lines = append(lines, w.streamLines(d, iw)...)
		lines = append(lines, "", w.totalLine(d, iw))
	}
	on := map[bool]string{true: "on", false: "off"}
	lines = append(lines, "", ansi.Truncate(fmt.Sprintf("c compressed: %s   w raw: %s   I every snapshot between: %s",
		on[w.opts.Compressed], on[w.opts.Raw], on[w.opts.Intermediates]), iw, "…"))
	capLine := "t capacity: none"
	switch {
	case w.typing != nil:
		capLine = "capacity: " + *w.typing + ui.Cursor(d) + muted.Render("   18TB decimal, 16T binary; enter sets, empty clears")
	case w.capacity > 0:
		capLine = "t capacity: " + ui.Size(w.capacity, d.Exact)
	}
	lines = append(lines, ansi.Truncate(capLine, iw, "…"))
	if w.message != "" {
		lines = append(lines, warn.Render(ansi.Wrap(w.message, iw, " ")))
	}
	for _, s := range w.warnings {
		lines = append(lines, warn.Render(ansi.Wrap(s, iw, " ")))
	}
	if cmds := w.commands(); len(cmds) > 0 {
		lines = append(lines, "", muted.Render("Commands"))
		for _, c := range cmds {
			lines = append(lines, ansi.Wrap(c, iw, " "))
		}
	}
	room := max(ih-1, 1)
	w.scroll = min(w.scroll, max(len(lines)-room, 0))
	lines = lines[w.scroll:min(len(lines), w.scroll+room)]
	return ui.Panel{Title: "send size", Focused: true}.Render(d, strings.Join(lines, "\n"), width, min(height, len(lines)+2))
}

// streamLines lists each stream with its estimated size, in a column,
// or why it has none.
func (w *sendWindow) streamLines(d ui.Display, iw int) []string {
	labels := make([]string, len(w.steps))
	col := 0
	for i, s := range w.steps {
		labels[i] = streamLabel(s)
		col = max(col, ansi.StringWidth(labels[i]))
	}
	col = min(col, max(iw-sizeWidth-2, 8))
	var out []string
	for i := range w.steps {
		size := ""
		switch {
		case i < len(w.errs) && !w.errs[i].IsEmpty():
			size = d.Palette.Style(theme.Error).Render(w.errs[i].Display())
		case w.loading:
			size = d.Palette.Style(theme.Muted).Render(lpad("…", sizeWidth))
		case i < len(w.sizes):
			size = lpad(ui.Size(w.sizes[i], d.Exact), sizeWidth)
		}
		out = append(out, ansi.Truncate(pad(ansi.Truncate(labels[i], col, "…"), col)+"  "+size, iw, "…"))
	}
	return out
}

// streamLabel names a stream: "full tank/a@1", or "tank/a @1 → @2".
func streamLabel(s zfs.SendStep) string {
	if s.From == "" {
		return "full " + termtext.New(s.To).Display()
	}
	ds, to, _ := strings.Cut(s.To, "@")
	from := s.From
	if _, snap, ok := strings.Cut(s.From, "@"); ok {
		from = "@" + snap
	} else if _, mark, ok := strings.Cut(s.From, "#"); ok {
		from = "#" + mark
	}
	return termtext.New(ds).Display() + " " + termtext.New(from).Display() + " → @" + termtext.New(to).Display()
}

// totalLine is the sum, and how much of the capacity it takes.
func (w *sendWindow) totalLine(d ui.Display, iw int) string {
	sum, failed := w.total()
	line := "Total " + ui.Size(sum, d.Exact)
	if failed > 0 {
		line += fmt.Sprintf(" (%d stream(s) without an estimate)", failed)
	}
	if w.capacity > 0 {
		pct := int(min(sum*100/w.capacity, 999)) //nolint:gosec // bounded above
		line += fmt.Sprintf(" of %s, %d%%", ui.Size(w.capacity, d.Exact), pct)
		if sum > w.capacity {
			return d.Palette.Style(theme.Warn).Render(ansi.Truncate(line+": does not fit", iw, "…"))
		}
		if bw := iw - ansi.StringWidth(line) - 2; bw >= 10 {
			line += "  " + ui.Gauge(d, min(pct, 100), min(bw, 30))
		}
	}
	return ansi.Truncate(line, iw, "…")
}

// commands are the zfs send commands for the streams that estimated,
// quoted for a POSIX shell. They are not run.
func (w *sendWindow) commands() []string {
	if w.loading {
		return nil
	}
	var out []string
	for i, s := range w.steps {
		if i < len(w.errs) && !w.errs[i].IsEmpty() {
			continue
		}
		out = append(out, termtext.New(s.Argv(w.opts, false).Shell()).Display())
	}
	return out
}
