package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ernestdefoe/garrison/internal/driver"
	"github.com/ernestdefoe/garrison/internal/offsite"
	"github.com/ernestdefoe/garrison/internal/players"
	"github.com/ernestdefoe/garrison/internal/settings"
)

/*
Preflight: everything that can be wrong with a config, said at once.

🚨 THIS IS THE OPERATOR'S FIRST CONTACT, AND IT USED TO CHECK ALMOST NOTHING.

An agent that starts cleanly and then fails at the first backup, the first
restore, or the first attempt to read a config file has taught its operator that
Garrison is unreliable — and taught them at the worst moment, which is the one
where they needed it. Every mistake below is one somebody makes while writing a
JSON file by hand: a path that does not exist, a bucket whose keys are wrong, a
preset whose name is spelled differently, a config file that moved.

All of them are cheap to check before anything is running, and expensive to
discover any other way. So `--check` does the whole thing and reports EVERY
fault rather than stopping at the first, because an operator fixing a config
wants the list, not a game of whack-a-mole with a restart between each round.

🚨 It reaches the network on purpose. Checking that off-site credentials are
merely PRESENT is the check that passes for a typo'd secret key and lets
somebody believe their backups are safe for a year. Listing the bucket is the
only answer that means anything, and it is one request.
*/

// Finding is one thing worth telling the operator.
type Finding struct {
	Server string
	// Bad findings fail the check; the rest are information.
	Bad  bool
	Text string
}

// Check inspects a whole configuration and reports what it finds.
//
// ctx bounds the network checks. Everything else is local and immediate.
func Check(ctx context.Context, configPath string, servers []driver.Server, available []string, unavailable []string) []Finding {
	var out []Finding

	add := func(server string, bad bool, format string, args ...any) {
		out = append(out, Finding{Server: server, Bad: bad, Text: fmt.Sprintf(format, args...)})
	}

	sort.Strings(available)
	add("", false, "drivers available: %s", strings.Join(available, ", "))

	checkConfigFilePermissions(configPath, add)

	for _, u := range unavailable {
		// Not a failure: a host without Docker is a perfectly good host, and
		// most of them are. It is worth SAYING, because "my Docker server does
		// nothing" usually starts here — and a server that actually needs it
		// gets its own BAD line below.
		add("", false, "driver unavailable: %s", u)
	}

	for _, s := range servers {
		/*
		 * 🚨 Every server gets at least one line, even one with nothing
		 * optional configured.
		 *
		 * A server that produces no output at all reads as one the agent did
		 * not load — and the commonest real mistake in a hand-written config
		 * is a server that IS missing, because a comma landed in the wrong
		 * place. Naming each one, with its driver, is how an operator counts
		 * them against what they expect.
		 */
		add(s.ID, false, "driver %s, stop grace %s", s.Driver, s.Grace())

		if !contains(available, s.Driver) {
			add(s.ID, true, "driver %q is not available on this host", s.Driver)
		}

		checkBackups(s, add)
		checkConfig(s, add)
		checkPlayers(s, add)
		checkOffsite(ctx, s, add)
	}

	return out
}

/*
checkConfigFilePermissions warns when the agent's own config is readable by
anyone on the host.

🚨 THAT FILE HOLDS THE AGENT'S TOKEN, and the token is the whole of this agent's
authority: anything that can read it can impersonate the host to the forum, and
the operator's off-site bucket keys are usually in the same file.

A world-readable config is not something an operator will notice — it is the
default umask on most distributions, and nothing about a working agent looks
different. Saying it once, at install time, is the only moment it gets fixed.
*/
func checkConfigFilePermissions(path string, add func(string, bool, string, ...any)) {
	if path == "" {
		return
	}

	info, err := os.Stat(path)
	if err != nil {
		return
	}

	mode := info.Mode().Perm()

	// Anything readable by group or other.
	if mode&0o077 != 0 {
		add("", true, "%s is mode %04o — it holds this agent's token and any off-site keys, so it should be 0600", path, mode)
	}
}

func checkBackups(s driver.Server, add func(string, bool, string, ...any)) {
	if s.BackupRoot == "" && len(s.BackupPaths) == 0 {
		return
	}

	if s.BackupRoot == "" || len(s.BackupPaths) == 0 {
		add(s.ID, true, "backups are half configured: backupRoot and backupPaths are both needed")

		return
	}

	info, err := os.Stat(s.BackupRoot)

	if err != nil {
		add(s.ID, true, "backupRoot %q cannot be read: %v", s.BackupRoot, err)

		return
	}

	if !info.IsDir() {
		add(s.ID, true, "backupRoot %q is not a directory", s.BackupRoot)

		return
	}

	/*
	 * 🚨 Every path is checked, not just the first.
	 *
	 * A backup silently missing one of three directories is the worst possible
	 * shape of this bug: the archive is created, it is listed, it restores
	 * without error, and the thing somebody needed is not in it. They find out
	 * at the moment they are restoring from it.
	 */
	missing := 0

	for _, p := range s.BackupPaths {
		full := filepath.Join(s.BackupRoot, p)

		if _, err := os.Stat(full); err != nil {
			add(s.ID, true, "backup path %q does not exist under backupRoot (%v)", p, err)
			missing++
		}
	}

	/*
	 * 🚨 Said out loud when it is FINE, not only when it is broken.
	 *
	 * Every other optional feature here reports on success, and silence about
	 * a configured one reads as "not configured" — which is the wrong thing
	 * for an operator to conclude about their backups, of all things.
	 */
	if missing == 0 {
		keep := "all"
		if s.BackupKeep > 0 {
			keep = fmt.Sprintf("%d", s.BackupKeep)
		}

		add(s.ID, false, "backups: %d path(s) under %s, keeping %s", len(s.BackupPaths), s.BackupRoot, keep)
	}
}

func checkConfig(s driver.Server, add func(string, bool, string, ...any)) {
	seen := map[string]bool{}

	for _, f := range s.Config {
		if f.ID == "" {
			add(s.ID, true, "a config file has no id, so the forum could never ask for it")

			continue
		}

		// 🚨 A duplicate id means Find() returns the first and the second is
		// unreachable — an editable file that silently is not.
		if seen[f.ID] {
			add(s.ID, true, "two config files share the id %q; only the first can ever be opened", f.ID)
		}

		seen[f.ID] = true

		if f.Format != settings.FormatProperties && f.Format != settings.FormatINI {
			add(s.ID, true, "config %q has format %q; known formats are %q and %q",
				f.ID, f.Format, settings.FormatProperties, settings.FormatINI)

			continue
		}

		set, err := settings.Read(f)

		if err != nil {
			add(s.ID, true, "config %q cannot be read: %v", f.ID, err)

			continue
		}

		editable := 0
		for _, e := range set.Entries {
			if e.Editable {
				editable++
			}
		}

		add(s.ID, false, "config %q: %d setting(s), %d editable", f.ID, len(set.Entries), editable)

		/*
		 * 🚨 A `keys` entry that matches nothing in the file is almost always
		 * a typo, and it fails in the confusing direction: the setting simply
		 * is not offered, which reads as Garrison not supporting it.
		 */
		for _, want := range f.Keys {
			found := false

			for _, e := range set.Entries {
				if e.Key == want {
					found = true

					break
				}
			}

			if !found {
				add(s.ID, true, "config %q allows the key %q, which is not in the file", f.ID, want)
			}
		}
	}
}

func checkPlayers(s driver.Server, add func(string, bool, string, ...any)) {
	if !s.Players.Configured() {
		return
	}

	w, err := players.New(s.Players)

	if err != nil {
		add(s.ID, true, "players: %v", err)

		return
	}

	if w.CanSay() {
		add(s.ID, false, "players: reading who is online, and can verify forum accounts in-game")

		return
	}

	/*
	 * 🚨 Information, not a failure. Plenty of games have no whisper command —
	 * Valheim among them — and reading who is playing is useful on its own.
	 * What would be wrong is letting an operator believe the link flow works
	 * when the forum will never offer it.
	 */
	add(s.ID, false, "players: reading who is online; no whisper command, so forum accounts cannot be linked here")
}

func checkOffsite(ctx context.Context, s driver.Server, add func(string, bool, string, ...any)) {
	if !s.Offsite.Configured() {
		return
	}

	store, err := offsite.New(s.Offsite)

	if err != nil {
		add(s.ID, true, "off-site: %v", err)

		return
	}

	/*
	 * 🚨 The bucket is actually LISTED, not merely configured.
	 *
	 * A credentials check that only looks for non-empty strings passes for a
	 * typo'd secret key — and then off-site backups fail every night into a
	 * log nobody reads, while the operator believes their worlds are safe
	 * somewhere else. One request at install time turns that into a sentence
	 * they can act on, with the provider's own words in it.
	 */
	listCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	objects, err := store.List(listCtx)

	if err != nil {
		add(s.ID, true, "off-site: the bucket could not be listed: %v", err)

		return
	}

	add(s.ID, false, "off-site: %s reachable, %d archive(s) already there", s.Offsite.Bucket, len(objects))
}

func contains(haystack []string, needle string) bool {
	for _, h := range haystack {
		if h == needle {
			return true
		}
	}

	return false
}
