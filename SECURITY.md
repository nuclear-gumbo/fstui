# Security

fstui is usually run as root on machines that hold important data. Report security problems privately.

## Reporting

Use GitHub's private vulnerability reporting: the **Report a vulnerability** button under the repository's Security tab. You can also send a direct message on Matrix to `@gumbo:goc.dev`. Do not open a public issue for a vulnerability.

Include the fstui version (`fstui --version`), what an attacker controls (for example a dataset name, a property value or a configuration file), what happens, and how to reproduce it. You will get an answer, and a fix will be released, credited to you if you wish, before the details are made public.

## Supported versions

Only the latest release. Fixes are released as new versions, not backported.

## Scope

In scope, for example:

- Text from ZFS (names, property values, hold tags, error output) reaching the terminal unsanitised: escape sequences, bidirectional text.
- fstui running a program other than the system's `zfs` and `zpool`, or passing them arguments it did not intend.
- A user other than root gaining privileges through fstui: its lock, audit log, saved plans, configuration or theme files.
- A change running without the confirmation it should ask for, or after the system changed since it was shown.
- A passphrase appearing on a command line, in the audit log, in a saved plan or in an error message.
- The demo build or its container reaching a real pool.
