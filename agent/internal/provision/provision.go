// Package provision installs a new game server from a template the OPERATOR
// declared.
//
// 🚨 THE FORUM SENDS TWO STRINGS: A TEMPLATE NAME AND A SERVER ID.
//
// This is the first feature where the agent gains a server it did not have at
// startup, which means it writes its own config — and that is exactly the
// surface where "the operator decides what may run" could quietly become "the
// forum decides what may run". It does not, and the shape of this package is
// why: the install directory, the start command, the driver and the Steam app
// id all come from a template in the agent's own config file. The forum picks
// one of those templates by name and supplies an id for the new server, and
// both are validated against a pattern before they can become a path.
//
// There is deliberately no field anywhere in this package that lets a caller
// name a directory, a command or an app id. A forum that is fully compromised
// can install one of the games its operator already listed, into the directory
// its operator already chose. It cannot install anything else, anywhere else.
package provision

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/ernestdefoe/garrison/internal/driver"
	"github.com/ernestdefoe/garrison/internal/players"
	"github.com/ernestdefoe/garrison/internal/settings"
)

// Template is one thing the operator is willing to have installed.
type Template struct {
	// ID is what the forum asks for. Never a path.
	ID    string `json:"id"`
	Label string `json:"label,omitempty"`

	// Driver the installed server will run under — "process" or "docker".
	Driver string `json:"driver"`

	// Game is the catalogue key, so the forum can show the right logo.
	Game string `json:"game,omitempty"`

	/*
	 * SteamApp is the app id SteamCMD installs. Zero means this template is
	 * not installed from Steam, in which case Command is expected to point at
	 * something already on the host.
	 *
	 * 🚨 An INT from the operator's file, never a string from the wire. A
	 * numeric field cannot carry an argument, a flag or a second command.
	 */
	SteamApp int `json:"steamApp,omitempty"`

	/*
	 * Download is where to fetch this game when it is NOT on Steam — a URL, or
	 * a token the installer resolves against the publisher's own manifest
	 * ("mojang:release", "factorio:stable").
	 *
	 * 🚨 A token rather than a link for anything whose address changes per
	 * release. Mojang publishes a different URL for every Minecraft version, so
	 * a literal one would install whatever was current the day it was written
	 * and then quietly rot.
	 */
	Download string `json:"download,omitempty"`

	// Archive says how to treat what was downloaded: "tar.gz", "tar.xz",
	// "zip", or empty for a plain file saved as DownloadAs.
	Archive string `json:"archive,omitempty"`

	// DownloadAs names a plain (unarchived) download — server.jar and friends.
	DownloadAs string `json:"downloadAs,omitempty"`

	/*
	 * NeedsBinary is a command this game cannot run without, checked at INSTALL
	 * time rather than at first start.
	 *
	 * 🚨 Minecraft needs a JRE, and Garrison does not install one. Finding that
	 * out after a download, from a start failure, is the same bad trade as a
	 * wrong Steam app id: the cost has already been paid before the problem is
	 * mentioned. Said up front, it is one apt-get away.
	 */
	NeedsBinary string `json:"needsBinary,omitempty"`

	// InstallRoot is the directory new servers are created under. Each gets
	// its own subdirectory named after its id.
	InstallRoot string `json:"installRoot"`

	// Command is what starts the server, relative to its own directory.
	Command string `json:"command,omitempty"`

	StopGraceSeconds int    `json:"stopGraceSeconds,omitempty"`
	StopCommand      string `json:"stopCommand,omitempty"`

	// Everything a provisioned server should inherit, so an operator sets it
	// once per game rather than per server.
	BackupPaths []string        `json:"backupPaths,omitempty"`
	BackupKeep  int             `json:"backupKeep,omitempty"`
	Players     players.Config  `json:"players,omitempty"`
	Config      []settings.File `json:"config,omitempty"`
}

/*
🚨 The id pattern, and it is the only thing between a name from the forum and a
directory on the host.

Deliberately the same discipline as a backup id: lowercase, digits, hyphen and
underscore, bounded, and it cannot begin with a character that makes a relative
path mean something else. `..`, `/`, a leading dash that becomes a command-line
flag — none of them can appear at all, so there is no clever composition to
reason about.
*/
var idPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)

// ValidID reports whether a server id may be used.
func ValidID(id string) bool {
	return idPattern.MatchString(id)
}

// Find resolves a template id against the declared list.
//
// 🚨 The ONLY way a template is ever produced, so there is no code path where
// a name from the forum becomes an install directory or a command.
func Find(templates []Template, id string) (Template, error) {
	for _, t := range templates {
		if t.ID == id {
			return t, nil
		}
	}

	return Template{}, fmt.Errorf("no template called %q is available on this host", id)
}

// Describe is what the forum shows in a picker: enough to choose, nothing that
// names a path.
type Describe struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Game  string `json:"game,omitempty"`

	// FromSteam tells the panel whether to warn about a long download.
	FromSteam bool `json:"fromSteam"`
}

// List describes the declared templates.
//
// 🚨 InstallRoot, Command and SteamApp are NOT in what goes back. They are the
// operator's business and they are the fields an attacker would most like to
// read: knowing where a game lives on disk is the first half of doing something
// about it.
func List(templates []Template) []Describe {
	out := make([]Describe, 0, len(templates))

	for _, t := range templates {
		label := t.Label
		if label == "" {
			label = t.ID
		}

		out = append(out, Describe{ID: t.ID, Label: label, Game: t.Game, FromSteam: t.SteamApp > 0})
	}

	return out
}

// Server builds the server definition a template produces for one id.
//
// 🚨 Every path is composed HERE, from the template's root and a validated id.
// Nothing the caller passed is ever used as a path component without going
// through ValidID first.
func (t Template) Server(id, name string) (driver.Server, error) {
	if !ValidID(id) {
		return driver.Server{}, fmt.Errorf(
			"%q is not a usable server id — use lowercase letters, digits, hyphens and underscores", id)
	}

	name = strings.TrimSpace(name)

	if name == "" {
		name = id
	}

	if len(name) > 64 {
		return driver.Server{}, fmt.Errorf("that name is too long")
	}

	dir := filepath.Join(t.InstallRoot, id)

	return driver.Server{
		ID:               id,
		Name:             name,
		Driver:           t.Driver,
		Game:             t.Game,
		Dir:              dir,
		Command:          t.Command,
		StopGraceSeconds: t.StopGraceSeconds,
		StopCommand:      t.StopCommand,
		BackupRoot:       dir,
		BackupPaths:      t.BackupPaths,
		BackupKeep:       t.BackupKeep,
		Players:          t.Players,

		/*
		 * 🚨 Config paths are rebased under the NEW server's directory.
		 *
		 * A template declares `server.properties` relative to wherever the
		 * game lands; copying the template's absolute path verbatim would
		 * point every provisioned server at the same file, so editing one
		 * would edit them all — and the second server an operator created
		 * would silently share the first one's settings.
		 */
		Config: rebase(t.Config, dir),
	}, nil
}

func rebase(files []settings.File, dir string) []settings.File {
	if len(files) == 0 {
		return nil
	}

	out := make([]settings.File, 0, len(files))

	for _, f := range files {
		if !filepath.IsAbs(f.Path) {
			f.Path = filepath.Join(dir, f.Path)
		}

		out = append(out, f)
	}

	return out
}

// Install runs the installer for a template into a server's directory,
// reporting progress line by line.
//
// 🚨 exec.CommandContext with a fixed argv, never a shell. Every element below
// is either a literal or a value this package composed from the operator's own
// config — there is no string concatenation anywhere near it, so there is
// nothing for a quote or a semicolon to do.
func (t Template) Install(ctx context.Context, id string, progress func(string)) error {
	dir := filepath.Join(t.InstallRoot, id)

	/*
	 * 🚨 The directory is created FIRST, whether or not anything is downloaded.
	 *
	 * A template with no Steam app registers a server pointing at a directory
	 * that does not exist, and the failure surfaces later and elsewhere: the
	 * server appears in the panel, looks ordinary, and refuses to start with
	 * whatever the driver says about a missing working directory. Found
	 * exactly that way on the dev host — the config rewrite was perfect and
	 * the server was unusable.
	 */
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("could not create %s: %w", dir, err)
	}

	/*
	 * 🚨 Checked BEFORE anything is downloaded.
	 *
	 * Minecraft needs a JRE that Garrison does not install. Discovering that
	 * from a failed start, after a download, wastes the operator's bandwidth
	 * to tell them something that was knowable in advance.
	 */
	if t.NeedsBinary != "" {
		if _, err := exec.LookPath(t.NeedsBinary); err != nil {
			return fmt.Errorf("%q needs %q on this host and it is not installed: %w", t.ID, t.NeedsBinary, err)
		}
	}

	if t.SteamApp <= 0 && t.Download != "" {
		resolved, rerr := resolveDownload(ctx, t.Download)
		if rerr != nil {
			return rerr
		}

		return fetchInto(ctx, resolved, t.Archive, t.DownloadAs, dir, progress)
	}

	if t.SteamApp <= 0 {
		// Nothing to download: the operator's template points at something
		// already on the host.
		progress("this template downloads nothing; " + dir + " is ready for it")

		return nil
	}

	steam, err := exec.LookPath("steamcmd")
	if err != nil {
		return fmt.Errorf("steamcmd is not installed on this host, so %q cannot be downloaded: %w", t.ID, err)
	}

	cmd := exec.CommandContext(ctx, steam,
		"+force_install_dir", dir,
		"+login", "anonymous",
		"+app_update", fmt.Sprint(t.SteamApp),
		"validate",
		"+quit",
	)

	/*
	 * 🚨 HOME, or SteamCMD dies before it starts.
	 *
	 * On Debian and Ubuntu `steamcmd` is a shell wrapper, and its very first
	 * act is to use $HOME to find where to unpack itself. A systemd service
	 * gets no HOME unless its unit sets one, so the wrapper exits 2 with
	 *
	 *     /usr/local/bin/steamcmd: 16: HOME: parameter not set
	 *
	 * and nothing is downloaded. Found on a real host: the install reported
	 * failure into an event stream the forum was discarding at the time, so
	 * the symptom was an empty directory and total silence.
	 *
	 * Supplied here rather than left to the operator's unit file, because an
	 * agent that installs games only when somebody remembered to set HOME is
	 * one that works on the packager's machine and fails on everybody else's.
	 * An existing HOME is never overridden — the operator's environment wins.
	 */
	cmd.Env = withHome(os.Environ())

	/*
	 * 🚨 stderr is folded into stdout deliberately. SteamCMD reports several
	 * of its most useful failures — a disk that filled, an app id that needs a
	 * login — on stderr, and an installer that streams a tidy progress log
	 * while swallowing the reason it failed is worse than one that prints
	 * nothing.
	 */
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}

	cmd.Stderr = cmd.Stdout

	if err := cmd.Start(); err != nil {
		return err
	}

	scanner := bufio.NewScanner(pipe)

	// SteamCMD's progress lines are long, and a truncated one is a line that
	// stops at the percentage and never says what failed.
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		if line != "" {
			progress(line)
		}
	}

	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("steamcmd failed: %w", err)
	}

	return nil
}

/*
withHome guarantees a HOME in the environment SteamCMD is run with.

🚨 Falls back to the invoking user's home directory and only then to /tmp. A
wrong-but-present HOME is far better than an absent one: SteamCMD unpacks
itself there, so the worst case is a re-download into a scratch directory,
against a certain failure to start at all.
*/
func withHome(env []string) []string {
	for _, kv := range env {
		if strings.HasPrefix(kv, "HOME=") && len(kv) > len("HOME=") {
			return env
		}
	}

	home := "/tmp"

	if u, err := user.Current(); err == nil && u.HomeDir != "" {
		home = u.HomeDir
	}

	return append(env, "HOME="+home)
}
