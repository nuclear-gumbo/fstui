#!/bin/sh
# SPDX-License-Identifier: GPL-3.0-or-later
#
# Run the schedule planner for real under OpenRC and cron, inside
# throwaway rootless containers: the plans fstui makes are applied by its
# own pipeline, and the init system or cron daemon is checked to have
# taken them. The images are the pinned ones the distro workflow uses.
# The Debian container installs cron from Debian's mirror.
#
# Usage: scripts/test-schedules-container.sh   (as a user with rootless Podman)

set -eu

gentoo=docker.io/gentoo/stage3@sha256:549add81947a81b68f8ff83d78c0e5a6e876da429567aa0c6a32fc24d37ae578
debian=docker.io/library/debian:trixie@sha256:bfc36aecdece0387418759dba474aa856c026ca8087da3606b41b90ab0509c99

out=$(mktemp -d)
trap 'rm -rf "$out"' EXIT
CGO_ENABLED=0 go build -trimpath -o "$out/fstui" ./cmd/fstui
CGO_ENABLED=0 go test -c -trimpath -tags fstui_container -o "$out/schedule.test" ./internal/schedule

echo "== OpenRC (Gentoo stage3)"
podman run --rm --init --network=none -v "$out:/w:ro" "$gentoo" sh -euc '
	install -m 0755 /w/fstui /usr/bin/fstui
	mkdir -p /run/openrc && touch /run/openrc/softlevel
	/w/schedule.test -test.run "^TestContainerOpenRC" -test.v'

echo "== cron (Debian)"
podman run --rm --init -v "$out:/w:ro" "$debian" sh -euc '
	apt-get update -qq && apt-get install -y -qq --no-install-recommends cron >/dev/null
	install -m 0755 /w/fstui /usr/bin/fstui
	# A rootless container cannot set a login uid, and Debian'"'"'s cron
	# skips every job when pam_loginuid fails.
	sed -i "/pam_loginuid/s/^/#/" /etc/pam.d/cron
	cron -f -L 15 &
	# cron notices a changed /etc/cron.d by its mtime in whole seconds: a
	# file written in the second cron loaded would go unseen.
	sleep 2
	/w/schedule.test -test.run "^TestContainerCron" -test.v -test.timeout 5m'
