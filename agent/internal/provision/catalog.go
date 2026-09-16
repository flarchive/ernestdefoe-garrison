package provision

import (
	"fmt"
	"path/filepath"
	"sort"

	"github.com/ernestdefoe/garrison/internal/players"
)

/*
Built-in install templates, so that running a game server does not require
knowing its Steam app id and its launch flags.

🚨 THIS DOES NOT WIDEN WHAT AN AGENT MAY INSTALL.

The permission model is unchanged and deliberately so: declaring a template is
still the whole of the permission, the operator still names each game they are
willing to host, and they still choose the directory it goes in. What the
catalogue removes is the research — the app id, the binary's path inside the
download, the flag that stops a headless server trying to open a window. Those
are facts about a game, not decisions about a host, and making every operator
rediscover them was the reason provisioning went unused.

🚨 STEAM ONLY, and the omissions are honest ones.

Minecraft, Factorio and Terraria are not here. None of them installs through
SteamCMD — Minecraft is a jar from Mojang, Factorio a tarball from its own site,
Terraria a zip — and the installer in this package downloads from Steam and
nothing else. Listing them with a zero app id would produce a template that
looks installable, fails at install time, and teaches an operator that the
feature is broken. Garrison still RUNS all three perfectly well; it just cannot
fetch them for you, and says so rather than implying otherwise.
*/

// Preset is a catalogue entry: everything about a game that is true wherever it
// is installed. The operator supplies what is true about their host.
type Preset struct {
	// Game is the catalogue key the forum already uses for logos and log
	// presets, so a provisioned server gets the right artwork with no extra
	// mapping.
	Game  string
	Label string

	SteamApp int

	// Command is relative to the server's own directory.
	Command string

	// StopCommand is sent before a signal where the game has a clean shutdown.
	StopCommand      string
	StopGraceSeconds int

	// BackupPaths are relative to the server's directory — the saves, and
	// nothing else. A backup of the whole install would copy gigabytes of game
	// binaries that SteamCMD can fetch again for free.
	BackupPaths []string

	Players players.Config
}

/*
🚨 App ids are the dedicated-SERVER app, not the game.

They are different numbers, and using the game's id installs something that
cannot run headless — the failure arrives as a missing binary long after a
multi-gigabyte download, which is the most expensive way possible to learn it.

Every id below was checked against Valve on 2026-09-16 with

	steamcmd +login anonymous +app_info_print <id> +quit

and each returned a name ending in "Dedicated Server". Do the same for anything
added here: the tests below can prove an id is PRESENT and well-formed, but no
offline test can prove it is the right number.
*/
var presets = []Preset{
	{
		Game:             "valheim",
		Label:            "Valheim",
		SteamApp:         896660,
		Command:          "./valheim_server.x86_64 -nographics -batchmode -public 1",
		StopGraceSeconds: 40,
		// 🚨 worlds_local, not worlds. Valheim writes the live world to
		// worlds_local and keeps a legacy copy in worlds; backing up the wrong
		// one produces an archive that restores an old world silently.
		BackupPaths: []string{"worlds_local"},
		Players:     players.Config{Preset: "valheim"},
	},
	{
		Game:             "rust",
		Label:            "Rust",
		SteamApp:         258550,
		Command:          "./RustDedicated -batchmode -nographics",
		StopGraceSeconds: 60,
		BackupPaths:      []string{"server"},
		Players:          players.Config{Preset: "rust"},
	},
	{
		Game:             "ark",
		Label:            "ARK: Survival Evolved",
		SteamApp:         376030,
		Command:          "ShooterGame/Binaries/Linux/ShooterGameServer TheIsland?listen",
		StopGraceSeconds: 90,
		BackupPaths:      []string{"ShooterGame/Saved"},
		Players:          players.Config{Preset: "ark"},
	},
	{
		Game:             "7dtd",
		Label:            "7 Days to Die",
		SteamApp:         294420,
		Command:          "./startserver.sh -configfile=serverconfig.xml",
		StopGraceSeconds: 60,
		BackupPaths:      []string{"Saves"},
	},
	{
		Game:             "projectzomboid",
		Label:            "Project Zomboid",
		SteamApp:         380870,
		Command:          "./start-server.sh",
		StopGraceSeconds: 60,
		BackupPaths:      []string{"Zomboid/Saves"},
	},
	{
		Game:             "palworld",
		Label:            "Palworld",
		SteamApp:         2394010,
		Command:          "./PalServer.sh",
		StopGraceSeconds: 45,
		BackupPaths:      []string{"Pal/Saved"},
	},
	{
		Game:             "satisfactory",
		Label:            "Satisfactory",
		SteamApp:         1690800,
		Command:          "./FactoryServer.sh",
		StopGraceSeconds: 60,
		BackupPaths:      []string{"FactoryGame/Saved"},
	},
}

// Games lists every catalogue key, sorted, for error messages and docs.
func Games() []string {
	out := make([]string, 0, len(presets))

	for _, p := range presets {
		out = append(out, p.Game)
	}

	sort.Strings(out)

	return out
}

// PresetFor resolves a catalogue key.
func PresetFor(game string) (Preset, error) {
	for _, p := range presets {
		if p.Game == game {
			return p, nil
		}
	}

	return Preset{}, fmt.Errorf("no built-in template for %q; Garrison knows %v", game, Games())
}

/*
Expand turns a catalogue choice into a real template.

🚨 installRoot comes from the OPERATOR and is joined here, never taken from the
catalogue. A preset describes a game; where that game is allowed to write on
this particular machine is not a fact about the game, and a built-in default
would be a path nobody chose.
*/
func (p Preset) Expand(id, installRoot string) Template {
	return Template{
		ID:               id,
		Label:            p.Label,
		Driver:           "process",
		Game:             p.Game,
		SteamApp:         p.SteamApp,
		InstallRoot:      filepath.Clean(installRoot),
		Command:          p.Command,
		StopCommand:      p.StopCommand,
		StopGraceSeconds: p.StopGraceSeconds,
		BackupPaths:      append([]string(nil), p.BackupPaths...),
		BackupKeep:       7,
		Players:          p.Players,
	}
}
