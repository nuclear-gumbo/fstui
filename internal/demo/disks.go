// SPDX-License-Identifier: GPL-3.0-or-later

//go:build fstui_demo

package demo

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/nuclear-gumbo/fstui/internal/execute"
)

// demoDisks is a made-up machine's lsblk -J -b output: four empty 8 TB
// disks for a new pool, one with an old pool's label, two NVMe drives
// for log and special devices, and the system disk.
const demoDisks = `{"blockdevices": [
 {"name":"sdh","path":"/dev/sdh","size":8001563222016,"type":"disk","fstype":null,"mountpoints":[null],"pkname":null,"model":"DEMO HDD 8TB","serial":"DEMO0H","wwn":"0x5000c500demo0h","rota":true,"ro":false},
 {"name":"sdi","path":"/dev/sdi","size":8001563222016,"type":"disk","fstype":null,"mountpoints":[null],"pkname":null,"model":"DEMO HDD 8TB","serial":"DEMO0I","wwn":"0x5000c500demo0i","rota":true,"ro":false},
 {"name":"sdj","path":"/dev/sdj","size":8001563222016,"type":"disk","fstype":null,"mountpoints":[null],"pkname":null,"model":"DEMO HDD 8TB","serial":"DEMO0J","wwn":"0x5000c500demo0j","rota":true,"ro":false},
 {"name":"sdk","path":"/dev/sdk","size":8001563222016,"type":"disk","fstype":null,"mountpoints":[null],"pkname":null,"model":"DEMO HDD 8TB","serial":"DEMO0K","wwn":"0x5000c500demo0k","rota":true,"ro":false},
 {"name":"sdl","path":"/dev/sdl","size":4000787030016,"type":"disk","fstype":"zfs_member","mountpoints":[null],"pkname":null,"model":"DEMO HDD 4TB","serial":"DEMO0L","wwn":"0x5000c500demo0l","rota":true,"ro":false},
 {"name":"nvme1n1","path":"/dev/nvme1n1","size":500107862016,"type":"disk","fstype":null,"mountpoints":[null],"pkname":null,"model":"DEMO NVMe 500GB","serial":"DEMONV1","wwn":null,"rota":false,"ro":false},
 {"name":"nvme2n1","path":"/dev/nvme2n1","size":500107862016,"type":"disk","fstype":null,"mountpoints":[null],"pkname":null,"model":"DEMO NVMe 500GB","serial":"DEMONV2","wwn":null,"rota":false,"ro":false},
 {"name":"nvme0n1","path":"/dev/nvme0n1","size":1000204886016,"type":"disk","fstype":null,"mountpoints":[null],"pkname":null,"model":"DEMO NVMe 1TB","serial":"DEMONV0","wwn":null,"rota":false,"ro":false,
  "children":[
   {"name":"nvme0n1p1","path":"/dev/nvme0n1p1","size":536870912,"type":"part","fstype":"vfat","mountpoints":["/boot/efi"],"pkname":"nvme0n1","model":null,"serial":null,"wwn":null,"rota":false,"ro":false},
   {"name":"nvme0n1p2","path":"/dev/nvme0n1p2","size":999667064832,"type":"part","fstype":"ext4","mountpoints":["/"],"pkname":"nvme0n1","model":null,"serial":null,"wwn":null,"rota":false,"ro":false}
  ]}
]}`

// demoImportable is what zpool import lists on the made-up machine: an
// old pool on two of its disks, one of them gone.
const demoImportable = `  pool: oldtank
    id: 4866379416960248110
 state: DEGRADED
status: One or more devices are missing from the system.
action: The pool can be imported despite missing or damaged devices.  The
	fault tolerance of the pool may be compromised if imported.
   see: https://openzfs.github.io/openzfs-docs/msg/ZFS-8000-2Q
config:

	oldtank                     DEGRADED
	  mirror-0                  DEGRADED
	    wwn-0x5000c500demo0l    ONLINE
	    wwn-0x5000c500demo0m    UNAVAIL  cannot open
`

// diskAnswer answers lsblk, zpool list of names, and zpool create -n, the
// reads pool creation makes. The dry run prints the layout from the
// arguments as zpool does.
func (c *Commands) diskAnswer(a execute.Argv) ([]byte, bool) {
	cmd := string(a.Tool) + " " + strings.Join(a.Options, " ")
	switch {
	case a.Tool == execute.Lsblk:
		return []byte(demoDisks), true
	case cmd == "zpool list -H -o name":
		return []byte(strings.Join(c.poolNames(), "\n") + "\n"), true
	case a.Tool == execute.Zpool && len(a.Options) > 0 && a.Options[0] == "import" && len(a.Operands) == 0:
		return c.importable(), true
	case strings.HasPrefix(cmd, "zpool create -n") && len(a.Operands) > 1:
		return layout("would create '%s' with the following layout:", a.Operands), true
	case cmd == "zpool remove -n" && len(a.Operands) == 2:
		// zpool prints the memory the removed vdev's mapping takes; the
		// demo's vdevs are small.
		return []byte("Memory that will be used after removing " + a.Operands[1] + ": 1.01K\n"), true
	case strings.HasPrefix(cmd, "zpool add -n") && len(a.Operands) > 1:
		// zpool prints the whole pool; the demo prints the new vdevs.
		return layout("would update '%s' to the following configuration (new vdevs):", a.Operands), true
	}
	return nil, false
}

// layout prints what zpool create -n and add -n print after head: the
// pool, then its vdevs indented, with log, cache, special and spare
// groups.
func layout(head string, ops []string) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, head+"\n\n\t%s\n", ops[0], ops[0])
	indent := "\t  "
	for _, op := range ops[1:] {
		switch op {
		case "mirror", "raidz1", "raidz2", "raidz3":
			fmt.Fprintf(&b, "%s%s\n", indent, op)
			indent = strings.TrimSuffix(indent, "  ") + "    "
		case "log", "cache", "special", "spare":
			word := map[string]string{"log": "logs", "cache": "cache", "special": "special", "spare": "spares"}[op]
			fmt.Fprintf(&b, "\t%s\n", word)
			indent = "\t  "
		default:
			fmt.Fprintf(&b, "%s%s\n", indent, op)
		}
	}
	return []byte(b.String())
}

// importable is zpool import's list: the pools exported in the demo, and
// the made-up machine's old pool until it is imported.
func (c *Commands) importable() []byte {
	c.mu.Lock()
	away := slices.Sorted(maps.Keys(c.away))
	old := !c.oldImported
	c.mu.Unlock()
	var b strings.Builder
	for _, pool := range away {
		p, err := c.currentStatus(pool)
		if err != nil {
			continue
		}
		state, _ := p["state"].(string)
		fmt.Fprintf(&b, "  pool: %s\n    id: %v\n state: %s\naction: The pool can be imported using its name or numeric identifier.\nconfig:\n\n", pool, p["pool_guid"], state)
		importTree(&b, p, 1)
		b.WriteString("\n")
	}
	if old {
		b.WriteString(demoImportable)
	}
	return []byte(b.String())
}

// importTree prints a pool's devices as zpool import does, indented by
// depth; the root vdev's children print under the pool.
func importTree(b *strings.Builder, v node, depth int) {
	fmt.Fprintf(b, "\t%s%-40s %s\n", strings.Repeat("  ", depth-1), str(v, "name"), str(v, "state"))
	for _, ch := range children(v) {
		if str(ch, "vdev_type") == "root" {
			for _, gc := range children(ch) {
				importTree(b, gc, depth+1)
			}
			continue
		}
		importTree(b, ch, depth+1)
	}
}

// children are the vdevs below v in every group, by name.
func children(v node) []node {
	var out []node
	for _, g := range groups {
		for _, k := range slices.Sorted(maps.Keys(kids(v, g))) {
			if m, ok := kids(v, g)[k].(node); ok {
				out = append(out, m)
			}
		}
	}
	return out
}
