// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !fstui_nowrite

package actions

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/nuclear-gumbo/fstui/internal/mutate"
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
	for _, kind := range []string{"filesystem", "volume", "snapshot", "pool", "device", "arc"} {
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
