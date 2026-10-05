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
		/*
		 * 🚨 Every preset must be installable by SOME means. A preset with
		 * neither a Steam app id nor a download is one the picker offers and
		 * the installer cannot act on — it would create an empty directory,
		 * report success, and leave somebody wondering where their server is.
		 */
		if p.SteamApp <= 0 && p.Download == "" {
			t.Errorf("%s: neither a Steam app id nor a download, so nothing can install it", p.Game)
		}
		if p.SteamApp > 0 && p.Download != "" {
			t.Errorf("%s: has both a Steam app id and a download; which one wins is not obvious", p.Game)
		}
		// A download that is not an archive must say what to call the file, or
		// it lands as "download" and no command will find it.
		if p.Download != "" && p.Archive == "" && p.DownloadAs == "" {
			t.Errorf("%s: a plain download needs downloadAs, or the file has no usable name", p.Game)
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
	// Terraria has no permanent "latest" download to resolve against, so it is
	// deliberately absent rather than pinned to a version that will go stale.
	if _, err := PresetFor("terraria"); err == nil {
		t.Fatal("terraria has no resolvable download and must not be offered")
	}

	for _, game := range []string{"valheim", "minecraft", "factorio"} {
		if _, err := PresetFor(game); err != nil {
			t.Errorf("%s should resolve: %v", game, err)
		}
	}
}

/*
🚨 A download token must be one the installer actually understands.

A typo'd token is indistinguishable from a URL, so it would be fetched as one —
producing a confusing network error at install time instead of a clear refusal
here.
*/
func TestEveryDownloadIsAURLOrAKnownToken(t *testing.T) {
	known := map[string]bool{"mojang:release": true, "factorio:stable": true}

	for _, p := range presets {
		if p.Download == "" {
			continue
		}

		if known[p.Download] {
			continue
		}

		if !strings.HasPrefix(p.Download, "https://") {
			t.Errorf("%s: download %q is neither https nor a token the installer knows", p.Game, p.Download)
		}
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
		// FromSteam tells the panel whether to warn about a long download, so
		// it must match where the game actually comes from.
		p, _ := PresetFor(d.Game)
		if d.FromSteam != (p.SteamApp > 0) {
			t.Errorf("preset %q reports FromSteam=%v but SteamApp=%d", d.ID, d.FromSteam, p.SteamApp)
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
