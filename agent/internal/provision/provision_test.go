package provision

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ernestdefoe/garrison/internal/settings"
)

func template() Template {
	return Template{
		ID:          "valheim",
		Label:       "Valheim dedicated server",
		Driver:      "process",
		Game:        "valheim",
		SteamApp:    896660,
		InstallRoot: "/srv/garrison",
		Command:     "./start_server.sh",
		BackupPaths: []string{"worlds"},
		Config: []settings.File{
			{ID: "cfg", Path: "server.cfg", Format: settings.FormatProperties},
		},
	}
}

/*
🚨 THE TEST THIS PACKAGE EXISTS FOR.

This is the first feature where the agent gains a server it did not have at
startup, which means it writes its own config — and that is exactly the surface
where "the operator decides what may run" could quietly become "the forum
decides what may run". Every one of these is a name a compromised forum would
send, and each must fail before it can become a path.
*/
func TestOnlyUsableServerIdsAreAccepted(t *testing.T) {
	bad := []string{
		"../../etc/cron.d/x",
		"/etc/passwd",
		"..",
		".",
		"",
		"-rf", // a leading dash becomes a command-line flag
		"has space",
		"UPPER",
		"trailing/",
		"a/b",
		"null\x00byte",
		strings.Repeat("a", 33),
	}

	for _, id := range bad {
		if ValidID(id) {
			t.Errorf("accepted %q as a server id", id)
		}

		if _, err := template().Server(id, "x"); err == nil {
			t.Errorf("built a server definition for the id %q", id)
		}
	}

	for _, id := range []string{"valheim", "valheim-2", "my_server", "a", "s1"} {
		if !ValidID(id) {
			t.Errorf("refused %q, which is a perfectly ordinary id", id)
		}
	}
}

// 🚨 Every path is composed from the TEMPLATE's root and a validated id, so a
// caller cannot name a directory even indirectly.
func TestPathsAreComposedFromTheTemplate(t *testing.T) {
	s, err := template().Server("valheim-2", "Second world")
	if err != nil {
		t.Fatal(err)
	}

	want := filepath.Join("/srv/garrison", "valheim-2")

	if s.Dir != want {
		t.Fatalf("Dir is %q, want %q", s.Dir, want)
	}

	if s.BackupRoot != want {
		t.Fatalf("BackupRoot is %q, want the server's own directory", s.BackupRoot)
	}

	if s.Command != "./start_server.sh" {
		t.Fatalf("Command is %q — it must come from the template", s.Command)
	}
}

/*
🚨 Config paths are rebased under the NEW server's directory.

A template declares `server.cfg` relative to wherever the game lands. Copying
the path verbatim would point every provisioned server at the same file, so
editing one would edit them all — and the second server an operator created
would silently share the first one's settings.
*/
func TestConfigPathsAreRebasedPerServer(t *testing.T) {
	first, _ := template().Server("one", "One")
	second, _ := template().Server("two", "Two")

	if first.Config[0].Path == second.Config[0].Path {
		t.Fatalf("both servers point at the same config file: %q", first.Config[0].Path)
	}

	if first.Config[0].Path != filepath.Join("/srv/garrison", "one", "server.cfg") {
		t.Fatalf("config path is %q", first.Config[0].Path)
	}
}

// An absolute path in a template is left alone: an operator who wrote one meant
// it, and rebasing it would produce a path that exists nowhere.
func TestAnAbsoluteConfigPathIsNotRebased(t *testing.T) {
	tpl := template()
	tpl.Config = []settings.File{{ID: "hosts", Path: "/etc/game/shared.cfg", Format: settings.FormatProperties}}

	s, _ := tpl.Server("one", "One")

	if s.Config[0].Path != "/etc/game/shared.cfg" {
		t.Fatalf("an absolute path was rewritten to %q", s.Config[0].Path)
	}
}

func TestAnUnknownTemplateIsRefused(t *testing.T) {
	if _, err := Find([]Template{template()}, "minecraft"); err == nil {
		t.Fatal("an undeclared template resolved")
	}

	if _, err := Find([]Template{template()}, "valheim"); err != nil {
		t.Fatalf("the declared template did not resolve: %v", err)
	}
}

/*
🚨 The picker must not leak where games live on disk.

InstallRoot and Command are the operator's business, and they are the fields an
attacker would most like to read: knowing where a game lives is the first half
of doing something about it.
*/
func TestTheTemplateListLeaksNoPaths(t *testing.T) {
	list := List([]Template{template()})

	if len(list) != 1 {
		t.Fatalf("listed %d templates", len(list))
	}

	if list[0].ID != "valheim" || list[0].Label != "Valheim dedicated server" || !list[0].FromSteam {
		t.Fatalf("the description is wrong: %+v", list[0])
	}

	// Structural, not textual: the type simply has no field for a path, which
	// is a stronger guarantee than checking that a string does not contain one.
	rendered := fmt.Sprintf("%+v", list[0])

	for _, secret := range []string{"/srv/garrison", "start_server.sh", "896660"} {
		if strings.Contains(rendered, secret) {
			t.Errorf("the description leaks %q: %s", secret, rendered)
		}
	}
}

func TestAnUnnamedServerFallsBackToItsId(t *testing.T) {
	s, err := template().Server("valheim-2", "   ")
	if err != nil {
		t.Fatal(err)
	}

	if s.Name != "valheim-2" {
		t.Fatalf("Name is %q, want the id", s.Name)
	}
}

func TestAnAbsurdNameIsRefused(t *testing.T) {
	if _, err := template().Server("ok", strings.Repeat("n", 65)); err == nil {
		t.Fatal("a 65-character name was accepted")
	}
}
