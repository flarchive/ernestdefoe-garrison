package settings

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const minecraftish = `#Minecraft server properties
#Mon Sep 15 12:00:00 UTC 2026

# The message shown in the server list.
motd=A Minecraft Server
max-players=20

#Leave this alone unless you know what you are doing
rcon.password=hunter2
level-seed=
`

func fixture(t *testing.T, body string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "server.properties")

	if err := os.WriteFile(path, []byte(body), 0o640); err != nil {
		t.Fatal(err)
	}

	return path
}

func TestReadsKeysValuesAndTheirComments(t *testing.T) {
	f := File{ID: "props", Path: fixture(t, minecraftish), Format: FormatProperties}

	set, err := Read(f)
	if err != nil {
		t.Fatal(err)
	}

	if len(set.Entries) != 4 {
		t.Fatalf("got %d entries, want 4: %+v", len(set.Entries), set.Entries)
	}

	motd := set.Entries[0]

	if motd.Key != "motd" || motd.Value != "A Minecraft Server" {
		t.Fatalf("first entry is %+v", motd)
	}

	// 🚨 The game's own explanation is usually the most useful thing on the
	// screen, and it is one line above the setting.
	if motd.Comment != "The message shown in the server list." {
		t.Fatalf("the comment above motd was not carried through: %q", motd.Comment)
	}

	// An empty value is a value, not an absent key.
	last := set.Entries[3]

	if last.Key != "level-seed" || last.Value != "" {
		t.Fatalf("an empty value was not read as one: %+v", last)
	}
}

/*
🚨 THE TEST THIS PACKAGE EXISTS FOR.

A game config is usually the game's own annotated template. Read-modify-write
from parsed values turns somebody's documented server.properties into a bare
list of keys, deleting every explanation in it — and there is no undo, because
the file IS the record.
*/
func TestWritingOneKeyPreservesEverythingElse(t *testing.T) {
	path := fixture(t, minecraftish)
	f := File{ID: "props", Path: path, Format: FormatProperties}

	if err := Write(f, "", "motd", "Shattered Pact"); err != nil {
		t.Fatal(err)
	}

	after, _ := os.ReadFile(path)
	got := string(after)

	for _, keep := range []string{
		"#Minecraft server properties",
		"# The message shown in the server list.",
		"#Leave this alone unless you know what you are doing",
		"rcon.password=hunter2",
		"level-seed=",
	} {
		if !strings.Contains(got, keep) {
			t.Errorf("the rewrite lost %q:\n%s", keep, got)
		}
	}

	if !strings.Contains(got, "motd=Shattered Pact") {
		t.Errorf("the value was not changed:\n%s", got)
	}

	if strings.Contains(got, "motd=A Minecraft Server") {
		t.Errorf("the old value is still there:\n%s", got)
	}

	// And the line count is unchanged: nothing was added or dropped.
	if strings.Count(got, "\n") != strings.Count(minecraftish, "\n") {
		t.Errorf("the file gained or lost lines:\n%s", got)
	}
}

// 🚨 The keys allowlist is a second gate, and it is the one that lets an
// operator give staff the message of the day without giving them the RCON
// password sitting three lines below it.
func TestKeysOutsideTheAllowlistCannotBeWritten(t *testing.T) {
	path := fixture(t, minecraftish)
	f := File{ID: "props", Path: path, Format: FormatProperties, Keys: []string{"motd", "max-players"}}

	if err := Write(f, "", "rcon.password", "owned"); err == nil {
		t.Fatal("a key outside the allowlist was written")
	}

	after, _ := os.ReadFile(path)

	if !strings.Contains(string(after), "rcon.password=hunter2") {
		t.Fatal("the file was changed anyway")
	}

	// And it is reported as not editable rather than hidden — an operator
	// should be able to SEE the setting exists.
	set, _ := Read(f)

	for _, e := range set.Entries {
		if e.Key == "rcon.password" && e.Editable {
			t.Fatal("rcon.password was offered as editable")
		}
		if e.Key == "motd" && !e.Editable {
			t.Fatal("motd was not offered as editable")
		}
	}
}

func TestAReadOnlyFileRefusesEveryWrite(t *testing.T) {
	path := fixture(t, minecraftish)
	f := File{ID: "props", Path: path, Format: FormatProperties, ReadOnly: true}

	if err := Write(f, "", "motd", "nope"); err == nil {
		t.Fatal("a read-only file was written")
	}

	set, _ := Read(f)

	for _, e := range set.Entries {
		if e.Editable {
			t.Fatalf("%q was offered as editable in a read-only file", e.Key)
		}
	}
}

/*
🚨 THE ONE THAT DEFEATS THE WHOLE ALLOWLIST WITH A TEXT FIELD.

One newline turns a single permitted setting into two lines, and the second is
whatever the sender wants — `rcon.password=x` appended to a file the operator
only allowed `motd` in. It is the first thing anybody would try.
*/
func TestAValueCannotSmuggleASecondSetting(t *testing.T) {
	path := fixture(t, minecraftish)
	f := File{ID: "props", Path: path, Format: FormatProperties, Keys: []string{"motd"}}

	for _, evil := range []string{
		"hello\nrcon.password=owned",
		"hello\r\nrcon.password=owned",
		"hello\rrcon.password=owned",
	} {
		if err := Write(f, "", "motd", evil); err == nil {
			t.Errorf("a value containing a line break was written: %q", evil)
		}
	}

	after, _ := os.ReadFile(path)

	if strings.Contains(string(after), "owned") {
		t.Fatalf("a second setting was smuggled in:\n%s", after)
	}
}

// 🚨 A typo must not append a setting the game ignores. The operator would see
// it saved, see no effect, and conclude the panel does not work.
func TestAnUnknownKeyIsRefusedRatherThanAppended(t *testing.T) {
	path := fixture(t, minecraftish)
	f := File{ID: "props", Path: path, Format: FormatProperties}

	if err := Write(f, "", "mtod", "typo"); err == nil {
		t.Fatal("an unknown key was accepted")
	}

	after, _ := os.ReadFile(path)

	if strings.Contains(string(after), "mtod") {
		t.Fatalf("the typo was appended to the file:\n%s", after)
	}
}

/*
🚨 The id is the ONLY way a path is produced, and this is the test that says so.
A file manager on a game host is arbitrary write access, and arbitrary write
access on a host that runs a start script is arbitrary code execution — reached
from a PHP forum on the public internet.
*/
func TestOnlyDeclaredFilesResolve(t *testing.T) {
	declared := []File{{ID: "props", Path: "/srv/game/server.properties", Format: FormatProperties}}

	for _, id := range []string{
		"../../etc/passwd",
		"/etc/passwd",
		"props/../../../etc/shadow",
		"PROPS",
		"",
		"server.properties",
	} {
		if _, err := Find(declared, id); err == nil {
			t.Errorf("%q resolved to a file", id)
		}
	}

	if _, err := Find(declared, "props"); err != nil {
		t.Fatalf("the declared file did not resolve: %v", err)
	}
}

const sectioned = `[Server]
# How many people can play
MaxPlayers=32
Name=Shattered Pact

[Admin]
Name=ernest
`

// 🚨 The same key in two sections must not be confused. Writing Server.Name
// must not touch Admin.Name, and a parser that matches on the key alone
// rewrites whichever comes first — silently renaming the wrong thing.
func TestSectionsAreNotConfused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "game.ini")

	if err := os.WriteFile(path, []byte(sectioned), 0o640); err != nil {
		t.Fatal(err)
	}

	f := File{ID: "ini", Path: path, Format: FormatINI}

	if err := Write(f, "Admin", "Name", "someone-else"); err != nil {
		t.Fatal(err)
	}

	after, _ := os.ReadFile(path)
	got := string(after)

	if !strings.Contains(got, "Name=Shattered Pact") {
		t.Errorf("the Server section's Name was changed:\n%s", got)
	}

	if !strings.Contains(got, "Name=someone-else") {
		t.Errorf("the Admin section's Name was not changed:\n%s", got)
	}

	set, _ := Read(f)

	var sections []string
	for _, e := range set.Entries {
		sections = append(sections, e.Section+"."+e.Key)
	}

	want := "Server.MaxPlayers Server.Name Admin.Name"

	if strings.Join(sections, " ") != want {
		t.Fatalf("entries are %v, want %q", sections, want)
	}
}

// The file's permissions must survive an edit: a config the game runs as
// another user has to stay readable by it, and a server that will not start
// after an edit is a very confusing bug to attribute.
func TestPermissionsSurviveAWrite(t *testing.T) {
	path := fixture(t, minecraftish)

	if err := os.Chmod(path, 0o664); err != nil {
		t.Fatal(err)
	}

	f := File{ID: "props", Path: path, Format: FormatProperties}

	if err := Write(f, "", "motd", "changed"); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	if info.Mode().Perm() != 0o664 {
		t.Fatalf("permissions became %v, want 0664", info.Mode().Perm())
	}
}
