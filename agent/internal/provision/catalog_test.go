package provision

import (
	"strings"
	"testing"
)

/*
🚨 A wrong app id in here is the most expensive mistake this package can make.

It downloads several gigabytes over the operator's connection, writes them to
the operator's disk, and only then fails to find a binary that was never in that
app to begin with. So the catalogue is checked for the properties that make an
entry installable at all, rather than left to be discovered one game at a time
by whoever tries it first.
*/
func TestEveryPresetCanActuallyBeInstalled(t *testing.T) {
	for _, p := range presets {
		if p.Game == "" {
			t.Errorf("a preset has no game key")
		}
		if p.Label == "" {
			t.Errorf("%s: no label, so the picker would show a blank row", p.Game)
		}
		if p.SteamApp <= 0 {
			t.Errorf("%s: no Steam app id — the installer downloads from Steam and nothing else, "+
				"so this template would look installable and then fail", p.Game)
		}
		if p.Command == "" {
			t.Errorf("%s: no command, and the process driver cannot start a server without one", p.Game)
		}
		if len(p.BackupPaths) == 0 {
			t.Errorf("%s: no backup paths; a server nobody can back up is worse than one nobody provisioned", p.Game)
		}
	}
}

/*
🚨 Backup paths must be RELATIVE and must not be the whole install.

An absolute path escapes the server's directory. `.` backs up the game binaries
too — gigabytes that SteamCMD can fetch again for free, making every backup slow
enough that an operator turns them off.
*/
func TestBackupPathsAreRelativeAndNarrow(t *testing.T) {
	for _, p := range presets {
		for _, path := range p.BackupPaths {
			if strings.HasPrefix(path, "/") {
				t.Errorf("%s: backup path %q is absolute", p.Game, path)
			}
			if strings.Contains(path, "..") {
				t.Errorf("%s: backup path %q climbs out of the server directory", p.Game, path)
			}
			if path == "." || path == "" {
				t.Errorf("%s: backup path %q is the entire install", p.Game, path)
			}
		}
	}
}

func TestGameKeysAreUnique(t *testing.T) {
	seen := map[string]bool{}

	for _, p := range presets {
		if seen[p.Game] {
			t.Errorf("duplicate catalogue key %q", p.Game)
		}
		seen[p.Game] = true
	}
}

func TestPresetForRejectsWhatIsNotThere(t *testing.T) {
	if _, err := PresetFor("minecraft"); err == nil {
		t.Fatal("minecraft is not installable from Steam and must not resolve")
	}

	if _, err := PresetFor("valheim"); err != nil {
		t.Fatalf("valheim should resolve: %v", err)
	}
}

/*
🚨 The expanded template must pass the same validation as a hand-written one,
and its install root must be the OPERATOR's, joined — never something the
catalogue chose.
*/
func TestExpandUsesTheOperatorsRoot(t *testing.T) {
	p, err := PresetFor("valheim")
	if err != nil {
		t.Fatal(err)
	}

	tpl := p.Expand("valheim", "/srv/games/valheim")

	if tpl.InstallRoot != "/srv/games/valheim" {
		t.Errorf("install root is %q, not the operator's", tpl.InstallRoot)
	}
	if tpl.ID != "valheim" {
		t.Errorf("id is %q", tpl.ID)
	}
	if !ValidID(tpl.ID) {
		t.Errorf("catalogue id %q would be refused by the installer's own id check", tpl.ID)
	}
	if tpl.Driver != "process" {
		t.Errorf("driver is %q", tpl.Driver)
	}
	if tpl.SteamApp != 896660 {
		t.Errorf("valheim app id is %d", tpl.SteamApp)
	}
}

/*
🚨 Expand must COPY the backup paths.

Sharing the slice with the package-level preset means a caller that appends to
one template's paths mutates the catalogue for every server created afterwards
in the same process — which would show up as one server quietly backing up
another game's directory.
*/
func TestExpandDoesNotShareSliceStateWithTheCatalogue(t *testing.T) {
	p, _ := PresetFor("valheim")

	a := p.Expand("one", "/srv/a")
	a.BackupPaths = append(a.BackupPaths, "something-else")

	b := p.Expand("two", "/srv/b")

	for _, path := range b.BackupPaths {
		if path == "something-else" {
			t.Fatal("expanding one template changed the catalogue for the next")
		}
	}
}

func TestEveryPresetIsDescribedToTheForum(t *testing.T) {
	// Describe is what the picker renders; a preset the forum cannot describe
	// is one nobody can choose.
	var templates []Template

	for _, p := range presets {
		templates = append(templates, p.Expand(p.Game, "/srv/games/"+p.Game))
	}

	described := List(templates)

	if len(described) != len(presets) {
		t.Fatalf("described %d of %d presets", len(described), len(presets))
	}

	for _, d := range described {
		if d.Label == "" || d.Game == "" {
			t.Errorf("preset %q describes as %+v", d.ID, d)
		}
		if !d.FromSteam {
			t.Errorf("preset %q should report as installable from Steam", d.ID)
		}
	}
}

/*
🚨 HOME must always be present for SteamCMD.

On Debian and Ubuntu `steamcmd` is a shell wrapper whose first act uses $HOME.
A systemd service has none unless its unit sets one, so without this the
installer exits 2 having downloaded nothing — and the only clue is a line on
stderr that a forum ignoring its event stream never showed anybody.
*/
func TestWithHomeAlwaysProvidesOne(t *testing.T) {
	out := withHome([]string{"PATH=/usr/bin"})

	var found string
	for _, kv := range out {
		if strings.HasPrefix(kv, "HOME=") {
			found = kv
		}
	}

	if found == "" || found == "HOME=" {
		t.Fatalf("no usable HOME in %v", out)
	}
}

func TestWithHomeDoesNotOverrideTheOperators(t *testing.T) {
	out := withHome([]string{"HOME=/srv/steam", "PATH=/usr/bin"})

	count := 0
	for _, kv := range out {
		if strings.HasPrefix(kv, "HOME=") {
			count++
			if kv != "HOME=/srv/steam" {
				t.Errorf("overrode the operator's HOME with %q", kv)
			}
		}
	}

	if count != 1 {
		t.Errorf("HOME appears %d times; a duplicate is ambiguous", count)
	}
}

func TestWithHomeTreatsAnEmptyHomeAsAbsent(t *testing.T) {
	// `HOME=` is exactly what a unit with `Environment=HOME=` produces, and the
	// wrapper fails on it the same way it fails on no HOME at all.
	out := withHome([]string{"HOME="})

	ok := false
	for _, kv := range out {
		if strings.HasPrefix(kv, "HOME=") && len(kv) > len("HOME=") {
			ok = true
		}
	}

	if !ok {
		t.Fatal("an empty HOME must be replaced, not accepted")
	}
}
