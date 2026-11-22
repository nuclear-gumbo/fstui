# Proxmox VE 9.2 recordings

Read from one node of a four-node cluster (pve-manager 9.2.32, OpenZFS 2.4.4) on 2026-11-22, with `cat`:

- `members.json`: `/etc/pve/.members`, written by pmxcfs.
- `storage.cfg`: `/etc/pve/storage.cfg`, the cluster's storage definitions.

Names and addresses are replaced, consistently in both files: node and cluster names, addresses (moved to 192.0.2.0/24, reserved for documentation), the Proxmox Backup Server fingerprint and user name, and one datastore name. Everything else, layout included (tabs, order, blank lines), is as recorded. Do not edit these files by hand.
