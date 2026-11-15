// SPDX-License-Identifier: GPL-3.0-or-later

package zfs

import "strings"

// Effect says what changing a property does to data already written. Only
// statements made by zfsprops(7) and zpoolprops(7) for OpenZFS 2.3.9 are
// recorded; Unspecified means the man page says nothing either way.
type Effect int

// Effects.
const (
	Unspecified Effect = iota
	// NewDataOnly: blocks written before the change keep the old setting.
	NewDataOnly
	// CreationOnly: fixed when the dataset is created.
	CreationOnly
	// Computed: reported by ZFS, not set by anyone.
	Computed
)

// Help is the inline help for one property: a short description, the
// valid values and the effect on existing data. The text is fstui's own
// wording, checked against the installed man pages; the man pages are
// CDDL-licensed and are not copied.
type Help struct {
	Summary string
	Values  string // valid values, as the man page's synopsis lists them
	Effect  Effect
}

// Text describes an Effect for the UI.
func (e Effect) Text() string {
	switch e {
	case NewDataOnly:
		return "Changing it affects only data written afterwards."
	case CreationOnly:
		return "Set when the dataset is created; it cannot be changed later."
	case Computed:
		return "Reported by ZFS; it cannot be set."
	case Unspecified:
	}
	return ""
}

// DatasetHelp returns help for a dataset property. User properties
// (module:property) have none.
func DatasetHelp(prop string) (Help, bool) {
	h, ok := datasetHelp[prop]
	return h, ok
}

// PoolHelp returns help for a pool property, including feature@ flags.
func PoolHelp(prop string) (Help, bool) {
	if strings.HasPrefix(prop, "feature@") {
		return Help{
			Summary: "Pool feature flag: disabled, enabled (allowed but unused) or active (in use on disk). zpool-features(7) describes " +
				strings.TrimPrefix(prop, "feature@") + ".",
			Values: "disabled | enabled | active",
		}, true
	}
	h, ok := poolHelp[prop]
	return h, ok
}

var datasetHelp = map[string]Help{
	// Space, all computed.
	"used":                 {"Space used by the dataset and everything below it: data, snapshots, children, reservations.", "", Computed},
	"available":            {"Space the dataset and its children can still use, after quotas, reservations and other datasets.", "", Computed},
	"referenced":           {"Data reachable from this dataset, which may be shared with snapshots and clones.", "", Computed},
	"logicalused":          {"Like used, before compression.", "", Computed},
	"logicalreferenced":    {"Like referenced, before compression.", "", Computed},
	"usedbydataset":        {"Space freed if the dataset itself were destroyed (after its snapshots and refreservation).", "", Computed},
	"usedbysnapshots":      {"Space held only by this dataset's snapshots; freed by destroying all of them.", "", Computed},
	"usedbychildren":       {"Space used by datasets below this one.", "", Computed},
	"usedbyrefreservation": {"Space set aside by refreservation and not yet written.", "", Computed},
	"written":              {"Data written since the most recent snapshot: what a new snapshot would hold.", "", Computed},
	"compressratio":        {"Compression achieved on used space, as a ratio.", "", Computed},
	"refcompressratio":     {"Compression achieved on referenced data, as a ratio.", "", Computed},

	// Identity and state, computed.
	"type":              {"filesystem, volume, snapshot or bookmark.", "", Computed},
	"creation":          {"When the dataset was created.", "", Computed},
	"createtxg":         {"Transaction group in which the dataset was created; orders snapshots exactly.", "", Computed},
	"guid":              {"Unique identifier; kept by send and receive.", "", Computed},
	"objsetid":          {"Internal object set number.", "", Computed},
	"mounted":           {"Whether the filesystem is mounted now.", "", Computed},
	"origin":            {"For a clone, the snapshot it was created from.", "", Computed},
	"clones":            {"For a snapshot, the clones created from it; they must go before it can be destroyed.", "", Computed},
	"userrefs":          {"For a snapshot, the number of holds; a held snapshot cannot be destroyed.", "", Computed},
	"defer_destroy":     {"For a snapshot, whether it is marked for destruction once its holds and clones are gone.", "", Computed},
	"filesystem_count":  {"Filesystems below this one, counted against filesystem_limit.", "", Computed},
	"snapshot_count":    {"Snapshots of this dataset and below, counted against snapshot_limit.", "", Computed},
	"snapshots_changed": {"When a snapshot of this dataset was last created or destroyed.", "", Computed},
	"keystatus":         {"Whether the encryption key is loaded (available) or not (unavailable).", "", Computed},
	"encryptionroot":    {"The dataset whose key unlocks this one.", "", Computed},

	// Data layout.
	"recordsize":           {"Largest block size for files. Matters for databases with fixed record sizes; the default suits most data.", "power of two, 512 B to 128 KiB (up to 16 MiB with large_blocks)", NewDataOnly},
	"volblocksize":         {"Block size of a volume.", "size; cannot change once the volume has been written", CreationOnly},
	"compression":          {"Compression algorithm for data written to the dataset.", "on | off | gzip | gzip-N | lz4 | lzjb | zle | zstd | zstd-N | zstd-fast | zstd-fast-N", NewDataOnly},
	"checksum":             {"Checksum used to verify data. off disables integrity checking and is not recommended.", "on | off | fletcher2 | fletcher4 | sha256 | noparity | sha512 | skein | edonr | blake3", NewDataOnly},
	"copies":               {"Extra copies of each block, on top of pool redundancy; counted against quotas.", "1 | 2 | 3", NewDataOnly},
	"dedup":                {"Deduplication of identical blocks. Costs memory and write speed.", "off | on | verify | sha256[,verify] | sha512[,verify] | skein[,verify] | edonr,verify | blake3[,verify]", Unspecified},
	"dnodesize":            {"Size of file metadata nodes; larger ones hold more extended attributes inline.", "legacy | auto | 1k | 2k | 4k | 8k | 16k", Unspecified},
	"special_small_blocks": {"Blocks up to this size go to the pool's special vdev; needs a special vdev.", "0 or a power of two from 512 B to 1 MiB", Unspecified},
	"redundant_metadata":   {"How many extra copies of metadata are kept.", "all | most | some | none", Unspecified},
	"logbias":              {"Whether synchronous writes favour latency (use the log device) or throughput.", "latency | throughput", Unspecified},
	"sync":                 {"How synchronous write requests are honoured. disabled can lose recent writes on a crash.", "standard | always | disabled", Unspecified},
	"primarycache":         {"What the ARC caches for this dataset.", "all | none | metadata", Unspecified},
	"secondarycache":       {"What the L2ARC caches for this dataset.", "all | none | metadata", Unspecified},
	"prefetch":             {"What speculative prefetch reads ahead.", "all | none | metadata", Unspecified},
	"direct":               {"How O_DIRECT requests are handled.", "disabled | standard | always", Unspecified},
	"volthreading":         {"Whether volume I/O is processed in parallel.", "on | off", Unspecified},
	"volmode":              {"How a volume is exposed as a device.", "default | full | geom | dev | none", Unspecified},
	"volsize":              {"Logical size of a volume; changes adjust its reservation or refreservation to match.", "size, a non-zero multiple of volblocksize", Unspecified},

	// Space limits.
	"quota":            {"Limit on space used by the dataset and everything below it, snapshots included.", "size | none", Unspecified},
	"refquota":         {"Limit on space referenced by the dataset itself, not counting snapshots and children.", "size | none", Unspecified},
	"reservation":      {"Space guaranteed to the dataset and everything below it.", "size | none", Unspecified},
	"refreservation":   {"Space guaranteed to the dataset itself; a thick volume has one equal to its size.", "size | none | auto", Unspecified},
	"filesystem_limit": {"Limit on filesystems below this one.", "count | none", Unspecified},
	"snapshot_limit":   {"Limit on snapshots of this dataset and below.", "count | none", Unspecified},

	// Mounting and access.
	"mountpoint":      {"Where the filesystem is mounted; legacy leaves it to fstab or mount(8).", "path | none | legacy", Unspecified},
	"canmount":        {"Whether the filesystem can be mounted; noauto mounts only on request.", "on | off | noauto", Unspecified},
	"readonly":        {"Whether the dataset can be modified.", "on | off", Unspecified},
	"atime":           {"Whether reading a file updates its access time.", "on | off", Unspecified},
	"relatime":        {"With atime on, update an access time only if it is older than the last modify or change, or a day old.", "on | off", Unspecified},
	"exec":            {"Whether programs on the filesystem can be executed.", "on | off", Unspecified},
	"setuid":          {"Whether setuid bits are honoured.", "on | off", Unspecified},
	"devices":         {"Whether device files can be opened.", "on | off", Unspecified},
	"overlay":         {"Whether mounting over a non-empty directory is allowed.", "on | off", Unspecified},
	"nbmand":          {"Non-blocking mandatory locks, used for SMB.", "on | off", Unspecified},
	"xattr":           {"How extended attributes are stored; sa keeps them with the file's metadata.", "on | off | dir | sa", Unspecified},
	"acltype":         {"Which ACLs the filesystem supports.", "off | nfsv4 | posix", Unspecified},
	"aclinherit":      {"How ACL entries are inherited by new files and directories.", "discard | noallow | restricted | passthrough | passthrough-x", Unspecified},
	"aclmode":         {"How ACLs change when chmod is used.", "discard | groupmask | passthrough | restricted", Unspecified},
	"snapdir":         {"Whether the .zfs directory with snapshots is shown at the filesystem root.", "disabled | hidden | visible", Unspecified},
	"snapdev":         {"Whether a volume's snapshots appear as devices.", "hidden | visible", Unspecified},
	"sharenfs":        {"NFS export of the filesystem and its options.", "on | off | options", Unspecified},
	"sharesmb":        {"SMB share of the filesystem.", "on | off | options", Unspecified},
	"zoned":           {"Whether the dataset is managed from a Linux namespace.", "off | on", Unspecified},
	"vscan":           {"Virus scanning on open and close; not used by OpenZFS.", "on | off", Unspecified},
	"context":         {"SELinux context for the whole filesystem.", "none | SELinux context", Unspecified},
	"fscontext":       {"SELinux context for the filesystem itself.", "none | SELinux context", Unspecified},
	"defcontext":      {"SELinux context for unlabelled files.", "none | SELinux context", Unspecified},
	"rootcontext":     {"SELinux context for the root directory.", "none | SELinux context", Unspecified},
	"version":         {"On-disk filesystem version; upgrading is one-way.", "N | current", Unspecified},
	"casesensitivity": {"Whether file names are case-sensitive.", "sensitive | insensitive | mixed", CreationOnly},
	"normalization":   {"Unicode normalization applied to file names.", "none | formC | formD | formKC | formKD", CreationOnly},
	"utf8only":        {"Whether file names must be valid UTF-8.", "on | off", CreationOnly},

	// Encryption.
	"encryption":  {"Cipher suite. on means aes-256-gcm on OpenZFS 2.3.", "off | on | aes-128-ccm | aes-192-ccm | aes-256-ccm | aes-128-gcm | aes-192-gcm | aes-256-gcm", CreationOnly},
	"keyformat":   {"How the key is given; can be changed with zfs change-key.", "raw | hex | passphrase", Unspecified},
	"keylocation": {"Where zfs load-key finds the key.", "prompt | file:///path | https://address | http://address", Unspecified},
	"pbkdf2iters": {"PBKDF2 rounds applied to a passphrase key.", "iterations", Unspecified},
}

var poolHelp = map[string]Help{
	"size":              {"Raw capacity of the pool's devices, before redundancy.", "", Computed},
	"allocated":         {"Space allocated on the devices, including redundancy.", "", Computed},
	"free":              {"size minus allocated. Overstates what filesystems can write: see a dataset's available.", "", Computed},
	"capacity":          {"allocated as a percentage of size, as zpool list shows it. It counts parity and space ZFS keeps back, so fstui's capacity bars, which measure what datasets can still write (used against available), read higher.", "", Computed},
	"freeing":           {"Space still being freed after destroys; it is returned in the background.", "", Computed},
	"leaked":            {"Space lost to errors while freeing; should be 0.", "", Computed},
	"fragmentation":     {"How fragmented the free space is; high values slow writes. Not file fragmentation.", "", Computed},
	"expandsize":        {"Space the pool could grow into if its devices were expanded.", "", Computed},
	"checkpoint":        {"Space held by the pool checkpoint, if one exists.", "", Computed},
	"dedupratio":        {"Deduplication achieved, as a ratio.", "", Computed},
	"dedup_table_size":  {"Size of the deduplication table.", "", Computed},
	"bcloneused":        {"Space referenced by cloned blocks (block cloning).", "", Computed},
	"bclonesaved":       {"Space saved by block cloning.", "", Computed},
	"bcloneratio":       {"Block cloning savings, as a ratio.", "", Computed},
	"health":            {"ONLINE, DEGRADED, FAULTED, OFFLINE, REMOVED or UNAVAIL.", "", Computed},
	"guid":              {"Unique identifier of the pool.", "", Computed},
	"load_guid":         {"Identifier that changes each time the pool is imported.", "", Computed},
	"last_scrubbed_txg": {"Transaction group the last scrub reached; zpool scrub -C continues from it.", "", Computed},
	"ashift":            {"Sector size as a power of two, used for new vdevs. 0 detects it from the devices.", "0, or 9 to 16", Unspecified},
	"altroot":           {"Directory prefixed to every mountpoint, for rescue and temporary imports.", "path", Unspecified},
	"autoexpand":        {"Grow the pool automatically when its devices grow.", "on | off", Unspecified},
	"autoreplace":       {"Use a new device found in the same physical location as a failed one.", "on | off", Unspecified},
	"autotrim":          {"Trim freed space on devices automatically.", "on | off", Unspecified},
	"bootfs":            {"Default dataset to boot from.", "pool[/dataset] or unset", Unspecified},
	"cachefile":         {"Where the pool's configuration is cached for import at boot; none keeps it out.", "path | none", Unspecified},
	"comment":           {"Free text, printable characters only.", "text", Unspecified},
	"compatibility":     {"Restricts features to sets that older systems or bootloaders support.", "off | legacy | file[,file]...", Unspecified},
	"dedup_table_quota": {"Limit on the size of the deduplication table.", "number | none | auto", Unspecified},
	"delegation":        {"Whether zfs allow grants permissions to non-root users.", "on | off", Unspecified},
	"failmode":          {"What happens when the pool loses its devices: wait for them, return errors, or panic.", "wait | continue | panic", Unspecified},
	"listsnapshots":     {"Whether zfs list shows snapshots by default.", "on | off", Unspecified},
	"multihost":         {"Guard against importing the pool on two hosts at once.", "on | off", Unspecified},
	"readonly":          {"Whether the pool was imported read-only.", "on | off", Unspecified},
	"version":           {"Legacy on-disk version; it can be raised, never lowered. Pools using feature flags have none.", "version", Unspecified},
}
