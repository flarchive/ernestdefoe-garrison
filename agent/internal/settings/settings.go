// Package settings reads and writes a game server's configuration files.
//
// 🚨 A DECLARED LIST OF FILES, NOT A FILE MANAGER.
//
// The obvious feature here is "browse the server's folder and edit anything",
// and it is the single most dangerous thing this product could ship. A file
// manager on a game host is arbitrary write access, and arbitrary write access
// on a host that runs a start script is arbitrary code execution — reached from
// a PHP forum on the public internet running third-party extension code. One
// compromised admin session would own the machine.
//
// So the operator declares, in the agent's own config, exactly which files may
// be edited. The forum names a file by its ID from that list and never by a
// path; an id that is not in the list does not resolve to anything. There is no
// shape of request that reaches a file the operator did not name.
//
// The second rule is that editing preserves everything it does not change.
// Game config files are full of comments explaining what the settings do, and a
// naive read-modify-write turns somebody's annotated server.properties into a
// bare list of keys. Rewriting a single line in place is more work and it is
// the difference between a tool people trust with their files and one they use
// once.
package settings

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Format is how a file is laid out.
type Format string

const (
	// FormatProperties is `key=value`, one per line, `#` comments. Minecraft's
	// server.properties and most Java game servers.
	FormatProperties Format = "properties"

	// FormatINI is the same with [sections]. Source-engine and many others.
	FormatINI Format = "ini"
)

// File is one editable configuration file, as the OPERATOR declared it.
type File struct {
	// ID is what the forum asks for. Never a path.
	ID string `json:"id"`

	// Label is what a person sees. Optional; the id is used otherwise.
	Label string `json:"label,omitempty"`

	Path   string `json:"path"`
	Format Format `json:"format"`

	/*
	 * 🚨 ReadOnly exists because "show me the config" and "let me change it"
	 * are different permissions in practice.
	 *
	 * An operator may well want staff to READ the startup arguments or a
	 * whitelist without being able to edit them — and the alternative, leaving
	 * the file out of the list entirely, means nobody can see it either.
	 */
	ReadOnly bool `json:"readOnly,omitempty"`

	/*
	 * Keys, when set, narrows what may be edited within the file.
	 *
	 * 🚨 This is a second gate and a deliberate one. A Minecraft
	 * server.properties contains `rcon.password` and `level-seed` next to
	 * `motd`; an operator who wants staff changing the message of the day
	 * should not have to give them the RCON password to do it. Empty means
	 * every key in the file.
	 */
	Keys []string `json:"keys,omitempty"`
}

// Entry is one setting as it currently stands.
type Entry struct {
	Key     string `json:"key"`
	Value   string `json:"value"`
	Section string `json:"section,omitempty"`

	// Comment is the `#` line immediately above, if any — usually the game's
	// own explanation of what the setting does, and the most useful thing on
	// the screen.
	Comment string `json:"comment,omitempty"`

	// Editable is false for a key outside the declared allowlist.
	Editable bool `json:"editable"`
}

// Set is a file and what is in it.
type Set struct {
	ID       string  `json:"id"`
	Label    string  `json:"label"`
	Format   Format  `json:"format"`
	ReadOnly bool    `json:"readOnly"`
	Entries  []Entry `json:"entries"`
}

// Find resolves an id against the declared list.
//
// 🚨 The ONLY way a path is ever produced. Every caller goes through this, so
// there is no code path where a name from the forum becomes a filename.
func Find(files []File, id string) (File, error) {
	for _, f := range files {
		if f.ID == id {
			return f, nil
		}
	}

	return File{}, fmt.Errorf("no configuration file called %q is available on this server", id)
}

// Read parses a declared file.
func Read(f File) (*Set, error) {
	handle, err := os.Open(f.Path)
	if err != nil {
		return nil, err
	}
	defer handle.Close()

	out := &Set{ID: f.ID, Label: label(f), Format: f.Format, ReadOnly: f.ReadOnly}

	allowed := allowSet(f)

	var (
		section string
		comment string
	)

	scanner := bufio.NewScanner(handle)

	// 🚨 A generous line cap. Some games write a single line listing every
	// installed mod, and bufio's default 64 KiB limit would stop the scan
	// partway with no error a caller can distinguish from end of file — the
	// panel would show half a config and nothing would say so.
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)

		if trimmed == "" {
			comment = ""

			continue
		}

		if strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, ";") {
			comment = strings.TrimSpace(strings.TrimLeft(trimmed, "#; "))

			continue
		}

		if f.Format == FormatINI && strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			section = strings.TrimSpace(trimmed[1 : len(trimmed)-1])
			comment = ""

			continue
		}

		key, value, found := strings.Cut(trimmed, "=")
		if !found {
			comment = ""

			continue
		}

		key = strings.TrimSpace(key)

		out.Entries = append(out.Entries, Entry{
			Key:      key,
			Value:    strings.TrimSpace(value),
			Section:  section,
			Comment:  comment,
			Editable: !f.ReadOnly && allowed(key),
		})

		comment = ""
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return out, nil
}

// Write changes one key in place.
//
// 🚨 IN PLACE, preserving every other line exactly — comments, blank lines,
// ordering, and any syntax this parser did not understand. A game config is
// usually the game's own annotated template, and rewriting the file from parsed
// values would silently delete the explanations somebody relies on.
//
// It also refuses to CREATE a key that is not already in the file. A typo would
// otherwise append a setting the game ignores, and the operator would see it
// saved, see no effect, and conclude the panel does not work.
func Write(f File, section, key, value string) error {
	if f.ReadOnly {
		return fmt.Errorf("%q is read-only", label(f))
	}

	if !allowSet(f)(key) {
		return fmt.Errorf("%q is not one of the settings that may be changed in %q", key, label(f))
	}

	/*
	 * 🚨 A value may not contain a newline, ever.
	 *
	 * One newline turns a single setting into two lines, and the second line
	 * is whatever the sender wants — `rcon.password=x` appended to a file the
	 * operator only allowed `motd` in. This is the whole allowlist defeated by
	 * a text field, and it is the first thing anybody would try.
	 */
	if strings.ContainsAny(value, "\r\n") {
		return fmt.Errorf("a setting cannot contain a line break")
	}

	original, err := os.ReadFile(f.Path)
	if err != nil {
		return err
	}

	lines := strings.Split(string(original), "\n")

	var (
		current string
		written bool
	)

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)

		if f.Format == FormatINI && strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			current = strings.TrimSpace(trimmed[1 : len(trimmed)-1])

			continue
		}

		if strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, ";") || trimmed == "" {
			continue
		}

		existing, _, found := strings.Cut(trimmed, "=")
		if !found || strings.TrimSpace(existing) != key {
			continue
		}

		if f.Format == FormatINI && current != section {
			continue
		}

		// Keep the key exactly as it was written, including any indentation,
		// so a file that lines its values up stays lined up.
		indent := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
		lines[i] = indent + strings.TrimSpace(existing) + "=" + value
		written = true

		break
	}

	if !written {
		return fmt.Errorf("%q is not in %q — Garrison will not add settings a game may not recognise", key, label(f))
	}

	return replace(f.Path, strings.Join(lines, "\n"))
}

/*
replace writes the file through a temporary file in the same directory and
renames it over the original.

🚨 Because the alternative loses somebody's entire server configuration.

Writing directly means truncating the file and then writing it back. A crash, a
full disk or a killed process between those two leaves a server.properties that
is empty or half-written — and the game reads it on next start, finds nothing,
and either refuses to boot or silently resets every setting to default,
including the ones that make the world what it is. A rename is atomic on POSIX,
so the file is either the old one or the new one and never neither.

Same directory on purpose: a rename across filesystems is not atomic, and /tmp
is very often a different filesystem.
*/
func replace(path, contents string) error {
	dir := filepath.Dir(path)

	tmp, err := os.CreateTemp(dir, ".garrison-*")
	if err != nil {
		return err
	}

	tmpName := tmp.Name()

	// Best effort: if anything below fails, do not leave the temp file behind.
	defer os.Remove(tmpName)

	// 🚨 The original's permissions, not the temp file's 0600. A config the
	// game runs as another user must stay readable by it, and a server that
	// will not start after an edit is a very confusing bug to attribute.
	if info, statErr := os.Stat(path); statErr == nil {
		_ = tmp.Chmod(info.Mode().Perm())
	}

	if _, err := tmp.WriteString(contents); err != nil {
		tmp.Close()

		return err
	}

	// 🚨 Flushed to disk BEFORE the rename. Without it the rename can be
	// durable while the contents are not, and a power loss leaves a file that
	// exists, is the right size, and is full of zeroes.
	if err := tmp.Sync(); err != nil {
		tmp.Close()

		return err
	}

	if err := tmp.Close(); err != nil {
		return err
	}

	return os.Rename(tmpName, path)
}

func label(f File) string {
	if f.Label != "" {
		return f.Label
	}

	return f.ID
}

// allowSet returns a predicate for whether a key may be edited.
func allowSet(f File) func(string) bool {
	if len(f.Keys) == 0 {
		return func(string) bool { return true }
	}

	allowed := make(map[string]struct{}, len(f.Keys))
	for _, k := range f.Keys {
		allowed[k] = struct{}{}
	}

	return func(k string) bool {
		_, ok := allowed[k]

		return ok
	}
}

// List describes the declared files without reading them, for a panel that
// wants to offer a choice before loading anything.
func List(files []File) []Set {
	out := make([]Set, 0, len(files))

	for _, f := range files {
		out = append(out, Set{ID: f.ID, Label: label(f), Format: f.Format, ReadOnly: f.ReadOnly})
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Label < out[j].Label })

	return out
}
