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

	// path is remembered so a provisioned server can be persisted back. Not
	// serialised: it is where this file came from, not part of it.
	path string
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
		case "":
			return fmt.Errorf("server %q has no driver", s.ID)
		default:
			// Naming systemd or service here is not a typo — they are real
			// drivers, just not in this phase. Say which, rather than
			// "unknown driver", so the operator knows to wait rather than to
			// go looking for their mistake.
			return fmt.Errorf("server %q wants the %q driver, which this agent does not implement yet", s.ID, s.Driver)
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
