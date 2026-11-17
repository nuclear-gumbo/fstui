// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !fstui_nowrite

package actions

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"

	tea "charm.land/bubbletea/v2"

	"github.com/nuclear-gumbo/fstui/internal/mutate"
	"github.com/nuclear-gumbo/fstui/internal/tune"
	"github.com/nuclear-gumbo/fstui/internal/ui"
	"github.com/nuclear-gumbo/fstui/internal/zfs/plan"
)

func TestMenuDisablesWhatIsNotDelegated(t *testing.T) {
	built := 0
	build := func(context.Context, map[string]string) (*mutate.Plan, error) {
		built++
		return testPlan(mutate.SnapshotCreate, "tank/a"), nil
	}
	offers := []Offer{
		{Key: "s", Name: "Create snapshot", Need: needs("snapshot", "mount"), Build: build},
		{Key: "D", Name: "Destroy", Need: needs("destroy", "mount"), Build: build},
		{
			Key: "e", Name: "Set a property", Need: propertyNeed, Build: build,
			Fields: []Field{{Name: "property", Label: "Property", Default: "quota"}, {Name: "value", Label: "Value", Default: "1G"}},
		},
	}
	perms := func(context.Context) (map[string]bool, error) {
		return map[string]bool{"snapshot": true, "mount": true, "compression": true}, nil
	}
	m, cmd := NewMenu("tank/a", offers, func(*mutate.Plan, Rebuild) ui.Modal { return nil }, perms)
	if _, c := m.Update(tea.KeyPressMsg{Code: 's', Text: "s"}); c != nil {
		t.Fatal("an offer was chosen before the permissions were known")
	}
	m.Update(cmd())
	if m.offers[0].Disabled != "" || !strings.Contains(m.offers[1].Disabled, "needs root or delegated destroy") || m.offers[2].Disabled != "" {
		t.Fatalf("offers = %+v", m.offers)
	}
	next, _ := m.Update(tea.KeyPressMsg{Code: 'e', Text: "e"})
	f, ok := next.(*Form)
	if !ok {
		t.Fatalf("e opened %T", next)
	}
	f.Update(tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModCtrl})
	if !strings.Contains(f.err, "delegated quota") || built != 0 {
		t.Fatalf("form err %q, built %d", f.err, built)
	}
}

// Menu letters must not be keys the menu uses to move or close, or the
// change can only be reached with the arrows.
func TestOfferKeysDoNotCollideWithMenuKeys(t *testing.T) {
	offers := ZFSOffers(plan.Builder{}, time.Now)
	reserved := map[string]bool{"j": true, "k": true, "q": true, "g": true, "G": true, " ": true}
	for _, kind := range []string{"filesystem", "volume", "snapshot", "pool", "pools", "device", "arc"} {
		_, list := offers(ui.Target{Backend: "zfs", Kind: kind, Names: []string{"tank/a@1"}, Parent: "tank"})
		seen := map[string]string{}
		for _, o := range list {
			if reserved[o.Key] {
				t.Errorf("%s: %q uses %q, which the menu uses to move or close", kind, o.Name, o.Key)
			}
			if other, dup := seen[o.Key]; dup {
				t.Errorf("%s: %q and %q share %q", kind, other, o.Name, o.Key)
			}
			seen[o.Key] = o.Name
		}
	}
}

func TestMenuSaysWhatEachChangeAsks(t *testing.T) {
	offers := []Offer{
		{Key: "s", Name: "Create snapshot", Op: mutate.SnapshotCreate},
		{Key: "D", Name: "Destroy", Op: mutate.DatasetDestroy},
		{Key: "d", Name: "Destroy snapshot", Op: mutate.SnapshotDestroy},
	}
	m, _ := NewMenu("tank/a", offers, nil, nil)
	for _, want := range []string{
		"Confirm with y; some choices ask for y, then enter.",
		"Confirm by typing the name.",
		"Confirm with y, then enter; some choices ask for typing the name.",
	} {
		out := render(m)
		if !strings.Contains(out, want) {
			t.Errorf("menu lacks %q:\n%s", want, out)
		}
		if strings.Contains(out, "tier") {
			t.Error("the menu speaks of tiers")
		}
		m.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	}
}

// What cannot be done now is listed apart, after what can, with the
// reason beside each.
func TestMenuListsWhatIsNotPossibleLast(t *testing.T) {
	offers := []Offer{
		{Key: "p", Name: "Pause the scrub", Disabled: "no scrub is running"},
		{Key: "t", Name: "Trim", Op: mutate.TrimStart},
	}
	m, _ := NewMenu("tank", offers, nil, nil)
	out := render(m)
	trim, head, pause := strings.Index(out, "t  Trim"), strings.Index(out, "Unavailable"), strings.Index(out, "no scrub is running")
	if trim < 0 || head < trim || pause < head {
		t.Fatalf("order wrong:\n%s", out)
	}
	if !strings.Contains(out, "> t  Trim") {
		t.Errorf("the cursor does not start on the first possible change:\n%s", out)
	}
}

func TestBatchMenuOffersOnlyWhatAppliesToSeveral(t *testing.T) {
	title, list := ZFSOffers(plan.Builder{}, time.Now)(ui.Target{Backend: "zfs", Kind: "snapshot", Names: []string{"tank/a@1", "tank/a@2"}})
	var keys []string
	for _, o := range list {
		keys = append(keys, o.Key)
	}
	if title != "2 snapshots" || strings.Join(keys, "") != "hud" {
		t.Fatalf("title %q, offers %q", title, keys)
	}
	title, list = ZFSOffers(plan.Builder{}, time.Now)(ui.Target{Backend: "zfs", Kind: "filesystem", Names: []string{"tank/a", "tank/b"}})
	if title != "2 datasets" || len(list) != 1 || list[0].Op != mutate.SnapshotCreate {
		t.Fatalf("datasets: %q, %+v", title, list)
	}
}

// A long reason wraps in its own column instead of being cut off.
func TestReasonLinesWrap(t *testing.T) {
	lines := reasonLines(lipgloss.NewStyle(), "  o  Bring online  ", "/dev/disk/by-id/ata-DEMO_TANK_B is already online", 50)
	if len(lines) < 2 || !strings.HasSuffix(strings.TrimSpace(lines[len(lines)-1]), "online") {
		t.Fatalf("lines %q", lines)
	}
	for _, l := range lines {
		if lipgloss.Width(l) > 50 {
			t.Errorf("%q is wider than 50", l)
		}
	}
	if !strings.HasPrefix(lines[1], strings.Repeat(" ", lipgloss.Width("  o  Bring online  "))) {
		t.Errorf("continuation not under the reason: %q", lines[1])
	}
}

// The message for a view without changes names where changes are, briefly.
func TestNoChangesMessage(t *testing.T) {
	out := render((&Session{}).NoChanges("I/O", false))
	for _, want := range []string{"I/O has nothing to change", "Pools (1)", "ARC (5)", "press a"} {
		if !strings.Contains(out, want) {
			t.Errorf("lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "ROADMAP") || strings.Contains(out, "does not") {
		t.Errorf("stale wording:\n%s", out)
	}
}

// docs/keys.md lists every change menu letter with the menu's own name for
// it, so the reference cannot drift from the menus.
func TestKeysDocListsEveryOffer(t *testing.T) {
	doc, err := os.ReadFile("../../../docs/keys.md")
	if err != nil {
		t.Fatal(err)
	}
	offers := ZFSOffers(plan.Builder{}, time.Now)
	for _, tg := range []ui.Target{
		{Kind: "pool", Names: []string{"tank"}},
		{Kind: "pools", Names: []string{"pools"}},
		{Kind: "device", Names: []string{"/dev/sda"}, Parent: "tank"},
		{Kind: "filesystem", Names: []string{"tank/a"}},
		{Kind: "filesystem", Names: []string{"tank/a", "tank/b"}},
		{Kind: "volume", Names: []string{"tank/v"}},
		{Kind: "snapshot", Names: []string{"tank/a@1"}},
		{Kind: "snapshot", Names: []string{"tank/a@1", "tank/a@2"}},
		{Kind: "arc", Names: []string{"arc"}},
	} {
		tg.Backend = "zfs"
		_, list := offers(tg)
		for _, o := range list {
			if row := "| `" + o.Key + "` | " + o.Name + " |"; !strings.Contains(string(doc), row) {
				t.Errorf("docs/keys.md lacks %q (%s)", row, tg.Kind)
			}
		}
	}
}

// The Tune tab's selection carries the changes its findings point to.
func TestTuneOffers(t *testing.T) {
	names := []string{
		"tank",
		tune.Change{Kind: "property", Dataset: "tank/db", Name: "recordsize", Value: "16K"}.Encode(),
		tune.Change{Kind: "property", Dataset: "tank/media", Name: "compression", Value: "lz4"}.Encode(),
		tune.Change{Kind: "param", Name: "zfs_prefetch_disable", Value: "1"}.Encode(),
		tune.Change{Kind: "arc"}.Encode(),
		"not a change",
	}
	title, offers := ZFSOffers(plan.Builder{}, time.Now)(ui.Target{Backend: "zfs", Kind: "tune", Names: names})
	var got []string
	for _, o := range offers {
		got = append(got, o.Key+" "+o.Name)
	}
	want := "r Set recordsize=16K on tank/db|c Set compression=lz4 on tank/media|p Set zfs_prefetch_disable to 1 until reboot|s Set ARC limits now"
	if strings.Join(got, "|") != want || !strings.Contains(title, "tank") {
		t.Errorf("%q: %q", title, got)
	}
}
