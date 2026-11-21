// SPDX-License-Identifier: GPL-3.0-or-later

package notify

import (
	"os"
	"testing"
)

// TestLiveNtfy sends one message to a real ntfy server when
// FSTUI_TEST_NTFY_URL, _TOPIC, _USER and _PASSWORD are set, and is skipped
// otherwise. The credentials stay in the environment of the test run.
func TestLiveNtfy(t *testing.T) {
	url, topic := os.Getenv("FSTUI_TEST_NTFY_URL"), os.Getenv("FSTUI_TEST_NTFY_TOPIC")
	if url == "" || topic == "" {
		t.Skip("FSTUI_TEST_NTFY_URL and FSTUI_TEST_NTFY_TOPIC are not set")
	}
	ch := Channel{Name: "live", Type: "ntfy", URL: url, Topic: topic, User: os.Getenv("FSTUI_TEST_NTFY_USER"), Secret: os.Getenv("FSTUI_TEST_NTFY_PASSWORD")}
	m := Message{Title: "fstui test", Body: "A test message from fstui's notify package tests.", Level: Info}
	if err := Send(t.Context(), ch, m); err != nil {
		t.Fatal(err)
	}
}
