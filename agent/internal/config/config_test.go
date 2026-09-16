package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ernestdefoe/garrison/internal/driver"
	"github.com/ernestdefoe/garrison/internal/provision"
)

const base = `{
  "forumUrl": "https://forum.example/api/garrison/agent/poll",
  "token": "1.secret-token-nobody-should-see",
  "servers": [
    {"id": "valheim", "name": "Shattered Pact", "driver": "process", "command": "./start.sh", "dir": "/srv/valheim"}
  ],
  "templates": [
    {"id": "valheim", "label": "Valheim", "driver": "process", "installRoot": "/srv/garrison", "command": "./start_server.sh", "steamApp": 896660}
  ]
}
`

func write(t *testing.T, body string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "agent.json")

	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	return path
}

func TestAddPersistsANewServer(t *testing.T) {
	path := write(t, base)

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}

	tpl, err := provision.Find(cfg.Templates, "valheim")
	if err != nil {
		t.Fatal(err)
	}

	server, err := tpl.Server("valheim-2", "Second world")
	if err != nil {
		t.Fatal(err)
	}

	if err := cfg.Add(server); err != nil {
		t.Fatal(err)
	}

	// 🚨 Re-LOADED from disk, not read back from memory. The point of Add is
	// that the new server survives a restart, and only the file can say so.
	again, err := Load(path)
	if err != nil {
		t.Fatalf("the config no longer loads after being written: %v", err)
	}

	if len(again.Servers) != 2 {
		t.Fatalf("got %d servers after adding one to one", len(again.Servers))
	}

	if again.Servers[1].ID != "valheim-2" || again.Servers[1].Dir != "/srv/garrison/valheim-2" {
		t.Fatalf("the persisted server is wrong: %+v", again.Servers[1])
	}

	// 🚨 And everything else survived. A rewrite that dropped the token, or a
	// template, would take the host off the forum on its next restart.
	if again.Token != "1.secret-token-nobody-should-see" {
		t.Fatal("the token did not survive the rewrite")
	}

	if len(again.Templates) != 1 {
		t.Fatal("the templates did not survive the rewrite")
	}

	if again.Servers[0].Name != "Shattered Pact" {
		t.Fatal("the existing server was changed")
	}
}

// 🚨 This file holds the agent's token. A config that arrives at 0600 and is
// rewritten at whatever the umask felt like is a credential leak caused by a
// feature that has nothing to do with credentials.
func TestAddKeepsTheFilePermissions(t *testing.T) {
	path := write(t, base)

	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, _ := Load(path)
	tpl, _ := provision.Find(cfg.Templates, "valheim")
	server, _ := tpl.Server("valheim-2", "Second")

	if err := cfg.Add(server); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	if info.Mode().Perm() != 0o600 {
		t.Fatalf("permissions became %v, want 0600 — the token is in this file", info.Mode().Perm())
	}
}

func TestAddRefusesADuplicateId(t *testing.T) {
	path := write(t, base)

	cfg, _ := Load(path)
	tpl, _ := provision.Find(cfg.Templates, "valheim")
	server, _ := tpl.Server("valheim", "Clash")

	if err := cfg.Add(server); err == nil {
		t.Fatal("a server with an existing id was added")
	}

	again, _ := Load(path)

	if len(again.Servers) != 1 {
		t.Fatal("the file was changed anyway")
	}
}

/*
🚨 A config that would not load must never reach the disk.

The agent would fail to start on its next restart, taking every OTHER server on
the host with it — a provisioning mistake turning into a total outage, at
whatever hour the machine next reboots.
*/
func TestAConfigThatWouldNotLoadIsNeverWritten(t *testing.T) {
	path := write(t, base)

	cfg, _ := Load(path)

	// A server with no driver: exactly what Validate refuses.
	if err := cfg.Add(driver.Server{ID: "broken", Name: "Broken"}); err == nil {
		t.Fatal("an invalid server was added")
	}

	again, err := Load(path)
	if err != nil {
		t.Fatalf("the file on disk no longer loads: %v", err)
	}

	if len(again.Servers) != 1 {
		t.Fatalf("the file was changed: %d servers", len(again.Servers))
	}
}

func TestTemplatesAreValidated(t *testing.T) {
	cases := map[string]string{
		"no installRoot": `{"id":"t","driver":"process","command":"./x"}`,
		"no driver":      `{"id":"t","installRoot":"/srv"}`,
		"no command":     `{"id":"t","driver":"process","installRoot":"/srv"}`,
		"no id":          `{"driver":"process","installRoot":"/srv","command":"./x"}`,
	}

	for name, tpl := range cases {
		body := `{"forumUrl":"https://x/y","token":"t","servers":[],"templates":[` + tpl + `]}`

		if _, err := Load(write(t, body)); err == nil {
			t.Errorf("a template with %s was accepted", name)
		}
	}
}

func TestDuplicateTemplateIdsAreRefused(t *testing.T) {
	body := `{"forumUrl":"https://x/y","token":"t","servers":[],"templates":[
		{"id":"a","driver":"process","installRoot":"/srv","command":"./x"},
		{"id":"a","driver":"process","installRoot":"/srv","command":"./x"}
	]}`

	if _, err := Load(write(t, body)); err == nil {
		t.Fatal("duplicate template ids were accepted")
	}
}

// 🚨 A typo in a config key is otherwise silently ignored, and the operator
// spends an evening wondering why stopGrace (which should be
// stopGraceSeconds) does nothing.
func TestUnknownFieldsAreRefused(t *testing.T) {
	body := `{"forumUrl":"https://x/y","token":"t","servers":[],"nonsense":true}`

	if _, err := Load(write(t, body)); err == nil {
		t.Fatal("an unknown top-level field was accepted")
	}
}

// The written file must be readable JSON somebody can go and edit by hand,
// because that is how every other change to it is made.
func TestTheRewrittenFileStaysReadable(t *testing.T) {
	path := write(t, base)

	cfg, _ := Load(path)
	tpl, _ := provision.Find(cfg.Templates, "valheim")
	server, _ := tpl.Server("valheim-2", "Second")
	_ = cfg.Add(server)

	raw, _ := os.ReadFile(path)

	if !strings.Contains(string(raw), "\n  ") {
		t.Fatal("the rewritten config is not indented; an operator has to read this")
	}

	var probe map[string]any

	if err := json.Unmarshal(raw, &probe); err != nil {
		t.Fatalf("the rewritten config is not valid JSON: %v", err)
	}
}
