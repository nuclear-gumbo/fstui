// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !fstui_nowrite

package plan

import (
	"strings"
	"testing"
	"time"

	"github.com/nuclear-gumbo/fstui/internal/mutate"
	"github.com/nuclear-gumbo/fstui/internal/termtext"
	"github.com/nuclear-gumbo/fstui/internal/zfs"
)

var taken = time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)

// checkpointed is tank on two devices under /dev/disk/by-id, with a
// checkpoint taken at taken (or none), and a dataset and a snapshot
// created after it.
func checkpointed(with bool) *fakeReader {
	f := mirrorPool("mirror")
	st := f.topo["tank"]
	st.Data.Children[0].Children = []zfs.Vdev{
		{Type: "disk", Path: "/dev/disk/by-id/ata-x1-part1"}, {Type: "disk", Path: "/dev/disk/by-id/ata-x2-part1"},
	}
	if with {
		st.Checkpoint = &zfs.Checkpoint{State: "EXISTS", Taken: taken, Space: 3 << 30}
	}
	f.topo["tank"] = st
	obj := func(n string, at time.Time, mp string) zfs.Dataset {
		return zfs.Dataset{Name: n, Properties: zfs.Properties{
			"creation":   {Number: uint64(at.Unix()), IsNumber: true}, // a test time
			"mountpoint": {Text: termtext.New(mp)}, "mounted": {Text: termtext.New("yes")},
		}}
	}
	f.objects = map[string][]zfs.Dataset{"tank": {
		obj("tank", taken.Add(time.Minute), "/tank"), obj("tank/new", taken.Add(time.Hour), "/tank/new"),
		obj("tank@later", taken.Add(2*time.Hour), "-"),
	}}
	return f
}

func TestCheckpointTake(t *testing.T) {
	p, err := Builder{R: checkpointed(false)}.Checkpoint(t.Context(), mutate.CheckpointTake, "tank")
	if err != nil || len(p.Blocked) != 0 || p.Steps[0].String() != "zpool checkpoint -- tank" || mutate.TierOf(p) != mutate.Medium {
		t.Fatalf("plan %+v, %v", p, err)
	}
	p, _ = Builder{R: checkpointed(true)}.Checkpoint(t.Context(), mutate.CheckpointTake, "tank")
	if !strings.Contains(strings.Join(p.Blocked, ";"), "already") {
		t.Errorf("second checkpoint: %v", p.Blocked)
	}
	f := checkpointed(false)
	f.feature = "disabled"
	p, _ = Builder{R: f}.Checkpoint(t.Context(), mutate.CheckpointTake, "tank")
	if !strings.Contains(strings.Join(p.Blocked, ";"), "zpool_checkpoint feature of tank is disabled") {
		t.Errorf("feature disabled: %v", p.Blocked)
	}
}

func TestCheckpointDiscard(t *testing.T) {
	p, err := Builder{R: checkpointed(true)}.Checkpoint(t.Context(), mutate.CheckpointDiscard, "tank")
	if err != nil || len(p.Blocked) != 0 || p.Steps[0].String() != "zpool checkpoint -d -- tank" {
		t.Fatalf("plan %+v, %v", p, err)
	}
	if !strings.Contains(p.Preview.Affects[0].Raw(), "3 GiB") || len(p.Irreversible) != 1 {
		t.Errorf("affects %v, irreversible %v", p.Preview.Affects, p.Irreversible)
	}
	p, _ = Builder{R: checkpointed(false)}.Checkpoint(t.Context(), mutate.CheckpointDiscard, "tank")
	if !strings.Contains(strings.Join(p.Blocked, ";"), "tank has no checkpoint") {
		t.Errorf("none: %v", p.Blocked)
	}
}

func TestCheckpointRewind(t *testing.T) {
	p, err := Builder{R: checkpointed(true)}.Checkpoint(t.Context(), mutate.CheckpointRewind, "tank")
	if err != nil || len(p.Blocked) != 0 || mutate.TierOf(p) != mutate.High {
		t.Fatalf("plan %+v, %v", p, err)
	}
	if len(p.Steps) != 2 || p.Steps[0].String() != "zpool export -- tank" ||
		p.Steps[1].String() != "zpool import --rewind-to-checkpoint -d /dev/disk/by-id -- tank" {
		t.Errorf("steps %v", p.Steps)
	}
	var lost []string
	for _, d := range p.Preview.Destroys {
		lost = append(lost, d.Raw())
	}
	if strings.Join(lost, ",") != "tank/new,tank@later" {
		t.Errorf("lost %v", lost)
	}
}

// A pool holding / cannot be exported from the running system.
func TestCheckpointRewindRoot(t *testing.T) {
	f := checkpointed(true)
	root := f.objects["tank"][0]
	root.Properties["mountpoint"] = zfs.Property{Text: termtext.New("/")}
	p, err := Builder{R: f}.Checkpoint(t.Context(), mutate.CheckpointRewind, "tank")
	if err != nil || !strings.Contains(strings.Join(p.Blocked, ";"), "running system's root") {
		t.Errorf("blocked %v, %v", p.Blocked, err)
	}
}

func TestPoolAddCheckpointFeature(t *testing.T) {
	f := mirrorPool("mirror")
	f.feature = "disabled"
	p, err := creator(f, inventory()).PoolAdd(t.Context(), AddSpec{Pool: "tank", Log: []string{id("sdc")}, Checkpoint: true})
	if err != nil || !strings.Contains(strings.Join(p.Blocked, ";"), "zpool_checkpoint feature") {
		t.Errorf("blocked %v, %v", p.Blocked, err)
	}
}

// Looking exports the pool and imports it read-only at the checkpoint;
// while it is read-only, every checkpoint change says how to come back.
func TestCheckpointLook(t *testing.T) {
	p, err := Builder{R: checkpointed(true)}.Checkpoint(t.Context(), mutate.CheckpointLook, "tank")
	if err != nil || len(p.Blocked) != 0 || mutate.TierOf(p) != mutate.High || len(p.Irreversible) != 0 {
		t.Fatalf("plan %+v, %v", p, err)
	}
	if len(p.Steps) != 2 || p.Steps[1].String() != "zpool import -o readonly=on --rewind-to-checkpoint -d /dev/disk/by-id -- tank" {
		t.Errorf("steps %v", p.Steps)
	}
	f := checkpointed(false)
	f.poolProps = map[string]string{"readonly": "on"}
	for _, op := range []mutate.Op{mutate.CheckpointTake, mutate.CheckpointDiscard, mutate.CheckpointRewind, mutate.CheckpointLook} {
		p, _ := Builder{R: f}.Checkpoint(t.Context(), op, "tank")
		if !strings.Contains(blocked(p), "export it (x) and import it again (I)") {
			t.Errorf("%s on a read-only pool: %q", op, blocked(p))
		}
	}
}
