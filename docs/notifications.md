# Notifications

fstui tells you when a pool needs attention: a pool that is not ONLINE, a device that dropped out, data errors, a pool filling up, slow disks, an overdue scrub, and more. Messages go to ntfy, a webhook, Gotify, Pushover, Telegram, Discord, Slack, Matrix or email.

## Getting notified

In the Config view (`9`), the Notifications tab opens with the three steps, and marks the ones that are done:

1. **Where to send:** `a` adds a channel; `t` sends it a test message.
2. **What to send:** the Alerts tab. The usual alerts are on from the start; `space` turns one on or off, `enter` sets its pools, channels and thresholds.
3. **Something that sends:** `s` sets up the notification service, `fstui watch`, so nothing has to be run by hand.

Every change shows the file it writes as a diff first. The rest of this page is what those tabs write, for editing the files directly.

## How messages are sent

Nothing is sent until something on the system runs the checks. The usual way is `fstui watch` as a service (systemd, or OpenRC's supervise-daemon): it checks every pool every `notifications.watch_interval` (5 minutes) and sends a new kernel event within `notifications.watch_events` (15 seconds). Where a service is not wanted, a timer or cron job runs `fstui check`, which checks once and exits. The Schedules tab of view 9 writes either, and the Notifications tab says which one runs, or that nothing does. Both share what was sent, so switching from one to the other sends nothing twice. Run either as root to see kernel events and pool history. Neither changes anything on the pools. `fstui watch` reads the configuration again for every check, checks at once on SIGHUP, logs only checks that sent or failed, and refuses to start while another `fstui watch` of the same user runs.

Other machines can be checked from this one over SSH: `notifications.hosts` lists them by name ([remote.md](remote.md#notifications)).

A monitor on the host cannot tell you that the host itself went down. For that, set `notifications.heartbeat_url` to a Healthchecks check or an Uptime Kuma push monitor: every check that read the pools requests it, and the service warns you when the requests stop.

## Channels

```yaml
notifications:
  repeat: 24h          # send an open problem again after this; 0 never repeats
  resolved: true       # send a message when a problem clears
  events: auto         # kernel events: auto (left to ZED when ZED sends them), on, off
  events_min: error    # least severe kernel event sent: info, notice, warn, error
  channels:
    - name: phone      # lower-case letters, digits, - and _
      type: ntfy
      url: https://ntfy.example.org
      topic: zfs
      user: alice      # with the secret as password; without user, the secret is a token
      min_severity: warning   # info, warning, error
```

What each type needs:

| Type | Settings | Secret |
|---|---|---|
| `ntfy` | `url`, `topic`, `user` (optional) | password, or access token |
| `webhook` | `url` | optional bearer token |
| `gotify` | `url` | application token |
| `pushover` | `user_key` | application token |
| `telegram` | `chat_id` | bot token |
| `discord`, `slack` | none | the webhook URL |
| `matrix` | `url` (the homeserver), `room` (`!id:server`) | access token |
| `smtp` | `host`, `port`, `from`, `to` (one or a list), `user` (optional) | password |

Every channel also takes `ca_file` (a PEM certificate authority, for a server with a private certificate), `insecure_http: true` (allow an `http://` URL) and `min_severity`.

- URLs must be `https://` unless `insecure_http` is set. Certificates are always verified. The proxy environment variables are not used, and redirects are not followed.
- SMTP uses TLS from the start on port 465. On any other port the server must offer STARTTLS; fstui does not send mail, or a password, unencrypted.
- A problem is sent when it appears or its state changes, again after `repeat`, and once when it clears. Each channel gets one message per run listing what changed. A channel that fails is tried again on the next run.
- Kernel events are sent once each. The first run sends none of the events the kernel already holds.
- Timeouts, retries, the host name in messages and how long one run may take are `notifications.*` settings in the [settings reference](configuration.md#reference).

## Secrets


Passwords and tokens go in `secrets.yaml` next to `config.yaml` (for root also `/etc/fstui/secrets.yaml`), by channel name:

```yaml
phone: the password or token
```

The file must belong to the user fstui runs as, with mode 0600; otherwise fstui ignores it and says why. A channel can instead name a file of its own with `secret_file`, under the same rule. Secrets never appear in `config.yaml`, the audit log, saved plans or messages.

`fstui check --test NAME` sends a test message to one channel.

## Alerts


In the Alerts tab every kind of alert is a line to tick: "A pool is filling up", "A device is offline", "Reads are slow". In the file, each kind is a template: a measure of a pool, a comparison, a threshold, a severity and the message it sends. An entry under `alerts:` turns a template on for some pools and channels, or all of them, and can change its threshold, severity, messages, how often it repeats and whether it says when the condition clears. Your own alerts, added with `a` in the Alerts tab, are templates under `templates:`.

```yaml
notifications:
  templates:                       # your own; one named like a built-in template replaces it
    - name: nearly-full
      description: Less than a terabyte free
      metric: free
      op: "<"
      value: 1T
      error_value: 200G            # past this it is an error
      severity: warning
      text: "{pool} has {value} free"
      resolved_text: "{pool} has room again"
  alerts:
    - template: pool-unhealthy     # every pool, every channel
    - template: capacity
      pools: [tank, vault]
      channels: [phone]
      value: "70"
      error_value: "85"
    - template: nearly-full
      pools: backup
      repeat: 12h                  # this alert only; 0 sends once
      resolved: false
```

Without `alerts:`, fstui sends what it sent before alerts existed: `pool-unhealthy`, `data-errors`, `capacity`, `fragmentation`, `scrub-overdue` and `checkpoint-held` at the [thresholds](configuration.md#thresholds), `version-mismatch`, and `kernel-events` at `events_min`. Once the file lists alerts, only those are sent. The Alerts tab shows the defaults ticked and writes them out with your first change.

The built-in templates:

| Template | Sent when | Severity |
|---|---|---|
| `pool-unhealthy` | the pool is not ONLINE | error |
| `device-down` | a device is offline, faulted, unavailable or removed | error |
| `device-errors` | devices count read, write or checksum errors | warning |
| `data-errors` | files have permanent data errors | error |
| `capacity` | the pool is 80% full; an error at 90% | warning |
| `low-free-space` | less than 100 GiB is free | warning |
| `fragmentation` | free space is 50% fragmented | warning |
| `scrub-overdue` | the last scrub finished more than 35 days ago | warning |
| `resilvering` | a resilver runs | info |
| `checkpoint-held` | a checkpoint is older than 24h | warning |
| `slow-reads`, `slow-writes` | reads or writes wait more than 50 ms on average | warning |
| `version-mismatch` | the userland and kernel module versions differ | warning |
| `kernel-events` | the kernel reports an event of `error` or worse | the event's |

The measures a template can compare, and how its `value` is written:

| `metric` | Value |
|---|---|
| `capacity`, `fragmentation` | a percentage: `80` |
| `free` | a size: `500G`, `2T` (powers of 1024) |
| `health` | a state: `ONLINE`, `DEGRADED`, compared with `==` or `!=` |
| `devices_down`, `device_errors`, `data_errors` | a count |
| `resilvering`, `versions_differ` | 1 or 0 |
| `scrub_age` | days |
| `checkpoint_age` | a duration: `24h` |
| `read_wait`, `write_wait` | milliseconds, averaged over one second of `zpool iostat`, read only when an alert uses them |
| `events` | `info`, `notice`, `warn` or `error`, with `>=` |

`op` is one of `>`, `>=`, `<`, `<=`, `==`, `!=`. A message can include `{pool}`, `{value}` (as measured, with its unit), `{threshold}`, `{metric}`, `{severity}` and `{detail}` (what fstui adds, such as `: 1 device OFFLINE`). A pool the backend cannot measure something of is never sent an alert about it.
