// SPDX-License-Identifier: GPL-3.0-or-later

package zfs

// ZvolDevice is the block device path of a volume. OpenZFS's udev rule
// (60-zvol.rules) links each zvol at /dev/zvol/<dataset name>.
func ZvolDevice(dataset string) string { return "/dev/zvol/" + dataset }
