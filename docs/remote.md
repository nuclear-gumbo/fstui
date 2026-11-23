# Other hosts

fstui can show the pools of other machines over SSH, read-only: every view works as on the machine it runs on. A Proxmox VE cluster's nodes are found on their own. Changes on other hosts come in a later release.

## Setting up a host

1. Make sure `ssh HOST` logs in without a password prompt, with a key or through your SSH agent, and that the host's key is in your `known_hosts`. fstui never asks for a password, a key passphrase or a new host key; it uses the OpenSSH client with your `~/.ssh/config`, so host aliases, `User`, `Port`, `IdentityFile` and `ProxyJump` all apply.
2. Log in as root, or as a user with `zfs allow` permissions. fstui does not use `sudo`.
3. Name the host in `config.yaml`:

   ```yaml
   hosts:
     - name: pve1                 # what fstui shows and --host takes
       destination: root@pve1     # an alias from ~/.ssh/config, or user@host
   ```

Then `fstui --host pve1` opens it, or `H` inside fstui lists the hosts and switches to one. The status bar names the host and says `root over SSH`, or the user ID fstui has there.

## What fstui checks first

Before it reads a host, fstui checks that:

- the login shell passes arguments on unchanged (sh, bash, dash or zsh; csh and fish are refused);
- `zfs`, `zpool` and `env` are in `/usr/sbin`, `/usr/bin`, `/sbin` or `/bin`, owned by root and writable by no one else, as fstui requires locally;
- the OpenZFS release is 2.3 or newer.

Every command then runs by that absolute path with a fixed environment, and names are quoted so the host's shell cannot read them as anything else. A host whose key is unknown or has changed is refused, not added.

One SSH connection per host is shared by every read, and closed when fstui exits; `remote.persist` closes one left behind. `remote.connect_timeout` and `remote.alive_interval` bound connecting and a quiet connection ([reference](configuration.md#reference)).

## Proxmox VE

On a Proxmox VE node, run as root, `H` also lists the cluster's other nodes from `/etc/pve`, and reaches them the way the cluster does (by address, with the node's own known-hosts file). From a workstation, a listed host that is a Proxmox VE node adds the other nodes of its cluster to the list, reached as the same user by node name; your SSH configuration or DNS has to resolve those names.

## The dashboard

Add `hosts` to `dashboard.panels` for a panel with one line per host: its pools and their health, or why it could not be read, read every `refresh.hosts` (60 seconds) while the dashboard is shown.

## Notifications

`fstui check` and `fstui watch` look at the hosts in `notifications.hosts`; `local` is the machine they run on.

```yaml
notifications:
  hosts: [local, pve1, pve2]
```

Each host's messages name it ("pve1: tank is DEGRADED"), and what was sent is kept per host. A host that cannot be reached is sent once as a problem of its own, and again when it is back. The heartbeat URL is requested once per check, not per host. See [notifications.md](notifications.md).
