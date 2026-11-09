package store

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestNodeDisplayNameIsIndependentFromAgentHostname(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	id, err := st.UpsertNodeByToken("token-hash", "workstation-01", "linux", "amd64", "0.7.0", "192.0.2.10", false, 0, 0, 0, "instance-01")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetNodeDisplayName(id, "Домашний сервер"); err != nil {
		t.Fatal(err)
	}

	// A reconnect/heartbeat updates agent facts but must not overwrite the admin label.
	id2, err := st.UpsertNodeByToken("token-hash", "workstation-01-renamed-by-os", "linux", "amd64", "0.7.1", "192.0.2.11", false, 0, 0, 0, "instance-01")
	if err != nil {
		t.Fatal(err)
	}
	if id2 != id {
		t.Fatalf("node id changed on reconnect: got %d want %d", id2, id)
	}

	n, err := st.NodeByID(id)
	if err != nil {
		t.Fatal(err)
	}
	if n.DisplayName != "Домашний сервер" {
		t.Fatalf("display name was overwritten: %q", n.DisplayName)
	}
	if n.Hostname != "workstation-01-renamed-by-os" {
		t.Fatalf("agent hostname was changed unexpectedly: %q", n.Hostname)
	}
	payload, err := json.Marshal(n)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(payload), "display_name") || strings.Contains(string(payload), "Домашний сервер") {
		t.Fatalf("admin-only label leaked into the public node JSON: %s", payload)
	}
}