// Package config loads the agent's own configuration.
//
// 🚨 This file is the ONLY place a command line, a container name or a working
// directory can be written. The forum names a server by ID and nothing more,
// so the set of things this agent can run is fixed by an operator with shell
// access to the host and cannot be widened over the wire.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ernestdefoe/garrison/internal/driver"
	"github.com/ernestdefoe/garrison/internal/provision"
)

// Config is the agent's config file.
//
// JSON rather than YAML for the spike, purely to keep the dependency list at
// one. Operators will want YAML and phase 1 should give them it — this shape
// is what gets parsed either way.
type Config struct {
	// ForumURL is the endpoint the agent dials OUT to.
	//
	// An https:// or http:// URL uses the polling transport, which is the one
	// the product ships with and needs nothing on the forum host but Flarum.
	// A ws:// or wss:// URL uses the websocket transport, which needs a
	// gateway daemon there — the optional upgrade for sub-second streaming,
	// never the only way in.
	ForumURL string `json:"forumUrl"`
	// Token authenticates this agent. Issued by the forum at pairing.
	Token string `json:"token"`
	// Servers this agent may operate.
	Servers []driver.Server `json:"servers"`

	/*
	 * Templates the operator is willing to have installed.
	 *
	 * 🚨 Declaring one is the whole of the permission. The forum picks a
	 * template by name and supplies an id for the new server; the install
	 * directory, the start command and the Steam app id all come from here. An
	 * agent with no templates cannot be asked to install anything, and that is
	 * the default.
	 */
	Templates []provision.Template `json:"templates,omitempty"`

	/*
	 * Built-in templates the operator wants, by game.
	 *
	 * 🚨 A shortcut for writing `templates` by hand, NOT a widening of what an
	 * agent may install. The operator still names every game they are willing to
	 * host and still chooses the directory it goes in; what they no longer have
	 * to know is the Steam app id and the launch flags, which are facts about a
	 * game rather than decisions about a host.
	 *
	 * Expanded into Templates at load, so everything downstream — Find, the
	 * installer, the forum's picker — sees one uniform list and has no idea
	 * whether an entry was typed or came from the catalogue.
	 */
	Catalog *CatalogConfig `json:"catalog,omitempty"`

	// path is remembered so a provisioned server can be persisted back. Not
	// serialised: it is where this file came from, not part of it.
	path string
}

/*
CatalogConfig is the short way to declare templates.

	"catalog": { "installRoot": "/srv/games", "games": ["valheim", "rust"] }
*/
type CatalogConfig struct {
	// InstallRoot is where servers created from these templates are put. Every
	// game gets its own subdirectory beneath it.
	InstallRoot string `json:"installRoot"`

	// Games are catalogue keys — see provision.Games().
	Games []string `json:"games"`
}

// Path is where this config was loaded from.
func (c *Config) Path() string {
	return c.path
}

// Load reads and validates a config file.
func Load(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c Config
	dec := json.NewDecoder(strings.NewReader(string(b)))
	// 🚨 Refuse unknown fields. A typo in a config key is otherwise silently
	// ignored, and the operator spends an evening wondering why stopGrace
	// (which should be stopGraceSeconds) does nothing.
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	/*
	 * 🚨 Expanded BEFORE Validate, so catalogue entries face exactly the same
	 * checks as hand-written ones — a duplicate id between the two lists, a
	 * missing installRoot, an unknown driver. Validating first and expanding
	 * after would create one class of template nothing had ever checked.
	 */
	if err := c.expandCatalog(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}

	if err := c.Validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}

	c.path = path

	return &c, nil
}

/*
Add appends a provisioned server and writes the file back.

🚨 THE AGENT EDITING ITS OWN ALLOWLIST, WHICH IS THE ONE THING THIS FILE EXISTS
TO PREVENT ANYBODY ELSE DOING.

It is safe only because of what reaches here: a driver.Server built by
provision.Template.Server from a template the operator declared and an id that
passed provision.ValidID. Nothing in it came from the wire as a path or a
command. If that ever stops being true, this function becomes remote code
execution on every host running the agent.

The write goes through a temporary file and a rename, for the reason every
write in this codebase does: a truncate-then-write interrupted halfway leaves an
agent config that is empty or half-parsed, and the agent then starts with no
servers at all — which looks exactly like every game on the host disappearing.
*/
func (c *Config) Add(server driver.Server) error {
	if c.path == "" {
		return fmt.Errorf("this config was not loaded from a file, so it cannot be written back")
	}

	for _, s := range c.Servers {
		if s.ID == server.ID {
			return fmt.Errorf("a server called %q already exists on this host", server.ID)
		}
	}

	next := *c
	next.Servers = append(append([]driver.Server{}, c.Servers...), server)

	// 🚨 Validated BEFORE anything is written. A config that would not load is
	// a config that must never reach the disk — the agent would fail to start
	// on its next restart, taking every other server with it.
	if err := next.Validate(); err != nil {
		return fmt.Errorf("the new server would make this config invalid: %w", err)
	}

	encoded, err := json.MarshalIndent(next, "", "  ")
	if err != nil {
		return err
	}

	if err := replaceFile(c.path, append(encoded, '\n')); err != nil {
		return err
	}

	c.Servers = next.Servers

	return nil
}

// replaceFile writes through a temporary file in the same directory and renames
// it over the original, so the config is either the old one or the new one and
// never a half-written thing in between.
func replaceFile(path string, contents []byte) error {
	dir := filepath.Dir(path)

	tmp, err := os.CreateTemp(dir, ".garrison-agent-*")
	if err != nil {
		return err
	}

	name := tmp.Name()
	defer os.Remove(name)

	/*
	 * 🚨 The original's permissions, and this file holds the agent's TOKEN.
	 *
	 * A config that arrives at 0600 and is rewritten at whatever CreateTemp
	 * felt like is a credential leak caused by a feature that has nothing to do
	 * with credentials. os.CreateTemp is already 0600, so this only ever
	 * narrows or matches — but it is written down because the next person to
	 * touch this function will not know that.
	 */
	if info, statErr := os.Stat(path); statErr == nil {
		_ = tmp.Chmod(info.Mode().Perm())
	}

	if _, err := tmp.Write(contents); err != nil {
		tmp.Close()

		return err
	}

	if err := tmp.Sync(); err != nil {
		tmp.Close()

		return err
	}

	if err := tmp.Close(); err != nil {
		return err
	}

	return os.Rename(name, path)
}

// Validate catches what would otherwise be a confusing runtime failure.
func (c *Config) Validate() error {
	seen := map[string]bool{}
	for i, s := range c.Servers {
		switch {
		case s.ID == "":
			return fmt.Errorf("server %d has no id", i)
		case seen[s.ID]:
			return fmt.Errorf("duplicate server id %q", s.ID)
		}
		seen[s.ID] = true

		switch s.Driver {
		case "docker":
			if s.Container == "" {
				return fmt.Errorf("server %q uses the docker driver but names no container", s.ID)
			}
		case "process":
			if s.Command == "" {
				return fmt.Errorf("server %q uses the process driver but has no command", s.ID)
			}
		case "systemd":
			// 🚨 The unit is the whole configuration for this driver. Without it
			// the agent would start nothing and report nothing, and the operator
			// would be reading logs to find a missing line in a file.
			if strings.TrimSpace(s.Unit) == "" {
				return fmt.Errorf("server %q uses the systemd driver but has no unit", s.ID)
			}
		case "":
			return fmt.Errorf("server %q has no driver", s.ID)
		default:
			// Naming `service` here is not a typo — it is a real driver, just
			// not in this phase. Say which, rather than "unknown driver", so
			// the operator knows to wait rather than to go looking for their
			// mistake.
			return fmt.Errorf("server %q wants the %q driver, which this agent does not implement yet", s.ID, s.Driver)
		}
	}

	for _, s := range c.Servers {
		for _, h := range s.Health {
			/*
			 * 🚨 A probe missing the field its type needs checks NOTHING, and
			 * a probe that checks nothing reports healthy. The operator sees a
			 * configured safeguard and believes something is watching.
			 *
			 * So a mistake is refused at load, where it is one line in a file,
			 * rather than at 3am when the thing it was supposed to catch
			 * happens and nobody was looking.
			 */
			switch h.Type {
			case "tcp", "udp_recvq":
				if h.Port == 0 {
					return fmt.Errorf("server %q has a %q probe %q with no port",
						s.ID, h.Type, h.Name)
				}
			case "log_match", "log_quiet":
				if strings.TrimSpace(h.Pattern) == "" {
					return fmt.Errorf("server %q has a %q probe %q with no pattern",
						s.ID, h.Type, h.Name)
				}
			case "":
				return fmt.Errorf("server %q has a health probe %q with no type", s.ID, h.Name)
			}
		}
	}

	seenTemplate := map[string]bool{}

	for i, t := range c.Templates {
		switch {
		case t.ID == "":
			return fmt.Errorf("template %d has no id", i)
		case seenTemplate[t.ID]:
			return fmt.Errorf("duplicate template id %q", t.ID)
		case t.InstallRoot == "":
			return fmt.Errorf("template %q has no installRoot, so there is nowhere to put a server", t.ID)
		case t.Driver == "":
			return fmt.Errorf("template %q has no driver", t.ID)
		case t.Driver == "process" && t.Command == "":
			return fmt.Errorf("template %q uses the process driver but has no command", t.ID)
		}

		seenTemplate[t.ID] = true
	}
	return nil
}

/*
expandCatalog turns `catalog` into ordinary templates.

🚨 A hand-written template WINS over a catalogue entry for the same id, and
silently — because that is what an override is for. An operator who needs a
different launch flag for their Valheim server writes the whole template out,
and it must not then collide with the built-in they also asked for.
*/
func (c *Config) expandCatalog() error {
	if c.Catalog == nil || len(c.Catalog.Games) == 0 {
		return nil
	}

	if c.Catalog.InstallRoot == "" {
		return fmt.Errorf("catalog has no installRoot, so there is nowhere to put a server")
	}

	declared := map[string]bool{}
	for _, t := range c.Templates {
		declared[t.ID] = true
	}

	seen := map[string]bool{}

	for _, game := range c.Catalog.Games {
		if seen[game] {
			return fmt.Errorf("catalog lists %q twice", game)
		}
		seen[game] = true

		// The operator wrote this one out in full; theirs wins.
		if declared[game] {
			continue
		}

		preset, err := provision.PresetFor(game)
		if err != nil {
			return err
		}

		c.Templates = append(c.Templates, preset.Expand(game, filepath.Join(c.Catalog.InstallRoot, game)))
	}

	return nil
}
