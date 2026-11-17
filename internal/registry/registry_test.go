// SPDX-License-Identifier: GPL-3.0-or-later

package registry

import "testing"

func TestHasBackendNeedsABackendNotJustFeatures(t *testing.T) {
	var r Registry
	if r.HasBackend() {
		t.Fatal("empty registry reports a backend")
	}
	r.Add(Module{Name: "write", Kind: Feature})
	if r.HasBackend() {
		t.Fatal("a feature module alone must not count as a backend")
	}
	r.Add(Module{Name: "zfs", Kind: Backend})
	if !r.HasBackend() {
		t.Fatal("registry with zfs reports no backend")
	}
}

func TestSummaryKeepsRegistrationOrder(t *testing.T) {
	var r Registry
	if got := r.Summary(); got != "none" {
		t.Fatalf("empty Summary() = %q, want %q", got, "none")
	}
	r.Add(Module{Name: "zfs", Kind: Backend})
	r.Add(Module{Name: "write", Kind: Feature})
	if got, want := r.Summary(), "zfs, write"; got != want {
		t.Fatalf("Summary() = %q, want %q", got, want)
	}
}

func TestDuplicateRegistrationPanics(t *testing.T) {
	var r Registry
	r.Add(Module{Name: "zfs", Kind: Backend})
	defer func() {
		if recover() == nil {
			t.Fatal("registering zfs twice did not panic")
		}
	}()
	r.Add(Module{Name: "zfs", Kind: Backend})
}

func TestModulesReturnsACopy(t *testing.T) {
	var r Registry
	r.Add(Module{Name: "zfs", Kind: Backend})
	m := r.Modules()
	m[0].Name = "changed"
	if got := r.Modules()[0].Name; got != "zfs" {
		t.Fatalf("caller modified registry contents: got %q", got)
	}
}
